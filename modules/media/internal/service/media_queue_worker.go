package service

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

func (s *Service) wakeMediaQueue() {
	select {
	case s.queueWake <- struct{}{}:
	default:
	}
}

// The queue is independent of discovery schedules: only existing durable jobs
// are resumed. Starting an empty queue never scans media directories or qB.
func (s *Service) queueWorker(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var intakeRetryAfter, movieRetryAfter time.Time
	for {
		if ctx.Err() != nil {
			return
		}
		if !time.Now().Before(intakeRetryAfter) {
			if err := s.drainPendingIntake(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("resume intake queue", "error", err)
				intakeRetryAfter = time.Now().Add(30 * time.Second)
			}
		}
		// Already queued local films do not depend on qB being reachable. A
		// storage failure gets its own backoff, separate from network retries.
		if !time.Now().Before(movieRetryAfter) {
			if err := s.drainPendingMovies(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("resume movie queue", "error", err)
				movieRetryAfter = time.Now().Add(30 * time.Second)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.queueWake:
		}
	}
}

func (s *Service) drainPendingIntake(ctx context.Context) error {
	if !s.intakeLock.TryLock() {
		return nil
	}
	defer s.intakeLock.Unlock()
	jobs, err := s.db.pendingMediaStages("torrent", []string{"transfer", "qb_cleanup"}, 100)
	if err != nil {
		return err
	}
	hasWork := false
	for _, job := range jobs {
		if job.Phase == "transfer" || job.Phase == "qb_cleanup" {
			hasWork = true
			break
		}
	}
	if !hasWork {
		return nil
	}
	// Avoid repeated qB logins while an in-process organizer owns the files.
	if !s.mediaFiles.TryLock() {
		return nil
	}
	s.mediaFiles.Unlock()
	settings, err := s.db.Settings(true)
	if err != nil {
		return err
	}
	session, err := s.collector.OpenQBSession(ctx, settings)
	if err != nil {
		return err
	}
	defer session.Close()
	torrents, err := session.Torrents(ctx)
	if err != nil {
		return err
	}
	runID, err := s.db.StartMaintenanceRun("intake", "queue")
	if err != nil {
		return err
	}
	result := IntakeRunResult{Scanned: len(torrents), Errors: []string{}}
	runErr := s.drainIntakeQueue(ctx, session, torrents, &result)
	status, message := "success", ""
	if result.Failed > 0 {
		status = "partial"
	}
	if runErr != nil {
		status, message = "failed", runErr.Error()
	}
	finishErr := s.db.FinishMaintenanceRun(runID, status, result.Scanned, len(jobs), result.TasksDeleted, result.Errors, message)
	return errors.Join(runErr, finishErr)
}
