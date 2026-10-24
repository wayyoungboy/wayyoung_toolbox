package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// The original entry survives rotated run logs. Only resolver input and the
// subscription identity and download destination are captured; credentials
// remain in private settings.
type RSSDiscovery struct {
	SubscriptionName      string    `json:"subscription_name"`
	Entry                 FeedEntry `json:"entry"`
	Magnet                string    `json:"magnet,omitempty"`
	SubmissionState       string    `json:"submission_state,omitempty"`
	ConfirmationCheckedAt string    `json:"confirmation_checked_at,omitempty"`
	// nil identifies legacy jobs; a non-nil empty string deliberately retains
	// qB's default directory instead of adopting a later shared configuration.
	DownloadPath *string `json:"download_path,omitempty"`
}

func rssDownloadPath(task Task, intake IntakeConfig) string {
	if path := strings.TrimSpace(task.SavePath); path != "" {
		return path
	}
	return strings.TrimSpace(intake.SourceDirectory)
}

func (d *Database) EnqueueRSSAcquisition(task Task, entry FeedEntry) (MediaJob, error) {
	return d.enqueueRSSAcquisition(task, entry, "")
}

func (d *Database) enqueueRSSAcquisition(task Task, entry FeedEntry, knownReason string) (MediaJob, error) {
	if task.ID < 1 {
		return MediaJob{}, fmt.Errorf("订阅编号无效")
	}
	identity := entry.EntryID
	if identity == "" {
		identity = entry.SourceURL
	}
	if identity == "" {
		identity = entry.Title + "\x00" + entry.Summary
	}
	if strings.TrimSpace(identity) == "\x00" {
		return MediaJob{}, fmt.Errorf("订阅条目缺少身份信息")
	}
	id, _ := mediaIDFromTitle(entry.Title)
	key := fingerprint(identity)
	tx, err := d.db.Begin()
	if err != nil {
		return MediaJob{}, err
	}
	defer tx.Rollback()
	stored, err := readSettings(context.Background(), tx)
	if err != nil {
		return MediaJob{}, err
	}
	intake := intakeConfigFromSettings(stored)
	downloadPath := rssDownloadPath(task, intake)
	intakeRoot := ""
	if filepath.IsAbs(intake.SourceDirectory) && filepath.Clean(intake.SourceDirectory) != string(filepath.Separator) && !hasParentSegment(intake.SourceDirectory) && !strings.ContainsRune(intake.SourceDirectory, 0) && configuredPathWithin(intake.SourceDirectory, downloadPath) {
		intakeRoot = intake.SourceDirectory
	}
	job, err := enqueueMediaJobTx(tx, MediaJob{Key: fmt.Sprintf("rss:%d:%s", task.ID, key), Kind: "torrent", Phase: "discovered", MediaID: id, Title: entry.Title, RSSID: task.ID, RSSItemKey: key, SourceURL: entry.SourceURL, IntakeSourceRoot: intakeRoot, Discovery: &RSSDiscovery{SubscriptionName: task.Name, Entry: entry, DownloadPath: &downloadPath}})
	if err != nil {
		return MediaJob{}, err
	}
	// Persist discovery's completed-library decision in the same transaction
	// as the job. Recovery cannot reinterpret a known duplicate as new work.
	// Never discard an in-flight or unconfirmed submission's existing state.
	if knownReason != "" && job.Status == "pending" && job.Phase == "discovered" && job.Discovery != nil && job.Discovery.SubmissionState == "" {
		job.Phase, job.Status, job.Error = "done", "skipped", boundedText(knownReason, 2048)
		job.Version++
		job.UpdatedAt = nowUTC()
		job.Discovery.Entry.Summary = ""
		payload, err := json.Marshal(job)
		if err != nil {
			return MediaJob{}, err
		}
		if _, err := tx.Exec(`UPDATE media_jobs SET phase=?,status=?,error=?,version=?,updated_at=?,payload=? WHERE id=?`, job.Phase, job.Status, job.Error, job.Version, job.UpdatedAt, string(payload), job.ID); err != nil {
			return MediaJob{}, err
		}
		if err := insertMediaEvent(tx, job, "acquisition_skipped"); err != nil {
			return MediaJob{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return MediaJob{}, err
	}
	return job, nil
}

// In-phase checkpoints keep the claim's cancellation revision stable. The
// reservation decision and all competing jobs are observed in one transaction.
func (d *Database) checkpointRSS(id, version int64, reason string, change func(*sql.Tx, *MediaJob) error) (MediaJob, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return MediaJob{}, err
	}
	defer tx.Rollback()
	job, err := scanMediaJob(tx.QueryRow(`SELECT `+mediaJobColumns+` FROM media_jobs WHERE id=?`, id))
	if err != nil {
		return MediaJob{}, err
	}
	if job.Version != version || job.Status != "running" || job.Kind != "torrent" || job.Phase != "discovered" || job.Discovery == nil {
		return MediaJob{}, errMediaJobConflict
	}
	if err := change(tx, &job); err != nil {
		return MediaJob{}, err
	}
	if job.Status != "running" {
		job.Version++
	}
	job.UpdatedAt = nowUTC()
	data, err := json.Marshal(job)
	if err != nil {
		return MediaJob{}, err
	}
	res, err := tx.Exec(`UPDATE media_jobs SET phase=?,status=?,version=?,payload=?,error=?,updated_at=? WHERE id=? AND version=? AND status='running'`, job.Phase, job.Status, job.Version, string(data), job.Error, job.UpdatedAt, id, version)
	if err != nil {
		return MediaJob{}, err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return MediaJob{}, err
	}
	if count != 1 {
		return MediaJob{}, errMediaJobConflict
	}
	if err := insertMediaEvent(tx, job, reason); err != nil {
		return MediaJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return MediaJob{}, err
	}
	return job, nil
}

func (d *Database) ReserveRSSDownload(id, version int64, magnet string) (MediaJob, error) {
	hash, ok := magnetInfoHash(magnet)
	if !ok {
		return MediaJob{}, fmt.Errorf("磁力链接缺少有效的 BTIH 标识")
	}
	return d.checkpointRSS(id, version, "download_reserved", func(tx *sql.Tx, job *MediaJob) error {
		if job.Discovery.Magnet != "" && job.TorrentHash != hash {
			return fmt.Errorf("已预留下载的磁力标识发生变化")
		}
		if job.Discovery.SubmissionState == "dispatching" || job.Discovery.SubmissionState == "acknowledged" {
			// This task may already exist remotely. A new owner of the same
			// catalog number cannot turn its outstanding POST into a skip.
			return nil
		}
		var owner int64
		err := tx.QueryRow(`SELECT id FROM media_jobs WHERE id<>? AND status<>'skipped' AND COALESCE(json_extract(payload,'$.related_job_id'),0)=0
AND (lower(json_extract(payload,'$.torrent_hash'))=? OR (media_id<>'' AND media_id=?))
		AND (COALESCE(json_extract(payload,'$.discovery.magnet'),'')<>'' OR json_extract(payload,'$.resource') IS NOT NULL OR phase<>'discovered') ORDER BY id LIMIT 1`, job.ID, hash, job.MediaID).Scan(&owner)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		job.TorrentHash = hash
		job.Discovery.Magnet = magnet
		if err == nil {
			job.Status, job.Phase, job.RelatedJobID = "skipped", "done", owner
			job.Error = "影片或种子已在处理队列中，本条目关联已有任务"
			job.Discovery.Entry.Summary = ""
		}
		return nil
	})
}

func (d *Database) SaveRSSSubmission(id, version int64, state string) error {
	_, err := d.checkpointRSS(id, version, "submission_"+state, func(_ *sql.Tx, job *MediaJob) error {
		if job.Discovery.Magnet == "" {
			return fmt.Errorf("尚未预留下载")
		}
		previous := job.Discovery.SubmissionState
		valid := state == "dispatching" && (previous == "" || previous == "rejected") || (state == "acknowledged" || state == "rejected") && previous == "dispatching"
		if !valid {
			return fmt.Errorf("无效的投递状态转换")
		}
		job.Discovery.SubmissionState = state
		return nil
	})
	return err
}

func (d *Database) ObserveRSSDownload(id, version int64, torrent QBTorrent) (MediaJob, error) {
	return d.changeMediaJob(id, version, "download_observed", func(job *MediaJob) error {
		if job.Status != "running" || job.Phase != "discovered" || job.Discovery == nil {
			return errMediaJobConflict
		}
		if torrent.AddedOn <= 0 || !strings.EqualFold(torrent.Hash, job.TorrentHash) {
			return fmt.Errorf("%w：无法确认 qB 任务身份", errIntakeConflict)
		}
		job.TorrentAddedOn = torrent.AddedOn
		job.Phase, job.Status = "download", "pending"
		job.SourcePath = torrent.ContentPath
		if err := validateMediaJob(*job); err != nil {
			return fmt.Errorf("%w：qB 返回的内容路径无效：%v", errIntakeConflict, err)
		}
		job.Discovery.Entry.Summary = ""
		return nil
	})
}

// Discovery of completed downloads may coincide with RSS dispatch. Reuse the
// original queue identity; never bypass an unconfirmed/canceled submission or
// rewind a transfer whose qB cleanup is already pending.
func (d *Database) registerIntakeJob(candidate MediaJob) (MediaJob, error) {
	if err := validateMediaJob(candidate); err != nil {
		return MediaJob{}, err
	}
	tx, err := d.db.Begin()
	if err != nil {
		return MediaJob{}, err
	}
	defer tx.Rollback()
	job, err := scanMediaJob(tx.QueryRow(`SELECT `+mediaJobColumns+` FROM media_jobs
WHERE kind='torrent' AND status<>'skipped' AND json_extract(payload,'$.discovery') IS NOT NULL
AND lower(json_extract(payload,'$.torrent_hash'))=?
AND (json_extract(payload,'$.torrent_added_on')=? OR (phase='discovered' AND COALESCE(json_extract(payload,'$.torrent_added_on'),0)=0)) ORDER BY id LIMIT 1`, strings.ToLower(candidate.TorrentHash), candidate.TorrentAddedOn))
	if errors.Is(err, sql.ErrNoRows) {
		job, err = enqueueMediaJobTx(tx, candidate)
	} else if err == nil && job.Status == "pending" && job.Phase == "download" {
		job.Phase = "transfer"
		job.Version++
		job.UpdatedAt = nowUTC()
		job.SourcePath, job.TargetPath = candidate.SourcePath, candidate.TargetPath
		job.IntakeSourceRoot, job.IntakeTargetRoot = candidate.IntakeSourceRoot, candidate.IntakeTargetRoot
		job.Organize, job.PlannedPaths = candidate.Organize, candidate.PlannedPaths
		var data []byte
		data, err = json.Marshal(job)
		if err == nil {
			_, err = tx.Exec(`UPDATE media_jobs SET phase=?,version=?,payload=?,updated_at=? WHERE id=?`, job.Phase, job.Version, string(data), job.UpdatedAt, job.ID)
		}
		if err == nil {
			err = insertMediaEvent(tx, job, "download_complete")
		}
	}
	if err != nil {
		return MediaJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return MediaJob{}, err
	}
	return job, nil
}
