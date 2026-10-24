package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// ScanJob separates lifecycle revisions from durable traversal progress. A
// progress checkpoint must not invalidate an administrator's pause/cancel CAS.
type ScanJob struct {
	ID              int64       `json:"id"`
	LibraryID       int64       `json:"library_id"`
	Root            string      `json:"root"`
	RootIdentity    string      `json:"-"`
	Status          string      `json:"status"`
	Phase           string      `json:"phase"`
	Version         int64       `json:"version"`
	Entries         int         `json:"entries"`
	ProcessedFiles  int         `json:"processed_files"`
	DiscoveredFiles int         `json:"discovered_files"`
	StagedBytes     int64       `json:"-"`
	CurrentPath     string      `json:"current_path"`
	Error           string      `json:"error"`
	Result          *ScanResult `json:"result,omitempty"`
	CreatedAt       string      `json:"created_at"`
	UpdatedAt       string      `json:"updated_at"`
}

const scanJobColumns = `id,library_id,root,root_identity,status,phase,version,entries,processed_files,discovered_files,staged_bytes,current_path,error,result,created_at,updated_at`

func initializeScanJobs(tx *sql.Tx) error {
	const schema = `
CREATE TABLE IF NOT EXISTS catalog_scan_jobs (
 id INTEGER PRIMARY KEY AUTOINCREMENT, library_id INTEGER NOT NULL REFERENCES catalog_libraries(id),
 root TEXT NOT NULL,root_identity TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('pending','running','paused','canceled','failed','success')),
 phase TEXT NOT NULL DEFAULT 'discover',version INTEGER NOT NULL DEFAULT 1,
 entries INTEGER NOT NULL DEFAULT 0,processed_files INTEGER NOT NULL DEFAULT 0,discovered_files INTEGER NOT NULL DEFAULT 0,
 staged_bytes INTEGER NOT NULL DEFAULT 0,current_path TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',result TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS catalog_scan_active_library ON catalog_scan_jobs(library_id) WHERE status IN ('pending','running','paused');
CREATE TABLE IF NOT EXISTS catalog_scan_dirs (
 job_id INTEGER NOT NULL REFERENCES catalog_scan_jobs(id) ON DELETE CASCADE,path TEXT NOT NULL,
 identity TEXT NOT NULL DEFAULT '',modified_ns INTEGER NOT NULL DEFAULT 0,listed INTEGER NOT NULL DEFAULT 0,
 sidecars TEXT NOT NULL DEFAULT '',manifest TEXT NOT NULL DEFAULT '',PRIMARY KEY(job_id,path)
);
CREATE TABLE IF NOT EXISTS catalog_scan_files (
 job_id INTEGER NOT NULL REFERENCES catalog_scan_jobs(id) ON DELETE CASCADE,path TEXT NOT NULL,parent TEXT NOT NULL,
 processed INTEGER NOT NULL DEFAULT 0,data TEXT NOT NULL DEFAULT '',PRIMARY KEY(job_id,path)
);
CREATE INDEX IF NOT EXISTS catalog_scan_pending_files ON catalog_scan_files(job_id,processed,parent,path);
UPDATE catalog_scan_jobs SET status='pending',version=version+1,error='服务重启，从已保存的扫描进度接续',updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE status='running';
`
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	// An early development checkpoint had only directory mtimes. Such a
	// checkpoint is not safe to publish; an empty manifest forces explicit retry.
	rows, err := tx.Query("PRAGMA table_info(catalog_scan_dirs)")
	if err != nil {
		return err
	}
	hasManifest := false
	for rows.Next() {
		var index, notNull, primary int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&index, &name, &kind, &notNull, &defaultValue, &primary); err != nil {
			rows.Close()
			return err
		}
		hasManifest = hasManifest || name == "manifest"
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !hasManifest {
		_, err = tx.Exec("ALTER TABLE catalog_scan_dirs ADD COLUMN manifest TEXT NOT NULL DEFAULT ''")
	}
	return err
}

