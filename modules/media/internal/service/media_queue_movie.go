package service

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

func (s *Service) drainPendingMovies(ctx context.Context) error {
	jobs, err := s.db.PendingMediaJobs("movie", 1)
	if err != nil || len(jobs) == 0 {
		return err
	}
	if !s.acquireMediaFileWriter() {
		return nil
	}
	defer s.mediaFiles.Unlock()
	if !s.javspLock.TryLock() {
		return nil
	}
	defer s.javspLock.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	job, runID, err := s.db.claimMovieWithHistory(jobs[0].ID, jobs[0].Version)
	if errors.Is(err, errMediaJobConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	jobCtx, finish := s.mediaJobContext(ctx, job)
	defer finish()
	result, runErr := s.runQueuedMovie(jobCtx, job)
	status, phase, message := "success", "done", ""
	if runErr != nil {
		status, phase, message = "failed", "organize", runErr.Error()
		if len(result.Movies) > 0 && len(result.Movies[0].OutputPaths) > 0 {
			status = "partial"
		}
		if errors.Is(runErr, errIntakeConflict) || errors.Is(runErr, errIntakePending) {
			status = "needs_attention"
		}
		if errors.Is(runErr, context.DeadlineExceeded) {
			status = "timed_out"
		}
		if errors.Is(runErr, context.Canceled) {
			status = "canceled"
		}
		if errors.Is(runErr, context.Canceled) && s.runCtx != nil && s.runCtx.Err() != nil {
			status = "pending"
		}
	}
	paths := []string{}
	for _, movie := range result.Movies {
		paths = append(paths, movie.OutputPaths...)
	}
	_, transitionErr := s.db.TransitionMediaJob(job.ID, job.Version, MediaJobTransition{Phase: phase, Status: status, Error: message, OutputPaths: paths})
	if errors.Is(transitionErr, errMediaJobConflict) {
		transitionErr = nil
	}
	code := result.ExitCode
	var exitCode *int
	if runErr == nil {
		exitCode = &code
	}
	historyStatus := status
	if status == "pending" {
		// Shutdown requeues the business job; this particular attempt ended.
		historyStatus = "canceled"
	}
	historyErr := s.db.FinishJavspRun(runID, historyStatus, exitCode, result.Output, message)
	return errors.Join(transitionErr, historyErr)
}

func (s *Service) runQueuedMovie(ctx context.Context, job MediaJob) (JavspResult, error) {
	var result JavspResult
	if err := ctx.Err(); err != nil {
		return result, err
	}
	cfg, err := s.db.JavspConfigPrivate()
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	plan := job.MoviePlan
	if plan == nil || !plan.MetadataReady {
		sourceRoot, sources, err := queuedMovieSources(cfg.InputDirectory, job)
		if err != nil {
			return result, err
		}
		// The source boundary and naming anchor are independent. In particular,
		// intake can own /media/pending while a legacy relative template is
		// rooted at /media. Keep the original config for plan validation and
		// expand only the engine's in-memory template before narrowing its scan.
		if cfg.MoveFiles && cfg.OutputDirectory == "" {
			cfg.InputDirectory, err = canonicalDirectory(cfg.InputDirectory)
			if err != nil {
				return result, err
			}
		}
		engineConfig := cfg
		engineConfig.InputDirectory = sourceRoot
		if cfg.MoveFiles && cfg.OutputDirectory == "" {
			engineConfig.OutputFolderPattern = filepath.Join(cfg.InputDirectory, cfg.OutputFolderPattern)
		}
		s.javsp.BeginRun()
		result, err = s.javsp.RunSelected(ctx, engineConfig, sources, JavspExecutionHooks{
			BeforeOrganize: func(ctx context.Context, movie JavspMoviePlan) error {
				prepared, err := prepareMoviePlan(ctx, job, cfg, movie)
				if err != nil {
					return err
				}
				if err := s.db.SaveMoviePlan(job.ID, job.Version, prepared); err != nil {
					return err
				}
				plan = &prepared
				return nil
			},
			MoveFiles: func(ctx context.Context, movie JavspMoviePlan) (JavspMoveResult, error) {
				if plan == nil || !reflect.DeepEqual(plan.Plan, movie) {
					return JavspMoveResult{}, fmt.Errorf("%w：整理计划尚未确认", errIntakeConflict)
				}
				return s.moveQueuedMovie(ctx, job, plan)
			},
		})
		if err != nil {
			return result, err
		}
		if result.Total != 1 || result.Processed != 1 || result.Failed != 0 || plan == nil {
			return result, fmt.Errorf("整理引擎未确认该影片完成")
		}
	}
	// This path also repairs the crash window after rename but before saving
	// completion. File identity, not a same-named target, proves the move.
	moved, err := s.moveQueuedMovie(ctx, job, plan)
	result.Total = 1
	result.Movies = []JavspMovieOutcome{{MediaID: plan.Plan.MediaID, SourcePaths: job.movieSources(), OutputPaths: moved.OutputPaths, NFOPath: plan.Plan.NFOPath, PosterPath: plan.Plan.PosterPath, FanartPath: plan.Plan.FanartPath, Status: "partial"}}
	if err != nil {
		return result, err
	}
	if err := finalizeQueuedMovie(ctx, *plan); err != nil {
		return result, err
	}
	result.Processed = 1
	result.Failed = 0
	result.Movies[0].Status = "success"
	return result, nil
}

func finalizeQueuedMovie(ctx context.Context, plan queuedMoviePlan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(plan.Root)
	if err != nil {
		return err
	}
	defer root.Close()
	source, err := moviePlanRelative(plan.Root, plan.Plan.NFOPath)
	if err != nil {
		return err
	}
	data, err := readQueuedNFO(root, source)
	if err != nil {
		return err
	}
	var metadata struct {
		XMLName xml.Name
		Title   string `xml:"title"`
	}
	if err := xml.Unmarshal(data, &metadata); err != nil || metadata.XMLName.Local != "movie" || strings.TrimSpace(metadata.Title) == "" {
		return fmt.Errorf("%w：整理元数据缺少有效标题", errIntakeConflict)
	}
	for _, file := range plan.Plan.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := strings.TrimSuffix(file.Destination, filepath.Ext(file.Destination)) + ".nfo"
		dst, err := moviePlanRelative(plan.Root, path)
		if err != nil {
			return err
		}
		if dst == source {
			continue
		}
		if err := rejectIntakeLinks(root, dst, true); err != nil {
			return err
		}
		if existing, err := readQueuedNFO(root, dst); err == nil {
			if !bytes.Equal(data, existing) {
				return fmt.Errorf("%w：同名 NFO 已存在且内容不同", errIntakeConflict)
			}
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := publishQueuedNFO(ctx, root, dst, bytes.NewReader(data)); err != nil {
			return err
		}
	}
	return nil
}

func readQueuedNFO(root *os.Root, path string) ([]byte, error) {
	if err := rejectIntakeLinks(root, path, false); err != nil {
		return nil, err
	}
	info, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	const limit = 5 << 20
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("无效的整理 NFO 文件")
	}
	f, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if len(data) > limit {
		return nil, fmt.Errorf("整理 NFO 过大")
	}
	return data, err
}
