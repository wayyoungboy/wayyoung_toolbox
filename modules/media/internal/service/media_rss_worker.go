package service

import (
	"context"
	"log/slog"
	"time"
)

// This is an in-process worker, separate from local file work: an unreachable
// RSS resolver must not stall already downloaded films. Recovery only uses
// saved entries, never fetching a feed or scanning a media directory.
func (s *Service) rssQueueWorker(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var retryAfter time.Time
	for {
		if ctx.Err() != nil {
			return
		}
		if !time.Now().Before(retryAfter) {
			if err := s.drainPendingRSS(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("resume RSS queue", "error", err)
				retryAfter = time.Now().Add(30 * time.Second)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) drainPendingRSS(ctx context.Context) error {
	if !s.acquireRSSSlot() {
		return nil
	}
	defer s.releaseRSSSlot()
	jobs, err := s.db.pendingMediaStages("torrent", []string{"discovered"}, 100)
	if err != nil || len(jobs) == 0 {
		return err
	}
	settings, err := s.db.Settings(true)
	if err != nil {
		return err
	}
	var session *QBSession
	defer func() {
		if session != nil {
			session.Close()
		}
	}()
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if job.Resource != nil {
			if _, err := s.executeAcquisition(ctx, job, settings, &session); err != nil {
				return err
			}
			continue
		}
		// Discovery may still be evaluating this subscription's deduplication
		// results. Do not steal its newly enqueued entries mid-run.
		lock := s.taskLock(job.RSSID)
		if !lock.TryLock() {
			continue
		}
		_, err := s.executeRSSAcquisition(ctx, job, settings, &session)
		lock.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}
