package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func queuedTorrent(ctx context.Context, session *QBSession, hash string) (*QBTorrent, error) {
	torrents, err := session.Torrents(ctx)
	if err != nil {
		return nil, err
	}
	for _, torrent := range torrents {
		if strings.EqualFold(torrent.Hash, hash) {
			return &torrent, nil
		}
	}
	return nil, nil
}

func validateQueuedTorrent(torrent *QBTorrent, job MediaJob, captured bool) error {
	if torrent == nil {
		if captured {
			return nil
		}
		return fmt.Errorf("下载任务已不存在")
	}
	if job.TorrentAddedOn <= 0 || job.TorrentAddedOn != torrent.AddedOn {
		return fmt.Errorf("%w：无法确认原 qB 任务身份，任务可能已重新添加", errIntakeConflict)
	}
	rel, err := intakeRelativePath(job.IntakeSourceRoot, job.IntakeSourceRoot, torrent.ContentPath)
	if err != nil || filepath.Join(job.IntakeSourceRoot, rel) != job.SourcePath {
		return fmt.Errorf("%w：qB 下载路径已变化", errIntakeConflict)
	}
	if !captured && !torrentCompleted(*torrent) {
		return fmt.Errorf("下载任务尚未完成")
	}
	return nil
}

func cleanupQueuedTorrent(ctx context.Context, session *QBSession, job MediaJob) error {
	torrent, err := queuedTorrent(ctx, session, job.TorrentHash)
	if err != nil {
		return err
	}
	if torrent == nil {
		// Deleting a record is idempotent even if the prior response was lost.
		return nil
	}
	if err := validateQueuedTorrent(torrent, job, true); err != nil {
		return err
	}
	return session.DeleteTorrentRecord(ctx, job.TorrentHash)
}

func queuedTorrentStopped(torrent *QBTorrent) bool {
	if torrent == nil {
		return false
	}
	switch strings.ToLower(torrent.State) {
	case "stoppedup", "pausedup", "stoppeddl", "pauseddl":
		return true
	default:
		return false
	}
}

func prepareQueuedIntake(ctx context.Context, session *QBSession, job MediaJob) error {
	captured, err := queuedIntakeCaptured(job)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	torrent, err := queuedTorrent(ctx, session, job.TorrentHash)
	if err != nil {
		return err
	}
	if captured && torrent != nil && job.TorrentAddedOn > 0 && torrent.AddedOn > 0 && job.TorrentAddedOn != torrent.AddedOn {
		// Recover only our isolated old transaction. The replacement qB record
		// and any new content at the public source path belong to another job.
		return nil
	}
	if err := validateQueuedTorrent(torrent, job, captured); err != nil {
		return err
	}
	if torrent == nil || queuedTorrentStopped(torrent) {
		return nil
	}
	if err := session.StopTorrent(ctx, job.TorrentHash); err != nil {
		return err
	}
	for {
		torrent, err = queuedTorrent(ctx, session, job.TorrentHash)
		if err != nil {
			return err
		}
		if err := validateQueuedTorrent(torrent, job, captured); err != nil {
			return err
		}
		if torrent == nil || queuedTorrentStopped(torrent) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("未确认 qB 停止，未执行搬运：%w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
