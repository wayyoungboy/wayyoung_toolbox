package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	javspengine "github.com/yuukiy/javsp-go/engine"
)

type movieDiscoveryFunc func(context.Context, javspengine.Options) (javspengine.DiscoveryResult, error)

// Only descendants need excluding. Separate output roots are already outside
// the scan boundary; passing them to Discover would correctly be rejected.
func javspDiscoveryExclusions(cfg JavspConfig) ([]string, error) {
	if !cfg.MoveFiles {
		return nil, nil
	}
	outputRoot, available, err := plannedJavspOutputRoot(cfg)
	if err != nil || !available {
		return nil, errors.Join(fmt.Errorf("请先设置与扫描目录区分的整理完成目录"), err)
	}
	canonical, err := filepath.EvalSymlinks(cfg.InputDirectory)
	if err != nil {
		return nil, err
	}
	for _, input := range []string{cfg.InputDirectory, canonical} {
		rel, err := filepath.Rel(input, outputRoot)
		if err == nil && filepath.IsLocal(rel) && rel != "." {
			return []string{outputRoot}, nil
		}
	}
	return nil, nil
}

type movieDiscoveryStatus struct {
	RunID        int64  `json:"run_id"`
	Status       string `json:"status"`
	Identified   int    `json:"identified"`
	Unrecognized int    `json:"unrecognized"`
	Queued       int    `json:"queued"`
	Existing     int    `json:"existing"`
	Error        string `json:"error,omitempty"`
}

func (s *Service) movieDiscoveryStatus() movieDiscoveryStatus {
	s.discoveryMu.Lock()
	defer s.discoveryMu.Unlock()
	return s.discoveryState
}