type scanRow interface{ Scan(...any) error }

func readScanJob(row scanRow) (ScanJob, error) {
	var job ScanJob
	var raw string
	err := row.Scan(&job.ID, &job.LibraryID, &job.Root, &job.RootIdentity, &job.Status, &job.Phase, &job.Version, &job.Entries, &job.ProcessedFiles, &job.DiscoveredFiles, &job.StagedBytes, &job.CurrentPath, &job.Error, &raw, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return job, ErrNotFound
	}
	if err != nil {
		return job, err
	}
	if raw != "" {
		var result ScanResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return job, fmt.Errorf("decode scan result: %w", err)
		}
		job.Result = &result
	}
	return job, nil
}

func (s *Store) ScanJob(ctx context.Context, id int64) (ScanJob, error) {
	if id < 1 {
		return ScanJob{}, ErrInvalid
	}
	return readScanJob(s.db.QueryRowContext(ctx, "SELECT "+scanJobColumns+" FROM catalog_scan_jobs WHERE id=?", id))
}

func (s *Store) ScanJobs(ctx context.Context, before int64, limit int) ([]ScanJob, error) {
	if before < 0 || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+scanJobColumns+" FROM catalog_scan_jobs WHERE (?=0 OR id<?) ORDER BY id DESC LIMIT ?", before, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []ScanJob{}
	for rows.Next() {
		job, err := readScanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) StartScan(ctx context.Context, libraryID int64) (ScanJob, error) {
	if libraryID < 1 {
		return ScanJob{}, ErrInvalid
	}
	if !s.scanMu.TryLock() {
		return ScanJob{}, ErrBusy
	}
	defer s.scanMu.Unlock()
	library, err := s.library(ctx, libraryID)
	if err != nil {
		return ScanJob{}, err
	}
	root, err := openLibraryRoot(library.Root, library.RootIdentity)
	if err != nil {
		return ScanJob{}, err
	}
	if err := root.Close(); err != nil {
		return ScanJob{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScanJob{}, err
	}
	defer tx.Rollback()
	var active, all int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_scan_jobs WHERE library_id=? AND status IN ('pending','running','paused')", libraryID).Scan(&active); err != nil {
		return ScanJob{}, err
	}
	if active != 0 {
		return ScanJob{}, ErrConflict
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_scan_jobs WHERE status IN ('pending','running','paused')").Scan(&all); err != nil {
		return ScanJob{}, err
	}
	if all >= 128 {
		return ScanJob{}, ErrBusy
	}
	stamp := timestamp()
	result, err := tx.ExecContext(ctx, "INSERT INTO catalog_scan_jobs(library_id,root,root_identity,status,created_at,updated_at) VALUES(?,?,?,'pending',?,?)", library.ID, library.Root, library.RootIdentity, stamp, stamp)
	if err != nil {
		return ScanJob{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return ScanJob{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO catalog_scan_dirs(job_id,path) VALUES(?,'.')", id); err != nil {
		return ScanJob{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_scan_jobs WHERE status IN ('success','failed','canceled') AND id NOT IN (SELECT id FROM catalog_scan_jobs WHERE status IN ('success','failed','canceled') ORDER BY id DESC LIMIT 100)`); err != nil {
		return ScanJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return ScanJob{}, err
	}
	return s.ScanJob(ctx, id)
}

func (s *Store) ClaimScan(ctx context.Context, id, version int64) (ScanJob, error) {
	if id < 1 || version < 1 {
		return ScanJob{}, ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, "UPDATE catalog_scan_jobs SET status='running',version=version+1,error='',updated_at=? WHERE id=? AND version=? AND status='pending'", timestamp(), id, version)
	if err != nil {
		return ScanJob{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return ScanJob{}, err
	}
	if count != 1 {
		return ScanJob{}, ErrConflict
	}
	return s.ScanJob(ctx, id)
}

func (s *Store) NextPendingScan(ctx context.Context) (ScanJob, error) {
	return readScanJob(s.db.QueryRowContext(ctx, "SELECT "+scanJobColumns+" FROM catalog_scan_jobs WHERE status='pending' ORDER BY id LIMIT 1"))
}

// ReleaseScanAttempt preserves checkpoints on shutdown/interrupted I/O. A stale
// attempt must never overwrite an administrator's newer pause/cancel revision.
func (s *Store) ReleaseScanAttempt(ctx context.Context, id, version int64) error {
	if id < 1 || version < 1 {
		return ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, "UPDATE catalog_scan_jobs SET status='pending',version=version+1,updated_at=? WHERE id=? AND version=? AND status='running'", timestamp(), id, version)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) ChangeScanJob(ctx context.Context, id, version int64, action string) (ScanJob, error) {
	if id < 1 || version < 1 {
		return ScanJob{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScanJob{}, err
	}
	defer tx.Rollback()
	job, err := readScanJob(tx.QueryRowContext(ctx, "SELECT "+scanJobColumns+" FROM catalog_scan_jobs WHERE id=?", id))
	if err != nil {
		return job, err
	}
	if job.Version != version {
		return job, ErrConflict
	}
	valid := false
	switch action {
	case "pause":
		valid = job.Status == "pending" || job.Status == "running"
		if valid {
			job.Status = "paused"
		}
	case "resume":
		valid = job.Status == "paused"
		if valid {
			job.Status = "pending"
		}
	case "cancel":
		valid = job.Status == "pending" || job.Status == "running" || job.Status == "paused"
		if valid {
			job.Status = "canceled"
		}
	case "retry":
		valid = job.Status == "failed" || job.Status == "canceled"
		if valid {
			job.Status, job.Phase = "pending", "discover"
			job.Entries, job.ProcessedFiles, job.DiscoveredFiles, job.StagedBytes = 0, 0, 0, 0
			job.CurrentPath = ""
			job.Result = nil
		}
	default:
		return job, ErrInvalid
	}
	if !valid {
		return job, ErrConflict
	}
	if action == "retry" {
		var active int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_scan_jobs WHERE library_id=? AND status IN ('pending','running','paused')", job.LibraryID).Scan(&active); err != nil {
			return job, err
		}
		if active != 0 {
			return job, ErrConflict
		}
	}
	if action == "retry" || action == "cancel" {
		if err := clearScanStage(ctx, tx, id); err != nil {
			return job, err
		}
	}
	if action == "retry" {
		if _, err := tx.ExecContext(ctx, "INSERT INTO catalog_scan_dirs(job_id,path) VALUES(?,'.')", id); err != nil {
			return job, err
		}
	}
	job.Version++
	job.Error = ""
	job.UpdatedAt = timestamp()
	if err := saveScanJobTx(ctx, tx, job, version); err != nil {
		return job, err
	}
	if err := tx.Commit(); err != nil {
		return job, err
	}
	return job, nil
}

func saveScanJobTx(ctx context.Context, tx *sql.Tx, job ScanJob, version int64) error {
	raw := ""
	if job.Result != nil {
		raw = jsonText(job.Result)
	}
	result, err := tx.ExecContext(ctx, `UPDATE catalog_scan_jobs SET status=?,phase=?,version=?,entries=?,processed_files=?,discovered_files=?,staged_bytes=?,current_path=?,error=?,result=?,updated_at=? WHERE id=? AND version=?`, job.Status, job.Phase, job.Version, job.Entries, job.ProcessedFiles, job.DiscoveredFiles, job.StagedBytes, job.CurrentPath, job.Error, raw, job.UpdatedAt, job.ID, version)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func clearScanStage(ctx context.Context, tx *sql.Tx, id int64) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM catalog_scan_files WHERE job_id=?", id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM catalog_scan_dirs WHERE job_id=?", id)
	return err
}
