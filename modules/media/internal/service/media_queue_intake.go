package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Intake can register work while the organizer owns mediaFiles. Only the
// worker needs this lock; it never drops a request merely because it is busy.
func (s *Service) enqueueIntakeCandidate(ctx context.Context, item IntakeCandidate, config IntakeConfig) (MediaJob, error) {
	if strings.TrimSpace(item.Hash) == "" {
		return MediaJob{}, fmt.Errorf("种子 hash 为空")
	}
	if item.SourceRoot != "" {
		config.SourceDirectory = item.SourceRoot
	}
	id, _ := mediaIDFromVideoFilename(filepath.Base(item.Source))
	job := MediaJob{Key: fmt.Sprintf("torrent:%s:%d", strings.ToLower(item.Hash), item.AddedOn), Kind: "torrent", Title: item.Name, MediaID: id, TorrentHash: item.Hash, TorrentAddedOn: item.AddedOn, SourcePath: item.Source, TargetPath: item.Destination, Phase: "transfer", IntakeSourceRoot: config.SourceDirectory, IntakeTargetRoot: config.DestinationDirectory, Organize: config.RunJavsp}
	if job.Organize {
		files, err := intakePlannedMovies(ctx, item, config.SourceDirectory)
		if err != nil {
			return MediaJob{}, err
		}
		job.PlannedPaths = files
	}
	return s.db.registerIntakeJob(job)
}

func intakePlannedMovies(ctx context.Context, item IntakeCandidate, sourceDirectory string) ([]string, error) {
	root, err := os.OpenRoot(sourceDirectory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	rel, err := filepath.Rel(sourceDirectory, item.Source)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("来源目录越界")
	}
	if err := rejectIntakeLinks(root, rel, false); err != nil {
		return nil, err
	}
	files := []string{}
	scanned := 0
	err = fs.WalkDir(root.FS(), rel, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		scanned++
		if scanned > maxMaintenanceEntries {
			return fmt.Errorf("种子文件条目过多")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("种子目录含符号链接，请人工处理")
		}
		if entry.IsDir() || !isMaintenanceVideo(path) {
			return nil
		}
		suffix, err := filepath.Rel(rel, path)
		if err != nil {
			return err
		}
		if suffix == "." {
			files = append(files, item.Destination)
		} else {
			files = append(files, filepath.Join(item.Destination, suffix))
		}
		return nil
	})
	return files, err
}

func (s *Service) acquireMediaFileWriter() bool {
	if !s.mediaFiles.TryLock() {
		return false
	}
	blocked, err := s.db.MediaFilesBlocked()
	if err != nil || blocked {
		s.mediaFiles.Unlock()
		return false
	}
	return true
}

func (s *Service) drainIntakeQueue(ctx context.Context, session *QBSession, torrents []QBTorrent, result *IntakeRunResult) error {
	jobs, err := s.db.pendingMediaStages("torrent", []string{"transfer", "qb_cleanup"}, 100)
	if err != nil {
		return err
	}
	if !s.mediaFiles.TryLock() {
		result.Pending = len(jobs)
		return nil
	}
	defer s.mediaFiles.Unlock()
	for _, job := range jobs {
		if job.Phase != "transfer" && job.Phase != "qb_cleanup" {
			continue
		}
		if err := ctx.Err(); err != nil {
			result.Pending++
			continue
		}
		blocked, err := s.db.MediaFilesBlocked()
		if err != nil {
			return err
		}
		if blocked && !job.HoldFiles && job.Phase == "transfer" {
			result.Pending++
			continue
		}
		job, err = s.db.ClaimMediaJob(job.ID, job.Version)
		if errors.Is(err, errMediaJobConflict) {
			continue
		}
		if err != nil {
			return err
		}
		if job.Phase == "transfer" {
			jobCtx, finish := s.mediaJobContext(ctx, job)
			job, err = s.transferQueuedIntake(jobCtx, session, torrents, job, result)
			finish()
			if errors.Is(err, errMediaJobConflict) {
				continue
			}
			if err != nil {
				return err
			}
			if job.Status != "pending" {
				continue
			}
			job, err = s.db.ClaimMediaJob(job.ID, job.Version)
			if errors.Is(err, errMediaJobConflict) {
				continue
			}
			if err != nil {
				return err
			}
		}
		jobCtx, finish := s.mediaJobContext(ctx, job)
		err = s.finishQueuedIntake(jobCtx, session, job, result)
		finish()
		if err != nil && !errors.Is(err, errMediaJobConflict) {
			return err
		}
	}
	return nil
}

func (s *Service) failQueuedIntake(job MediaJob, err error, hold bool, result *IntakeRunResult) (MediaJob, error) {
	if job.Phase == "transfer" {
		needed, inspectErr := queuedIntakeNeedsHold(job)
		// Error wrapping is not a transaction boundary. An early cancellation or
		// inaccessible root must not release a previously captured source.
		hold = inspectErr != nil || needed
	}
	status := "failed"
	if errors.Is(err, errIntakeConflict) || errors.Is(err, errIntakePending) {
		status = "needs_attention"
	}
	if errors.Is(err, context.Canceled) {
		status = "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		status = "timed_out"
	}
	if errors.Is(err, context.Canceled) && s.runCtx != nil && s.runCtx.Err() != nil {
		status = "pending"
	}
	result.Failed++
	result.Errors = append(result.Errors, job.Title+"："+err.Error())
	return s.db.TransitionMediaJob(job.ID, job.Version, MediaJobTransition{Phase: job.Phase, Status: status, Error: err.Error(), HoldFiles: hold})
}