func (s *Service) handleMovieDiscoveryCancel(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		RunID int64 `json:"run_id"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.discoveryMu.Lock()
	defer s.discoveryMu.Unlock()
	if s.discoveryCancel == nil || s.discoveryState.RunID != payload.RunID {
		writeError(w, http.StatusConflict, fmt.Errorf("该文件发现任务已结束或发生变化，请刷新"))
		return
	}
	s.discoveryCancel()
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

// Scanning and execution are separate operations. The scanner uses a private
// configuration snapshot but does not prepare assets used by a running engine.
func (s *Service) startMovieDiscovery(ctx context.Context, trigger string) (int64, error) {
	if !s.javspDiscoveryLock.TryLock() {
		return 0, fmt.Errorf("JAVSP 文件发现正在运行")
	}
	started := false
	defer func() {
		if !started {
			s.javspDiscoveryLock.Unlock()
		}
	}()
	cfg, err := s.db.JavspConfigPrivate()
	if err != nil {
		return 0, err
	}
	data, err := s.javsp.prepareConfigDataContext(ctx, cfg, false)
	if err != nil {
		return 0, err
	}
	options := javspengine.Options{ConfigData: data, ResourceRoot: s.javsp.workRoot, InputDirectory: cfg.InputDirectory}
	options.ExcludedDirectories, err = javspDiscoveryExclusions(cfg)
	if err != nil {
		clear(data)
		return 0, err
	}
	runID, err := s.db.StartJavspRun(trigger + "_scan")
	if err != nil {
		clear(data)
		return 0, err
	}
	if err := s.db.MarkJavspStarted(); err != nil {
		clear(data)
		return 0, errors.Join(err, s.db.FinishJavspRun(runID, "failed", nil, "", err.Error()))
	}
	parent := s.runCtx
	if parent == nil {
		parent = context.Background()
	}
	jobCtx, cancel := context.WithTimeout(parent, 2*time.Minute)
	s.discoveryMu.Lock()
	s.discoveryCancel = cancel
	s.discoveryState = movieDiscoveryStatus{RunID: runID, Status: "running"}
	s.discoveryMu.Unlock()
	discover := s.discoverMovies
	if discover == nil {
		discover = javspengine.Discover
	}
	s.wait.Add(1)
	started = true
	go func() {
		defer s.wait.Done()
		defer s.javspDiscoveryLock.Unlock()
		defer clear(data)
		defer cancel()
		ctx := jobCtx
		result, err := discover(ctx, options)
		var jobs []MediaJob
		if err == nil {
			jobs, err = discoveredMovieJobs(ctx, cfg, runID, result)
		}
		queued, existing := 0, 0
		if err == nil {
			queued, existing, err = s.db.enqueueMovieDiscovery(ctx, jobs)
		}
		status, message := "success", ""
		if len(result.Unrecognized) > 0 {
			status = "partial"
		}
		if err != nil {
			status, message = "failed", err.Error()
			if errors.Is(err, context.Canceled) {
				status = "canceled"
			}
			if errors.Is(err, context.DeadlineExceeded) {
				status = "timed_out"
			}
		}
		output := fmt.Sprintf("文件发现完成：识别 %d 部，未识别 %d 个，新增队列 %d 项，已在队列 %d 项。此记录只表示扫描入队；刮削进度请查看处理队列。", len(result.Movies), len(result.Unrecognized), queued, existing)
		if finishErr := s.db.FinishJavspRun(runID, status, nil, output, message); finishErr != nil {
			slog.Error("finish movie discovery", "error", finishErr, "run_id", runID)
		}
		s.discoveryMu.Lock()
		s.discoveryCancel = nil
		s.discoveryState = movieDiscoveryStatus{RunID: runID, Status: status, Identified: len(result.Movies), Unrecognized: len(result.Unrecognized), Queued: queued, Existing: existing, Error: message}
		s.discoveryMu.Unlock()
		if err == nil {
			s.wakeMediaQueue()
		}
	}()
	return runID, nil
}

func discoveredMovieJobs(ctx context.Context, cfg JavspConfig, runID int64, result javspengine.DiscoveryResult) ([]MediaJob, error) {
	jobs := make([]MediaJob, 0, len(result.Movies)+len(result.Unrecognized))
	for _, movie := range result.Movies {
		job, err := movieDiscoveryJob(ctx, cfg.InputDirectory, movie.MediaID, movie.Files, runID)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	for _, file := range result.Unrecognized {
		job, err := movieDiscoveryJob(ctx, cfg.InputDirectory, "", []string{file.Path}, runID)
		if err != nil {
			return nil, err
		}
		job.Error = "未识别出番号或 CID；请核对文件名及扫描配置后再重试"
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func movieDiscoveryJob(ctx context.Context, root, mediaID string, paths []string, runID int64) (MediaJob, error) {
	job := MediaJob{Kind: "movie", Phase: "organize", MediaID: mediaID, DiscoveryRunID: runID}
	if len(paths) == 0 {
		return job, fmt.Errorf("影片缺少来源文件")
	}
	paths = append([]string(nil), paths...)
	sort.Strings(paths)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return job, err
		}
		canonical, source, err := queuedMovieSource(root, path)
		if err != nil {
			return job, err
		}
		if job.MovieSourceRoot != "" && job.MovieSourceRoot != canonical {
			return job, fmt.Errorf("%w：发现期间扫描根目录已变化", errIntakeConflict)
		}
		job.MovieSourceRoot = canonical
		info, err := os.Lstat(source)
		if err != nil {
			return job, err
		}
		if !info.Mode().IsRegular() || intakeFileIdentity(info) == "" {
			return job, fmt.Errorf("%w：发现文件身份不可确认", errIntakeConflict)
		}
		job.SourcePaths = append(job.SourcePaths, source)
		job.SourceIdentities = append(job.SourceIdentities, queuedMovieFile{Identity: intakeFileIdentity(info), Size: info.Size(), Modified: info.ModTime().UnixNano()})
	}
	job.SourcePath, job.Title = job.SourcePaths[0], filepath.Base(job.SourcePaths[0])
	job.Key = "movie:" + fingerprint(encodeJSON(struct {
		Paths      []string
		Identities []queuedMovieFile
	}{job.SourcePaths, job.SourceIdentities}))
	return job, nil
}

func (d *Database) enqueueMovieDiscovery(ctx context.Context, candidates []MediaJob) (int, int, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	queued, existing := 0, 0
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		_, created, err := enqueueDiscoveredMovieTx(ctx, tx, candidate)
		if err != nil {
			return 0, 0, err
		}
		if created {
			queued++
		} else {
			existing++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return queued, existing, nil
}

// Both manual discovery and intake use file-set identity, never the producer's
// run ID. An existing job keeps its lifecycle; overlapping different inputs
// require explicit resolution rather than changing a running selection.
func enqueueDiscoveredMovieTx(ctx context.Context, tx *sql.Tx, candidate MediaJob) (MediaJob, bool, error) {
	if err := validateMediaJob(candidate); err != nil {
		return MediaJob{}, false, err
	}
	if candidate.Kind != "movie" || candidate.Phase != "organize" {
		return MediaJob{}, false, fmt.Errorf("无效的影片发现任务")
	}
	job, err := scanMediaJob(tx.QueryRowContext(ctx, `SELECT `+mediaJobColumns+` FROM media_jobs WHERE job_key=?`, candidate.Key))
	if err == nil {
		return job, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return MediaJob{}, false, err
	}
	for _, path := range candidate.movieSources() {
		var id int64
		err = tx.QueryRowContext(ctx, `SELECT id FROM media_jobs WHERE kind='movie' AND phase<>'done' AND
(json_extract(payload,'$.source_path')=? OR EXISTS(SELECT 1 FROM json_each(payload,'$.source_paths') WHERE value=?)) LIMIT 1`, path, path).Scan(&id)
		if err == nil {
			return MediaJob{}, false, fmt.Errorf("%w：文件已在任务 %d 中，请处理原任务", errIntakeConflict, id)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return MediaJob{}, false, err
		}
	}
	job, err = enqueueMediaJobTx(tx, candidate)
	if err != nil {
		return MediaJob{}, false, err
	}
	if candidate.Error != "" {
		job.Status, job.Error = "needs_attention", candidate.Error
		if _, err := tx.ExecContext(ctx, `UPDATE media_jobs SET status=?,error=?,payload=? WHERE id=?`, job.Status, job.Error, encodeJSON(job), job.ID); err != nil {
			return MediaJob{}, false, err
		}
		if err := insertMediaEvent(tx, job, "unrecognized_input"); err != nil {
			return MediaJob{}, false, err
		}
	}
	return job, true, nil
}
