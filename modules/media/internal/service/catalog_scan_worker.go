package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

type catalogScanCancellation struct {
	cancel  context.CancelFunc
	version int64
	control sync.Mutex // administrative CAS must settle before attempt release
}

func (s *Service) wakeCatalogScans() {
	select {
	case s.catalogScanWake <- struct{}{}:
	default:
	}
}

func (s *Service) cancelCatalogScanAttempt(id, version int64) {
	if value, ok := s.catalogScanCancels.Load(id); ok {
		entry := value.(*catalogScanCancellation)
		if entry.version == version {
			entry.cancel()
		}
	}
}

func (s *Service) lockCatalogScanControl(id, version int64) func() {
	if value, ok := s.catalogScanCancels.Load(id); ok {
		entry := value.(*catalogScanCancellation)
		if entry.version == version {
			entry.control.Lock()
			if current, ok := s.catalogScanCancels.Load(id); ok && current == entry {
				entry.cancel()
				return entry.control.Unlock
			}
			entry.control.Unlock()
		}
	}
	return func() {}
}

func (s *Service) catalogScanContext(parent context.Context, job catalog.ScanJob) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	entry := &catalogScanCancellation{cancel: cancel, version: job.Version}
	s.catalogScanCancels.Store(job.ID, entry)
	// Cover the race where a pause/cancel commits before registration. Do not
	// touch the filesystem until the persistent revision has been checked.
	current, err := s.db.catalog.ScanJob(ctx, job.ID)
	if err != nil || current.Version != job.Version || current.Status != "running" {
		cancel()
	}
	return ctx, func() {
		entry.control.Lock()
		defer entry.control.Unlock()
		cancel()
		if !s.catalogScanCancels.CompareAndDelete(job.ID, entry) {
			return
		}
		// An in-flight pause/cancel has now settled. A newer lifecycle revision
		// cannot be overwritten; shutdown preserves a still-running cursor.
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer releaseCancel()
		if err := s.db.catalog.ReleaseScanAttempt(releaseCtx, job.ID, job.Version); err != nil && !errors.Is(err, catalog.ErrConflict) {
			slog.Warn("release interrupted catalog scan", "job_id", job.ID, "error", err)
		}
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
