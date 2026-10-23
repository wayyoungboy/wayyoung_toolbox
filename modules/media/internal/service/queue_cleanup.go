package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func (s *Service) handleQueueCleanupCandidates(w http.ResponseWriter, r *http.Request) {
	days := 30
	if raw := r.URL.Query().Get("older_days"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 3650 {
			writeError(w, 400, fmt.Errorf("过期时间应为 0–3650 天"))
			return
		}
		days = value
	}
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339Nano)
	where := ` WHERE status IN ('failed','partial','timed_out','needs_attention') AND COALESCE(json_extract(payload,'$.archived'),0)=0 AND COALESCE(json_extract(payload,'$.hold_files'),0)=0 AND updated_at<?`
	page := MediaQueuePage{Jobs: []MediaQueueJob{}}
	if err := s.db.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM media_jobs"+where, cutoff).Scan(&page.Total); err != nil {
		writeQueueError(w, err)
		return
	}
	rows, err := s.db.db.QueryContext(r.Context(), "SELECT "+mediaJobColumns+" FROM media_jobs"+where+" ORDER BY updated_at,id LIMIT 200", cutoff)
	if err != nil {
		writeQueueError(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		job, err := scanMediaJob(rows)
		if err != nil {
			writeQueueError(w, err)
			return
		}
		page.Jobs = append(page.Jobs, MediaQueueJob{MediaJob: job})
	}
	if err := rows.Err(); err != nil {
		writeQueueError(w, err)
		return
	}
	writeJSON(w, 200, page)
}

type queueCleanupItem struct {
	ID      int64 `json:"id"`
	Version int64 `json:"version"`
}

func (s *Service) handleQueueCleanup(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Jobs []queueCleanupItem `json:"jobs"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	if len(payload.Jobs) == 0 || len(payload.Jobs) > 200 {
		writeError(w, 400, fmt.Errorf("请选择 1–200 个已过期的异常任务"))
		return
	}
	seen := map[int64]bool{}
	for _, item := range payload.Jobs {
		if item.ID < 1 || item.Version < 1 || seen[item.ID] {
			writeError(w, 400, fmt.Errorf("任务标识或版本无效"))
			return
		}
		seen[item.ID] = true
	}
	tx, err := s.db.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeQueueError(w, err)
		return
	}
	defer tx.Rollback()
	for _, item := range payload.Jobs {
		job, err := scanMediaJob(tx.QueryRowContext(r.Context(), "SELECT "+mediaJobColumns+" FROM media_jobs WHERE id=?", item.ID))
		if err != nil {
			writeQueueError(w, err)
			return
		}
		if job.Version != item.Version || job.Archived || job.HoldFiles || !cleanupMediaStatus(job.Status) {
			writeQueueError(w, errMediaJobConflict)
			return
		}
		job.Archived = true
		job.Version++
		job.UpdatedAt = nowUTC()
		data, err := json.Marshal(job)
		if err != nil {
			writeQueueError(w, err)
			return
		}
		result, err := tx.ExecContext(r.Context(), "UPDATE media_jobs SET payload=?,version=?,updated_at=? WHERE id=? AND version=?", string(data), job.Version, job.UpdatedAt, job.ID, item.Version)
		if err != nil {
			writeQueueError(w, err)
			return
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			writeQueueError(w, errMediaJobConflict)
			return
		}
		if err := insertMediaEvent(tx, job, "archived"); err != nil {
			writeQueueError(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeQueueError(w, err)
		return
	}
	writeJSON(w, 200, map[string]int{"archived": len(payload.Jobs)})
}

func cleanupMediaStatus(status string) bool {
	return status == "failed" || status == "partial" || status == "timed_out" || status == "needs_attention"
}
