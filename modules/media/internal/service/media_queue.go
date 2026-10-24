package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

var errMediaJobConflict = errors.New("任务已变化，请刷新后重试")

// MediaJob keeps business provenance independently of rotated run logs. Key is
// the producer's stable artifact identity, not merely a film/catalog number.
type MediaJob struct {
	CatalogRegistration *catalog.RegistrationResult `json:"catalog_registration,omitempty"`
	Archived            bool                        `json:"archived,omitempty"`
	Duplicate           *MediaDuplicate             `json:"duplicate,omitempty"`
	Download            *DownloadObservation        `json:"download,omitempty"`
	IntakeMoviesReady   bool                        `json:"intake_movies_ready,omitempty"`
	ChildJobIDs         []int64                     `json:"child_job_ids,omitempty"`
	DiscoveryRunID      int64                       `json:"discovery_run_id,omitempty"`
	SourceIdentities    []queuedMovieFile           `json:"source_identities,omitempty"`
	SourcePaths         []string                    `json:"source_paths,omitempty"`
	MovieSourceRoot     string                      `json:"movie_source_root,omitempty"`
	Discovery           *RSSDiscovery               `json:"discovery,omitempty"`
	Resource            *ResourceAcquisition        `json:"resource,omitempty"`
	RelatedJobID        int64                       `json:"related_job_id,omitempty"`
	LastJavspRunID      int64                       `json:"last_javsp_run_id,omitempty"`
	MoviePlan           *queuedMoviePlan            `json:"movie_plan,omitempty"`
	PlannedPaths        []string                    `json:"planned_paths,omitempty"`
	IntakeSourceRoot    string                      `json:"intake_source_root,omitempty"`
	IntakeTargetRoot    string                      `json:"intake_target_root,omitempty"`
	Organize            bool                        `json:"organize"`
	HoldFiles           bool                        `json:"hold_files"`
	TransferResult      string                      `json:"transfer_result,omitempty"`
	ID                  int64                       `json:"id"`
	Key                 string                      `json:"key"`
	Kind                string                      `json:"kind"`
	ParentID            int64                       `json:"parent_id,omitempty"`
	MediaID             string                      `json:"media_id"`
	Title               string                      `json:"title"`
	RSSID               int64                       `json:"rss_id,omitempty"`
	RSSItemKey          string                      `json:"rss_item_key,omitempty"`
	SourceURL           string                      `json:"source_url,omitempty"`
	TorrentHash         string                      `json:"torrent_hash,omitempty"`
	TorrentAddedOn      int64                       `json:"torrent_added_on,omitempty"`
	SourcePath          string                      `json:"source_path"`
	TargetPath          string                      `json:"target_path"`
	OutputPaths         []string                    `json:"output_paths"`
	Phase               string                      `json:"phase"`
	Status              string                      `json:"status"`
	Version             int64                       `json:"version"`
	Attempts            int                         `json:"attempts"`
	Error               string                      `json:"error"`
	CreatedAt           string                      `json:"created_at"`
	UpdatedAt           string                      `json:"updated_at"`
}

type MediaJobTransition struct {
	HoldFiles      bool     `json:"hold_files"`
	TransferResult string   `json:"transfer_result,omitempty"`
	Phase          string   `json:"phase"`
	Status         string   `json:"status"`
	Error          string   `json:"error"`
	OutputPaths    []string `json:"output_paths,omitempty"`
}

type MediaJobEvent struct {
	ID        int64  `json:"id"`
	JobID     int64  `json:"job_id"`
	Phase     string `json:"phase"`
	Status    string `json:"status"`
	Reason    string `json:"reason"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}

func (d *Database) initializeMediaQueue() error {
	const schema = `
CREATE TABLE IF NOT EXISTS media_jobs (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 job_key TEXT NOT NULL UNIQUE,
 kind TEXT NOT NULL CHECK(kind IN ('torrent','movie')),
 parent_id INTEGER REFERENCES media_jobs(id),
 media_id TEXT NOT NULL DEFAULT '',
 phase TEXT NOT NULL,
 status TEXT NOT NULL,
 version INTEGER NOT NULL DEFAULT 1,
 attempts INTEGER NOT NULL DEFAULT 0,
 payload TEXT NOT NULL,
 error TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_media_jobs_pending ON media_jobs(status,phase,id);
CREATE INDEX IF NOT EXISTS idx_media_jobs_media_id ON media_jobs(media_id,status);
CREATE TABLE IF NOT EXISTS media_job_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 job_id INTEGER NOT NULL REFERENCES media_jobs(id),
 phase TEXT NOT NULL, status TEXT NOT NULL, reason TEXT NOT NULL,
 message TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_media_job_events_job ON media_job_events(job_id,id);`
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	now := nowUTC()
	if _, err := tx.Exec(`INSERT INTO media_job_events(job_id,phase,status,reason,message,created_at)
 SELECT id,phase,'pending','restart_recovery','应用重启，从未完成阶段恢复',? FROM media_jobs WHERE status='running'`, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE media_jobs SET status='pending',version=version+1,error='',updated_at=? WHERE status='running'`, now); err != nil {
		return err
	}
	return tx.Commit()
}

