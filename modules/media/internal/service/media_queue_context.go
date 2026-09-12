package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
)

type mediaJobCancellation struct {
	cancel  context.CancelFunc
	version int64
}

func (s *Service) cancelMediaJobAttempt(id, version int64) {
	if value, ok := s.mediaJobCancels.Load(id); ok {
		entry := value.(*mediaJobCancellation)
		if entry.version == version {
			entry.cancel()
		}
	}
}

func (s *Service) mediaJobContext(parent context.Context, job MediaJob) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	entry := &mediaJobCancellation{cancel: cancel, version: job.Version}
	s.mediaJobCancels.Store(job.ID, entry)
	// Close the race where cancellation is persisted before this worker has
	// registered its cancel function. No side effect precedes this check.
	current, err := s.db.MediaJob(job.ID)
	if err != nil || current.Version != job.Version || current.Status != "running" {
		cancel()
	}
	return ctx, func() {
		cancel()
		if s.mediaJobCancels.CompareAndDelete(job.ID, entry) {
			if err := s.settleMediaJobCancellation(job); err != nil && !errors.Is(err, errMediaJobConflict) {
				slog.Warn("settle canceled media job", "job_id", job.ID, "error", err)
			}
		}
	}
}

// Called only after the worker has stopped writing, while it still owns the
// media-files mutex. Settle cancellation without undoing a completed rename.
func (s *Service) settleMediaJobCancellation(attempt MediaJob) error {
	current, err := s.db.MediaJob(attempt.ID)
	if err != nil {
		return err
	}
	if current.Status != "canceled" || current.Phase != "transfer" {
		return nil
	}
	needed, err := queuedIntakeNeedsHold(current)
	if err != nil || needed {
		return err
	}
	completed := ""
	if exists, err := queuedIntakeCaptured(current); err != nil {
		return err
	} else if exists {
		source, err := os.OpenRoot(current.IntakeSourceRoot)
		if err != nil {
			return err
		}
		defer source.Close()
		target, err := os.OpenRoot(current.IntakeTargetRoot)
		if err != nil {
			return err
		}
		defer target.Close()
		rel, err := filepath.Rel(current.IntakeSourceRoot, current.SourcePath)
		if err != nil {
			return err
		}
		receipt, err := readIntakeOperation(source, target, rel, current.ID)
		if err != nil {
			return err
		}
		if receipt.State == "done" {
			completed = receipt.Outcome
			if completed == "" {
				completed = "duplicate_removed"
			}
		}
	}
	_, err = s.db.changeMediaJob(current.ID, current.Version, "cancel_settled", func(job *MediaJob) error {
		if job.Status != "canceled" {
			return errMediaJobConflict
		}
		job.HoldFiles = false
		if completed != "" {
			job.Phase = "qb_cleanup"
			job.TransferResult = completed
			job.OutputPaths = []string{job.TargetPath}
		}
		return nil
	})
	return err
}

// Called before publishing Service or starting any worker. It inspects only
// known transaction receipts, never discovers or scans the media library.
func (s *Service) recoverCanceledMediaJobs() error {
	rows, err := s.db.db.Query(`SELECT id FROM media_jobs WHERE status='canceled' AND phase='transfer' AND json_extract(payload,'$.hold_files')=1`)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.settleMediaJobCancellation(MediaJob{ID: id}); err != nil {
			// Inaccessible or genuinely partial transactions keep their guard;
			// users can inspect/retry them without blocking application startup.
			slog.Warn("recover canceled media transaction", "job_id", id, "error", err)
		}
	}
	return nil
}
