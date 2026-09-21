package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func uncertainRSSJob(job MediaJob) bool {
	return job.Kind == "torrent" && job.Phase == "discovered" && job.Discovery != nil &&
		(job.Discovery.SubmissionState == "dispatching" || job.Discovery.SubmissionState == "acknowledged") &&
		(job.Status == "needs_attention" || job.Status == "failed" || job.Status == "timed_out")
}

func (d *Database) rssSubmissionsToConfirm(ctx context.Context) ([]MediaJob, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT `+mediaJobColumns+` FROM media_jobs
WHERE kind='torrent' AND phase='discovered' AND status IN ('needs_attention','failed','timed_out')
AND json_extract(payload,'$.discovery.submission_state') IN ('dispatching','acknowledged')
ORDER BY COALESCE(json_extract(payload,'$.discovery.confirmation_checked_at'),''),id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []MediaJob
	for rows.Next() {
		job, err := scanMediaJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// Reconcile observation only: never submit, resolve a magnet, retry a canceled
// task, move a file, or increment submission attempts. Intake retains its own
// completion/content/identity checks before any subsequent transfer.
func (s *Service) reconcileRSSSubmissions(ctx context.Context) error {
	jobs, err := s.db.rssSubmissionsToConfirm(ctx)
	if err != nil || len(jobs) == 0 {
		return err
	}
	settings, err := s.db.Settings(true)
	if err != nil {
		return err
	}
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	session, loginErr := s.collector.OpenQBSession(requestCtx, settings)
	if session != nil {
		defer session.Close()
	}
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		var torrent *QBTorrent
		pollErr := loginErr
		if pollErr == nil {
			value, present, err := session.Torrent(requestCtx, job.TorrentHash)
			pollErr = err
			if present {
				torrent = &value
			}
		}
		if err := s.db.recordRSSConfirmation(ctx, job, torrent, pollErr); err != nil && !errors.Is(err, errMediaJobConflict) {
			return err
		}
		if requestCtx.Err() != nil {
			return requestCtx.Err()
		}
	}
	return loginErr
}

func (d *Database) recordRSSConfirmation(ctx context.Context, expected MediaJob, torrent *QBTorrent, pollErr error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	job, err := scanMediaJob(tx.QueryRowContext(ctx, `SELECT `+mediaJobColumns+` FROM media_jobs WHERE id=?`, expected.ID))
	if err != nil {
		return err
	}
	if job.Version != expected.Version || !uncertainRSSJob(job) {
		return errMediaJobConflict
	}
	message := "qB 投递结果待确认：暂未查询到原种子；将自动查询，不会重复投递"
	confirmed := false
	if pollErr != nil {
		message = "qB 投递结果待确认：查询暂时失败；将自动重查，不会重复投递"
	} else if torrent != nil {
		// Legacy jobs already persisted this event before sending their POST.
		// Do not adopt an unrelated, later re-addition of the same hash.
		var dispatchedAt string
		err := tx.QueryRowContext(ctx, `SELECT created_at FROM media_job_events WHERE job_id=? AND reason='submission_dispatching' ORDER BY id DESC LIMIT 1`, job.ID).Scan(&dispatchedAt)
		stamp, parseErr := time.Parse(time.RFC3339, dispatchedAt)
		if err != nil || parseErr != nil || torrent.AddedOn < stamp.Unix()-30 || torrent.AddedOn > stamp.Add(rssJobTimeout).Unix() || !strings.EqualFold(torrent.Hash, job.TorrentHash) {
			message = "qB 任务身份无法匹配原投递时间，请人工核对；不会接管或重复投递"
		} else {
			candidate := job
			candidate.SourcePath, candidate.TorrentAddedOn = torrent.ContentPath, torrent.AddedOn
			candidate.Phase, candidate.Status, candidate.Error = "download", "pending", ""
			if err := validateMediaJob(candidate); err != nil || torrent.ContentPath == "" {
				message = "qB 返回的内容路径无效，请人工核对；不会搬运或重复投递"
			} else {
				job, confirmed = candidate, true
				job.Discovery.Entry.Summary = ""
				job.Version++
			}
		}
	}
	if !confirmed {
		job.Error = message
	}
	job.Discovery.ConfirmationCheckedAt, job.UpdatedAt = nowUTC(), nowUTC()
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE media_jobs SET payload=?,phase=?,status=?,error=?,version=?,updated_at=? WHERE id=? AND version=?`, string(payload), job.Phase, job.Status, job.Error, job.Version, job.UpdatedAt, job.ID, expected.Version)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return fmt.Errorf("%w：确认结果未保存", errMediaJobConflict)
	}
	// Polls should not inflate event history or invalidate the cancel button.
	if confirmed {
		if err := insertMediaEvent(tx, job, "submission_confirmed"); err != nil {
			return err
		}
	}
	return tx.Commit()
}
