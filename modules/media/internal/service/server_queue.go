package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// WaitingForIntake is a live view, never a persisted job decision. The worker
// rechecks the same predicate transactionally when claiming a movie.
type MediaQueueJob struct {
	MediaJob
	WaitingForIntake []int64 `json:"waiting_for_intake,omitempty"`
}

type MediaQueuePage struct {
	Jobs       []MediaQueueJob `json:"jobs"`
	NextCursor int64           `json:"next_cursor"`
	Total      int             `json:"total"`
}

func (d *Database) MediaQueuePage(status string, before int64, limit int) (MediaQueuePage, error) {
	page := MediaQueuePage{Jobs: []MediaQueueJob{}}
	if limit < 1 || limit > 200 {
		return page, fmt.Errorf("limit 应为 1–200")
	}
	where := " WHERE COALESCE(json_extract(payload,'$.archived'),0)=0"
	args := []any{}
	if status == "archived" {
		where = " WHERE json_extract(payload,'$.archived')=1"
	} else if status != "" {
		where += " AND status=?"
		args = append(args, status)
	}
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM media_jobs`+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	if before > 0 {
		where += " AND id<?"
		args = append(args, before)
	}
	args = append(args, limit+1)
	rows, err := d.db.Query(`SELECT `+mediaJobColumns+` FROM media_jobs`+where+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		job, err := scanMediaJob(rows)
		if err != nil {
			return page, err
		}
		page.Jobs = append(page.Jobs, MediaQueueJob{MediaJob: job})
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Jobs) > limit {
		page.Jobs = page.Jobs[:limit]
		page.NextCursor = page.Jobs[limit-1].ID
	}
	return page, nil
}

// Enrich a bounded API page after its read cursor closes, not once per movie
// while SQLite's sole connection is still occupied by the page query.
func (d *Database) annotateQueueWaits(ctx context.Context, jobs []MediaQueueJob) error {
	positions := make(map[int64]int, len(jobs))
	args := make([]any, 0, len(jobs))
	for i := range jobs {
		jobs[i].WaitingForIntake = nil
		if jobs[i].Kind == "movie" && jobs[i].Phase == "organize" && jobs[i].Status == "pending" {
			positions[jobs[i].ID] = i
			args = append(args, jobs[i].ID)
		}
	}
	if len(args) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
	rows, err := d.db.QueryContext(ctx, `SELECT media_jobs.id,intake.id
 FROM media_jobs JOIN media_jobs AS intake ON (`+unpublishedIntakeMatchSQL+`)
 WHERE media_jobs.id IN (`+placeholders+`) AND media_jobs.kind='movie' AND media_jobs.phase='organize' AND media_jobs.status='pending'
 ORDER BY media_jobs.id,intake.id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var movieID, intakeID int64
		if err := rows.Scan(&movieID, &intakeID); err != nil {
			return err
		}
		i := positions[movieID]
		jobs[i].WaitingForIntake = append(jobs[i].WaitingForIntake, intakeID)
	}
	return rows.Err()
}

func (s *Service) handleQueue(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && status != "archived" && status != "pending" && status != "running" && status != "success" && status != "skipped" && !retryableMediaStatus(status) {
		writeError(w, 400, fmt.Errorf("无效的队列状态"))
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 200 {
			writeError(w, 400, fmt.Errorf("limit 应为 1–200"))
			return
		}
		limit = value
	}
	var before int64
	if raw := r.URL.Query().Get("before_id"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			writeError(w, 400, fmt.Errorf("无效的分页游标"))
			return
		}
		before = value
	}
	page, err := s.db.MediaQueuePage(status, before, limit)
	if err == nil {
		err = s.db.annotateQueueWaits(r.Context(), page.Jobs)
	}
	if err != nil {
		writeQueueError(w, err)
		return
	}
	writeJSON(w, 200, page)
}

func queueRequestID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, fmt.Errorf("无效的任务 ID"))
		return 0, false
	}
	return id, true
}

func writeQueueError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, 404, fmt.Errorf("任务不存在"))
	case errors.Is(err, errMediaJobConflict):
		writeError(w, 409, err)
	default:
		slog.Error("media queue request", "error", err)
		writeError(w, 500, fmt.Errorf("队列操作失败，请查看应用日志"))
	}
}

func (s *Service) handleQueueDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := queueRequestID(w, r)
	if !ok {
		return
	}
	job, err := s.db.MediaJob(id)
	if err != nil {
		writeQueueError(w, err)
		return
	}
	events, err := s.db.MediaJobEvents(id)
	if err != nil {
		writeQueueError(w, err)
		return
	}
	view := []MediaQueueJob{{MediaJob: job}}
	if err := s.db.annotateQueueWaits(r.Context(), view); err != nil {
		writeQueueError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"job": view[0], "events": events})
}

func (s *Service) handleQueueAction(w http.ResponseWriter, r *http.Request) {
	id, ok := queueRequestID(w, r)
	if !ok {
		return
	}
	var body struct {
		Version int64 `json:"version"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, 400, err)
		return
	}
	if body.Version < 1 {
		writeError(w, 400, fmt.Errorf("必须提供任务当前 version"))
		return
	}
	var job MediaJob
	var err error
	switch r.PathValue("action") {
	case "retry":
		job, err = s.db.RetryMediaJob(id, body.Version)
	case "cancel":
		job, err = s.db.CancelMediaJob(id, body.Version)
		if err == nil {
			s.cancelMediaJobAttempt(id, body.Version)
		}
	default:
		writeError(w, 404, fmt.Errorf("未知的队列操作"))
		return
	}
	if err != nil {
		writeQueueError(w, err)
		return
	}
	s.wakeMediaQueue()
	writeJSON(w, 200, job)
}