func mediaPhaseIndex(kind, phase string) int {
	var phases []string
	switch kind {
	case "torrent":
		phases = []string{"discovered", "download", "transfer", "qb_cleanup", "done"}
	case "movie":
		phases = []string{"organize", "catalog", "done"}
	default:
		return -1
	}
	for i, p := range phases {
		if p == phase {
			return i
		}
	}
	return -1
}

func validateMediaJob(job MediaJob) error {
	if strings.TrimSpace(job.Key) == "" || len(job.Key) > 1024 || mediaPhaseIndex(job.Kind, job.Phase) < 0 || job.Phase == "done" || job.ParentID < 0 {
		return fmt.Errorf("无效的影片任务标识或阶段")
	}
	paths := append([]string{job.SourcePath, job.TargetPath}, job.SourcePaths...)
	for _, path := range paths {
		if path != "" && (!filepath.IsAbs(path) || hasParentSegment(path) || strings.ContainsRune(path, 0)) {
			return fmt.Errorf("影片路径必须为绝对路径")
		}
	}
	return nil
}

func (d *Database) EnqueueMediaJob(job MediaJob) (MediaJob, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return MediaJob{}, err
	}
	defer tx.Rollback()
	stored, err := enqueueMediaJobTx(tx, job)
	if err != nil {
		return MediaJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return MediaJob{}, err
	}
	return stored, nil
}

func enqueueMediaJobTx(tx *sql.Tx, job MediaJob) (MediaJob, error) {
	if err := validateMediaJob(job); err != nil {
		return MediaJob{}, err
	}
	job.ID = 0
	job.Status = "pending"
	job.Version = 1
	job.Attempts = 0
	job.Error = ""
	job.CreatedAt = nowUTC()
	job.UpdatedAt = job.CreatedAt
	job.OutputPaths = []string{}
	data, err := json.Marshal(job)
	if err != nil {
		return MediaJob{}, err
	}
	var parent any
	if job.ParentID != 0 {
		parent = job.ParentID
	}
	res, err := tx.Exec(`INSERT INTO media_jobs(job_key,kind,parent_id,media_id,phase,status,payload,created_at,updated_at)
 VALUES(?,?,?,?,?,'pending',?,?,?) ON CONFLICT(job_key) DO NOTHING`, job.Key, job.Kind, parent, job.MediaID, job.Phase, string(data), job.CreatedAt, job.UpdatedAt)
	if err != nil {
		return MediaJob{}, err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return MediaJob{}, err
	}
	stored, err := scanMediaJob(tx.QueryRow(`SELECT `+mediaJobColumns+` FROM media_jobs WHERE job_key=?`, job.Key))
	if err != nil {
		return MediaJob{}, err
	}
	if count == 1 {
		if err := insertMediaEvent(tx, stored, "enqueued"); err != nil {
			return MediaJob{}, err
		}
	}
	return stored, nil
}

const mediaJobColumns = `id,job_key,kind,COALESCE(parent_id,0),media_id,phase,status,version,attempts,payload,error,created_at,updated_at`

type mediaRowScanner interface{ Scan(...any) error }

func scanMediaJob(row mediaRowScanner) (MediaJob, error) {
	var job, fields MediaJob
	var payload string
	if err := row.Scan(&fields.ID, &fields.Key, &fields.Kind, &fields.ParentID, &fields.MediaID, &fields.Phase, &fields.Status, &fields.Version, &fields.Attempts, &payload, &fields.Error, &fields.CreatedAt, &fields.UpdatedAt); err != nil {
		return job, err
	}
	if err := json.Unmarshal([]byte(payload), &job); err != nil {
		return job, fmt.Errorf("读取影片任务数据：%w", err)
	}
	job.ID, job.Key, job.Kind, job.ParentID, job.MediaID = fields.ID, fields.Key, fields.Kind, fields.ParentID, fields.MediaID
	job.Phase, job.Status, job.Version, job.Attempts = fields.Phase, fields.Status, fields.Version, fields.Attempts
	job.Error, job.CreatedAt, job.UpdatedAt = fields.Error, fields.CreatedAt, fields.UpdatedAt
	if job.OutputPaths == nil {
		job.OutputPaths = []string{}
	}
	return job, nil
}

