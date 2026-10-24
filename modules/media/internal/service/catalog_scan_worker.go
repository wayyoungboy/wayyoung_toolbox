package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

func (s *Service) wakeCatalogScans() {
	select {
	case s.catalogScanWake <- struct{}{}:
	default:
	}
}

func (s *Service) cancelCatalogScanAttempt(id, version int64) {
	if value, ok := s.catalogScanCancels.Load(id); ok {
		entry := value.(*mediaJobCancellation)
		if entry.version == version {
			entry.cancel()
		}
	}
}

func (s *Service) catalogScanContext(parent context.Context, job catalog.ScanJob) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	entry := &mediaJobCancellation{cancel: cancel, version: job.Version}
	s.catalogScanCancels.Store(job.ID, entry)
	// Cover the race where a pause/cancel commits before registration. Do not
	// touch the filesystem until the persistent revision has been checked.
	current, err := s.db.catalog.ScanJob(ctx, job.ID)
	if err != nil || current.Version != job.Version || current.Status != "running" {
		cancel()
	}
	return ctx, func() {
		cancel()
		s.catalogScanCancels.CompareAndDelete(job.ID, entry)
	}
}

func (s *Service) catalogScanWorker(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.drainCatalogScans(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("catalog scan attempt stopped", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.catalogScanWake:
		}
	}
}

// Only explicit durable jobs are resumed. One media-files guard covers the
// entire attempt, with lock order mediaFiles -> catalog.scanMu -> SQLite tx.
func (s *Service) drainCatalogScans(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	job, err := s.db.catalog.NextPendingScan(ctx)
	if errors.Is(err, catalog.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !s.acquireMediaFileWriter() {
		return nil
	}
	defer s.mediaFiles.Unlock()
	job, err = s.db.catalog.ClaimScan(ctx, job.ID, job.Version)
	if errors.Is(err, catalog.ErrConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	attemptCtx, cleanup := s.catalogScanContext(ctx, job)
	defer cleanup()
	defer func() {
		// Success/failure/pause/cancel has a newer revision and cannot be
		// overwritten. A still-running interruption retains its traversal.
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.db.catalog.ReleaseScanAttempt(releaseCtx, job.ID, job.Version); err != nil && !errors.Is(err, catalog.ErrConflict) {
			slog.Warn("release interrupted catalog scan", "job_id", job.ID, "error", err)
		}
	}()
	for {
		if err := attemptCtx.Err(); err != nil {
			return err
		}
		current, err := s.db.catalog.ScanBatch(attemptCtx, job.ID, job.Version, 64)
		if err != nil {
			return err
		}
		if current.Status != "running" {
			return nil
		}
	}
}