func (s *Service) transferQueuedIntake(ctx context.Context, session *QBSession, _ []QBTorrent, job MediaJob, result *IntakeRunResult) (MediaJob, error) {
	// Discovery snapshots cannot authorize a move: a queued torrent may have
	// changed since discovery. A receipt written before rename is not proof
	// that this process has captured the content either.
	if err := prepareQueuedIntake(ctx, session, job); err != nil {
		return s.failQueuedIntake(job, err, true, result)
	}
	item := IntakeCandidate{JobID: job.ID, Hash: job.TorrentHash, Name: job.Title, Source: job.SourcePath, Destination: job.TargetPath}
	outcome, err := moveIntakeCandidateContext(ctx, item, job.IntakeSourceRoot, job.IntakeTargetRoot)
	if err != nil {
		return s.failQueuedIntake(job, err, errors.Is(err, errIntakePending), result)
	}
	if outcome == "moved" {
		result.Moved++
	} else {
		result.DuplicatesCleaned++
	}
	return s.db.TransitionMediaJob(job.ID, job.Version, MediaJobTransition{Phase: "qb_cleanup", Status: "pending", TransferResult: outcome, OutputPaths: []string{job.TargetPath}})
}

func queuedIntakeNeedsHold(job MediaJob) (bool, error) {
	source, err := os.OpenRoot(job.IntakeSourceRoot)
	if err != nil {
		return true, err
	}
	defer source.Close()
	destination, err := os.OpenRoot(job.IntakeTargetRoot)
	if err != nil {
		return true, err
	}
	defer destination.Close()
	rel, err := filepath.Rel(job.IntakeSourceRoot, job.SourcePath)
	if err != nil || !filepath.IsLocal(rel) {
		return true, fmt.Errorf("队列来源路径越界")
	}
	exists, err := intakeOperationExists(source, destination, rel, job.ID)
	if err != nil || !exists {
		return false, err
	}
	journal, err := readIntakeOperation(source, destination, rel, job.ID)
	if err != nil {
		return true, err
	}
	if journal.State == "done" || journal.State == "restored" {
		_, err := source.Lstat(filepath.Join(".media-center-intake", intakeOperationName(destination, rel, job.ID), "content"))
		if os.IsNotExist(err) {
			return false, nil
		}
		return true, err
	}
	return true, nil
}

func queuedIntakeCaptured(job MediaJob) (bool, error) {
	source, err := os.OpenRoot(job.IntakeSourceRoot)
	if err != nil {
		return false, err
	}
	defer source.Close()
	destination, err := os.OpenRoot(job.IntakeTargetRoot)
	if err != nil {
		return false, err
	}
	defer destination.Close()
	rel, err := filepath.Rel(job.IntakeSourceRoot, job.SourcePath)
	if err != nil || !filepath.IsLocal(rel) {
		return false, fmt.Errorf("队列来源路径越界")
	}
	exists, err := intakeOperationExists(source, destination, rel, job.ID)
	if err != nil || !exists {
		return false, err
	}
	journal, err := readIntakeOperation(source, destination, rel, job.ID)
	if err != nil {
		return false, err
	}
	if journal.State == "done" {
		return true, nil
	}
	if journal.Outcome == "moved" && journal.State == "moving" {
		if _, err := source.Lstat(rel); !os.IsNotExist(err) {
			return false, err
		}
		if err := rejectIntakeLinks(destination, rel, false); err != nil {
			return false, err
		}
		info, err := destination.Lstat(rel)
		return err == nil && journal.Identity != "" && intakeFileIdentity(info) == journal.Identity, err
	}
	if journal.State == "cleaning" {
		// The cleanup manifest is durable before any captured entry is removed.
		// The cleanup implementation revalidates that manifest during recovery.
		return true, nil
	}
	if journal.State == "capturing" || journal.State == "captured" || journal.State == "restoring" {
		path := filepath.Join(".media-center-intake", intakeOperationName(destination, rel, job.ID), "content")
		if err := rejectIntakeLinks(source, path, false); err != nil {
			return false, err
		}
		_, err := source.Lstat(path)
		return err == nil, err
	}
	return false, nil
}

func (s *Service) finishQueuedIntake(ctx context.Context, session *QBSession, job MediaJob, result *IntakeRunResult) error {
	if err := ctx.Err(); err != nil {
		_, err = s.failQueuedIntake(job, err, false, result)
		return err
	}
	if job.Organize && !job.IntakeMoviesReady {
		children, err := s.discoverIntakeMovies(ctx, job)
		if err != nil {
			_, err = s.failQueuedIntake(job, err, false, result)
			return err
		}
		updated, created, err := s.db.saveIntakeMovies(ctx, job.ID, job.Version, children)
		if err != nil {
			if errors.Is(err, errMediaJobConflict) {
				return err
			}
			_, err = s.failQueuedIntake(job, err, false, result)
			return err
		}
		job = updated
		result.JavspQueued += created
		s.wakeMediaQueue()
	}
	if err := cleanupQueuedTorrent(ctx, session, job); err != nil {
		_, err = s.failQueuedIntake(job, fmt.Errorf("文件已搬运，删除 qB 记录失败：%w", err), false, result)
		return err
	}
	_, err := s.db.CompleteIntakeJob(job.ID, job.Version)
	if err == nil {
		result.TasksDeleted++
	}
	return err
}
