package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

var errQBSubmissionRejected = errors.New("qBittorrent 明确拒绝投递")
var errRSSSubmissionUnconfirmed = errors.New("qB 投递结果待确认")

func (session *QBSession) Torrent(ctx context.Context, hash string) (QBTorrent, bool, error) {
	if _, ok := magnetInfoHash("magnet:?xt=urn:btih:" + hash); !ok {
		return QBTorrent{}, false, fmt.Errorf("无效的种子 hash")
	}
	body, err := qbRequest(ctx, session.client, session.base, http.MethodGet, "/api/v2/torrents/info?hashes="+url.QueryEscape(hash), nil, "")
	if err != nil {
		return QBTorrent{}, false, err
	}
	var rows []QBTorrent
	if err := json.Unmarshal(body, &rows); err != nil {
		return QBTorrent{}, false, fmt.Errorf("qBittorrent 任务查询响应无效：%w", err)
	}
	for _, torrent := range rows {
		if strings.EqualFold(torrent.Hash, hash) {
			return torrent, true, nil
		}
	}
	return QBTorrent{}, false, nil
}

func (d *Database) skipRSSAcquisition(id, version int64, reason string) (MediaJob, error) {
	return d.checkpointRSS(id, version, "acquisition_skipped", func(_ *sql.Tx, job *MediaJob) error {
		job.Status, job.Phase, job.Error = "skipped", "done", reason
		job.Discovery.Entry.Summary = ""
		return nil
	})
}