func (d *Database) MediaJob(id int64) (MediaJob, error) {
	return scanMediaJob(d.db.QueryRow(`SELECT `+mediaJobColumns+` FROM media_jobs WHERE id=?`, id))
}

func (d *Database) MediaFilesBlocked() (bool, error) {
	var count int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM media_jobs WHERE json_extract(payload,'$.hold_files')=1`).Scan(&count)
	return count > 0, err
}

func (d *Database) PendingMediaJobs(kind string, limit int) ([]MediaJob, error) {
	return d.pendingMediaStages(kind, nil, limit)
}

// Filter stages before the limit so long-lived downloads cannot starve local
// transfers or new acquisitions. Stage values are always bound parameters.
func (d *Database) pendingMediaStages(kind string, phases []string, limit int) ([]MediaJob, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	query := `SELECT ` + mediaJobColumns + ` FROM media_jobs WHERE status='pending' AND kind=?`
	args := []any{kind}
	if kind == "movie" {
		query += ` AND (phase!='organize' OR NOT ` + movieHasUnpublishedIntakeSQL + `)`
	}
	if len(phases) > 0 {
		query += ` AND phase IN (` + strings.TrimSuffix(strings.Repeat("?,", len(phases)), ",") + `)`
		for _, phase := range phases {
			args = append(args, phase)
		}
	}
	query += ` ORDER BY COALESCE(json_extract(payload,'$.hold_files'),0) DESC,id LIMIT ?`
	args = append(args, limit)
	rows, err := d.db.Query(query, args...)
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

func (d *Database) ListMediaJobs(status string, limit int) ([]MediaJob, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	query := `SELECT ` + mediaJobColumns + ` FROM media_jobs`
	args := []any{}
	if status != "" {
		query += ` WHERE status=?`
		args = append(args, status)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := d.db.Query(query, args...)
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

func (d *Database) MediaJobEvents(id int64) ([]MediaJobEvent, error) {
	rows, err := d.db.Query(`SELECT id,job_id,phase,status,reason,message,created_at FROM media_job_events WHERE job_id=? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []MediaJobEvent{}
	for rows.Next() {
		var event MediaJobEvent
		if err := rows.Scan(&event.ID, &event.JobID, &event.Phase, &event.Status, &event.Reason, &event.Message, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func insertMediaEvent(tx *sql.Tx, job MediaJob, reason string) error {
	_, err := tx.Exec(`INSERT INTO media_job_events(job_id,phase,status,reason,message,created_at) VALUES(?,?,?,?,?,?)`, job.ID, job.Phase, job.Status, reason, job.Error, job.UpdatedAt)
	return err
}

// Every claim/action changes the revision within the same transaction as its
// event. A canceled/retried job cannot be overwritten by an old worker.
func (d *Database) changeMediaJob(id, version int64, reason string, change func(*MediaJob) error) (MediaJob, error) {
	return d.changeMediaJobTx(id, version, reason, func(_ *sql.Tx, job *MediaJob) error { return change(job) })
}

func (d *Database) changeMediaJobTx(id, version int64, reason string, change func(*sql.Tx, *MediaJob) error) (MediaJob, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return MediaJob{}, err
	}
	defer tx.Rollback()
	job, err := scanMediaJob(tx.QueryRow(`SELECT `+mediaJobColumns+` FROM media_jobs WHERE id=?`, id))
	if err != nil {
		return MediaJob{}, err
	}
	if job.Version != version || job.Archived {
		return MediaJob{}, errMediaJobConflict
	}
	if err := change(tx, &job); err != nil {
		return MediaJob{}, err
	}
	job, err = saveMediaJobChange(tx, job, version, reason)
	if err != nil {
		return MediaJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return MediaJob{}, err
	}
	return job, nil
}

// saveMediaJobChange is shared with atomic catalog publication; the caller owns
// both the transaction and the prior version/status checks.
func saveMediaJobChange(tx *sql.Tx, job MediaJob, version int64, reason string) (MediaJob, error) {
	job.Version++
	job.UpdatedAt = nowUTC()
	data, err := json.Marshal(job)
	if err != nil {
		return MediaJob{}, err
	}
	res, err := tx.Exec(`UPDATE media_jobs SET phase=?,status=?,version=?,attempts=?,payload=?,error=?,updated_at=? WHERE id=? AND version=?`, job.Phase, job.Status, job.Version, job.Attempts, string(data), job.Error, job.UpdatedAt, job.ID, version)
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
	return job, nil
}

func (d *Database) CompleteIntakeJob(id, version int64) (MediaJob, error) {
	return d.changeMediaJob(id, version, "intake_complete", func(job *MediaJob) error {
		if job.Status != "running" || job.Kind != "torrent" || job.Phase != "qb_cleanup" {
			return errMediaJobConflict
		}
		if job.Organize && !job.IntakeMoviesReady {
			return fmt.Errorf("整理任务尚未持久化，不能完成入库")
		}
		job.Phase = "done"
		job.Status = "success"
		job.HoldFiles = false
		job.Error = ""
		return nil
	})
}

func (d *Database) ClaimMediaJob(id, version int64) (MediaJob, error) {
	return d.changeMediaJob(id, version, "claimed", func(job *MediaJob) error {
		if job.Status != "pending" {
			return errMediaJobConflict
		}
		job.Status = "running"
		if job.Phase == "transfer" {
			job.HoldFiles = true
		}
		job.Attempts++
		return nil
	})
}

func retryableMediaStatus(status string) bool {
	switch status {
	case "failed", "partial", "canceled", "timed_out", "needs_attention":
		return true
	}
	return false
}

func (d *Database) RetryMediaJob(id, version int64) (MediaJob, error) {
	return d.changeMediaJob(id, version, "retry", func(job *MediaJob) error {
		if !retryableMediaStatus(job.Status) || job.Phase == "done" {
			return errMediaJobConflict
		}
		job.Status = "pending"
		job.Error = ""
		return nil
	})
}

func (d *Database) CancelMediaJob(id, version int64) (MediaJob, error) {
	return d.changeMediaJob(id, version, "cancel", func(job *MediaJob) error {
		if job.Phase == "done" || job.Status == "success" || job.Status == "canceled" || job.Status == "skipped" {
			return errMediaJobConflict
		}
		job.Status = "canceled"
		return nil
	})
}

func (d *Database) TransitionMediaJob(id, version int64, next MediaJobTransition) (MediaJob, error) {
	return d.changeMediaJobTx(id, version, "stage_result", func(tx *sql.Tx, job *MediaJob) error {
		if job.Status != "running" {
			return errMediaJobConflict
		}
		legacyCompletion := false
		if job.Kind == "movie" && job.Phase == "organize" && next.Phase == "done" && next.Status == "success" {
			var automatic bool
			if err := tx.QueryRow("SELECT after_organize FROM catalog_automation WHERE id=1").Scan(&automatic); err != nil {
				return err
			}
			if automatic {
				copy := *job
				copy.OutputPaths = next.OutputPaths
				if _, err := catalogTargetsForMovie(copy); err != nil {
					return err
				}
				next.Phase, next.Status = "catalog", "pending"
			} else {
				legacyCompletion = true
			}
		}
		oldIndex, nextIndex := mediaPhaseIndex(job.Kind, job.Phase), mediaPhaseIndex(job.Kind, next.Phase)
		if nextIndex < oldIndex || (nextIndex > oldIndex+1 && !legacyCompletion) || nextIndex < 0 {
			return fmt.Errorf("不能跳过或回退处理阶段")
		}
		if next.Phase == "done" {
			if next.Status != "success" {
				return fmt.Errorf("完成阶段必须成功")
			}
		} else if next.Status != "pending" && !retryableMediaStatus(next.Status) {
			return fmt.Errorf("无效的阶段结果状态")
		}
		if nextIndex != oldIndex && next.Status != "pending" && next.Status != "success" {
			return fmt.Errorf("失败必须保留当前阶段")
		}
		for _, path := range next.OutputPaths {
			if !filepath.IsAbs(path) || hasParentSegment(path) || strings.ContainsRune(path, 0) {
				return fmt.Errorf("整理结果必须为绝对路径")
			}
		}
		job.Phase, job.Status, job.Error = next.Phase, next.Status, next.Error
		job.HoldFiles = next.HoldFiles
		if next.TransferResult != "" {
			job.TransferResult = next.TransferResult
		}
		if next.OutputPaths != nil {
			job.OutputPaths = next.OutputPaths
		}
		return nil
	})
}
