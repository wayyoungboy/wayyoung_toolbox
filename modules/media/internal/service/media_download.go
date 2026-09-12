package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"path/filepath"
	"strings"
	"time"
)

// Observation is not permission to move files. Intake independently rechecks
// completion, qB identity, path boundaries, file identity and stopped state.
type DownloadObservation struct {
	Progress    float64 `json:"progress"`
	AmountLeft  int64   `json:"amount_left"`
	State       string  `json:"state"`
	ContentPath string  `json:"content_path"`
	Complete    bool    `json:"complete"`
	CheckedAt   string  `json:"checked_at"`
	ObservedAt  string  `json:"observed_at,omitempty"`
	Error       string  `json:"error,omitempty"`
}

// A separate goroutine in the same Go process keeps network polling independent
// of the organizer and subscription resolver. It only visits persisted jobs.
func (s *Service) downloadQueueWorker(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.refreshQueuedDownloads(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("refresh queued downloads", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Database) downloadsToObserve(ctx context.Context) ([]MediaJob, error) {
	// Oldest checked first prevents long-lived downloads from starving later
	// jobs. Bounds apply before any network or per-job database work.
	rows, err := d.db.QueryContext(ctx, `SELECT `+mediaJobColumns+` FROM media_jobs
WHERE kind='torrent' AND phase='download' AND status='pending'
ORDER BY COALESCE(json_extract(payload,'$.download.checked_at'),''),id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []MediaJob{}
	for rows.Next() {
		job, err := scanMediaJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (d *Database) recordDownloadObservation(ctx context.Context, expected MediaJob, torrent *QBTorrent, pollErr error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	job, err := scanMediaJob(tx.QueryRowContext(ctx, `SELECT `+mediaJobColumns+` FROM media_jobs WHERE id=?`, expected.ID))
	if err != nil {
		return err
	}
	if job.Version != expected.Version || job.Kind != "torrent" || job.Phase != "download" || job.Status != "pending" {
		return errMediaJobConflict
	}
	if job.Download == nil {
		job.Download = &DownloadObservation{}
	}
	job.Download.CheckedAt = nowUTC()
	var invalid string
	switch {
	case pollErr != nil:
		// Retain the last successful measurement, but never present it as live.
		job.Download.Error = "无法刷新 qB 下载状态：" + boundedText(pollErr.Error(), 1024)
	case torrent == nil:
		invalid = "原 qB 下载任务已不存在，请核对；不会重新投递"
	case job.TorrentAddedOn <= 0 || torrent.AddedOn != job.TorrentAddedOn || !strings.EqualFold(torrent.Hash, job.TorrentHash):
		invalid = "qB 下载任务身份已变化，请核对；不会关联重新添加的任务"
	case math.IsNaN(torrent.Progress) || math.IsInf(torrent.Progress, 0) || torrent.Progress < 0 || torrent.Progress > 1 || torrent.AmountLeft < 0:
		invalid = "qB 返回无效的下载进度，请核对"
	case torrent.ContentPath != "" && (!filepath.IsAbs(torrent.ContentPath) || hasParentSegment(torrent.ContentPath) || strings.ContainsRune(torrent.ContentPath, 0)):
		invalid = "qB 返回无效的内容路径，请核对"
	default:
		job.Download = &DownloadObservation{Progress: torrent.Progress, AmountLeft: torrent.AmountLeft, State: torrent.State, ContentPath: torrent.ContentPath, Complete: torrent.ContentPath != "" && torrentCompleted(*torrent), CheckedAt: nowUTC(), ObservedAt: nowUTC()}
		switch strings.ToLower(torrent.State) {
		case "error", "missingfiles", "unknown":
			invalid = "qB 下载状态异常：" + boundedText(torrent.State, 80) + "，请先在 qB 中处理后重试状态检查"
		}
	}
	if invalid != "" {
		job.Download.Error = invalid
		job.Error, job.Status = invalid, "needs_attention"
		job.Version++
	}
	job.UpdatedAt = nowUTC()
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE media_jobs SET payload=?,status=?,error=?,version=?,updated_at=? WHERE id=? AND version=? AND status='pending' AND phase='download'`, string(payload), job.Status, job.Error, job.Version, job.UpdatedAt, job.ID, expected.Version)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errMediaJobConflict
	}
	// Periodic progress must not fill the event log or invalidate a user's
	// cancel button. Only an actionable state transition advances the revision.
	if invalid != "" {
		if err := insertMediaEvent(tx, job, "download_attention"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) refreshQueuedDownloads(ctx context.Context) error {
	if !s.downloadLock.TryLock() {
		return nil
	}
	defer s.downloadLock.Unlock()
	jobs, err := s.db.downloadsToObserve(ctx)
	if err != nil || len(jobs) == 0 {
		return err
	}
	settings, err := s.db.Settings(true)
	if err != nil {
		return err
	}
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	session, pollErr := s.collector.OpenQBSession(requestCtx, settings)
	var torrents []QBTorrent
	if pollErr == nil {
		defer session.Close()
		torrents, pollErr = session.Torrents(requestCtx)
	}
	byHash := make(map[string]*QBTorrent, len(torrents))
	for i := range torrents {
		hash := strings.ToLower(torrents[i].Hash)
		if _, exists := byHash[hash]; exists {
			pollErr = fmt.Errorf("qB 返回重复的种子标识，无法确认下载状态")
			break
		}
		byHash[hash] = &torrents[i]
	}
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.db.recordDownloadObservation(ctx, job, byHash[strings.ToLower(job.TorrentHash)], pollErr); err != nil && !errors.Is(err, errMediaJobConflict) {
			return errors.Join(pollErr, err)
		}
	}
	return pollErr
}