func (s *Service) executeRSSAcquisition(parent context.Context, job MediaJob, settings Settings, session **QBSession) (RunDetail, error) {
	detail := RunDetail{JobID: job.ID, Title: job.Title, Status: "skipped", Message: "已登记到处理队列"}
	if job.Archived {
		detail.Message = "此条目已清理归档，保留去重，不再执行"
		return detail, nil
	}
	if job.Status == "skipped" {
		detail.Message = job.Error
		return detail, nil
	}
	if retryableMediaStatus(job.Status) {
		detail.Status, detail.Message = "failed", job.Error
		return detail, nil
	}
	if job.Status != "pending" || job.Phase != "discovered" {
		return detail, nil
	}
	job, err := s.db.ClaimMediaJob(job.ID, job.Version)
	if errors.Is(err, errMediaJobConflict) {
		return detail, nil
	}
	if err != nil {
		return detail, err
	}
	ctx, finish := s.mediaJobContext(parent, job)
	defer finish()
	ctx, cancel := context.WithTimeout(ctx, rssJobTimeout)
	defer cancel()
	fail := func(cause error) (RunDetail, error) {
		status := "failed"
		if errors.Is(cause, errIntakeConflict) || errors.Is(cause, errRSSSubmissionUnconfirmed) {
			status = "needs_attention"
		}
		if errors.Is(cause, context.DeadlineExceeded) {
			status = "timed_out"
		}
		if errors.Is(cause, context.Canceled) {
			status = "canceled"
		}
		if errors.Is(cause, context.Canceled) && s.runCtx != nil && s.runCtx.Err() != nil {
			status = "pending"
		}
		detail.Status, detail.Message = "failed", boundedText(cause.Error(), 2048)
		_, err := s.db.TransitionMediaJob(job.ID, job.Version, MediaJobTransition{Phase: "discovered", Status: status, Error: detail.Message})
		if errors.Is(err, errMediaJobConflict) {
			err = nil
		}
		return detail, err
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if job.Discovery == nil {
		return fail(fmt.Errorf("%w：RSS 来源信息缺失", errIntakeConflict))
	}
	task, err := s.db.Task(job.RSSID)
	if err != nil {
		return fail(fmt.Errorf("%w：原订阅不可读取：%v", errIntakeConflict, err))
	}
	magnet := job.Discovery.Magnet
	if magnet == "" {
		magnet, err = s.collector.ResolveMagnet(ctx, job.Discovery.Entry, task, settings)
		if err != nil {
			return fail(err)
		}
	}
	reserved, err := s.db.ReserveRSSDownload(job.ID, job.Version, magnet)
	if err != nil {
		return fail(err)
	}
	job = reserved
	if job.Status == "skipped" {
		detail.Message = job.Error
		return detail, nil
	}
	// Older releases only persisted the hash of the complete magnet URI.
	// Preserve final markers, but do not label an interrupted submission as
	// success or permit a blind repost when its outcome is still unknown.
	legacyStatus, legacySeen, err := s.db.seenStatus(job.RSSID, fingerprint(magnet))
	if err != nil {
		return fail(err)
	}
	if legacySeen && (legacyStatus == "submitted" || legacyStatus == "already_present") {
		reason := "旧版本已记录投递，保留历史去重结果"
		_, err := s.db.skipRSSAcquisition(job.ID, job.Version, reason)
		detail.Message = reason
		return detail, err
	}
	if *session == nil {
		*session, err = s.collector.OpenQBSession(ctx, settings)
		if err != nil {
			return fail(err)
		}
	}
	torrent, present, err := (*session).Torrent(ctx, job.TorrentHash)
	if err != nil {
		return fail(err)
	}
	observe := func(torrent QBTorrent, submitted bool) (RunDetail, error) {
		if _, err := s.db.ObserveRSSDownload(job.ID, job.Version, torrent); err != nil {
			return fail(err)
		}
		detail.Status, detail.Message = "skipped", "qBittorrent 中已存在，已关联下载任务"
		marker := "already_present"
		if submitted {
			detail.Status, detail.Message, marker = "submitted", "已确认 qBittorrent 下载任务", "submitted"
		}
		// Queue identity is authoritative; this marker preserves older RSS
		// history semantics without being the only record of dispatched work.
		if err := s.db.MarkSeen(job.RSSID, job.Discovery.Entry, magnet, marker); err != nil {
			return detail, err
		}
		return detail, nil
	}
	if present {
		return observe(torrent, false)
	}
	if legacySeen {
		return fail(fmt.Errorf("%w：旧版本记录的投递结果未确认；不会重新投递，请核对 qB 后重试确认", errIntakeConflict))
	}
	state := job.Discovery.SubmissionState
	if state == "dispatching" || state == "acknowledged" {
		return fail(fmt.Errorf("%w：将自动查询原种子，不会重复下发", errRSSSubmissionUnconfirmed))
	}
	// Queued discoveries can resume long after the feed was checked. Recheck
	// an explicitly configured library before a new submission, while leaving
	// already-dispatched jobs on the observation-only path above.
	cfg, err := s.db.JavspConfig()
	if err != nil {
		return fail(err)
	}
	if cfg.OutputDirectory != "" {
		index, err := s.completedIndex(ctx, cfg)
		if err != nil {
			return fail(fmt.Errorf("成品媒体库无法核对，已暂停投递，请检查整理完成目录：%w", err))
		}
		if mediaID, completed := index.MatchTitle(job.Title); completed {
			reason := "整理完成目录中已存在：" + mediaID
			_, err := s.db.skipRSSAcquisition(job.ID, job.Version, reason)
			detail.Message = reason
			return detail, err
		}
	}
	if err := s.db.SaveRSSSubmission(job.ID, job.Version, "dispatching"); err != nil {
		return fail(err)
	}
	// Only new discoveries have a frozen path. Legacy jobs keep their former
	// subscription/qB-default behavior; no existing qB task is relocated.
	if job.Discovery.DownloadPath != nil {
		task.SavePath = *job.Discovery.DownloadPath
	}
	submitErr := (*session).Submit(ctx, magnet, task)
	if submitErr == nil {
		if err := s.db.SaveRSSSubmission(job.ID, job.Version, "acknowledged"); err != nil {
			return fail(err)
		}
	}
	torrent, present, verifyErr := (*session).Torrent(ctx, job.TorrentHash)
	if verifyErr == nil && present {
		return observe(torrent, submitErr == nil)
	}
	if errors.Is(submitErr, errQBSubmissionRejected) && verifyErr == nil {
		if err := s.db.SaveRSSSubmission(job.ID, job.Version, "rejected"); err != nil {
			return fail(err)
		}
		return fail(submitErr)
	}
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	return fail(fmt.Errorf("%w：将自动查询原种子，不会重复下发", errRSSSubmissionUnconfirmed))
}
