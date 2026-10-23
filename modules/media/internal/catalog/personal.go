package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"
)

type HistoryEntry struct {
	ID           string  `json:"id"`
	AssetID      string  `json:"asset_id"`
	WorkID       string  `json:"work_id"`
	Title        string  `json:"title"`
	Code         string  `json:"code"`
	Path         string  `json:"path"`
	Available    bool    `json:"available"`
	CoverAssetID string  `json:"cover_asset_id"`
	Position     float64 `json:"position"`
	Duration     float64 `json:"duration"`
	Completed    bool    `json:"completed"`
	StartedAt    string  `json:"started_at"`
	UpdatedAt    string  `json:"updated_at"`
}
type HistoryPage struct {
	Items  []HistoryEntry `json:"items"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

func (s *Store) History(ctx context.Context, query string, limit, offset int) (HistoryPage, error) {
	result := HistoryPage{Items: []HistoryEntry{}, Limit: limit, Offset: offset}
	if len(query) > 500 || limit < 1 || limit > 100 || offset < 0 {
		return result, ErrInvalid
	}
	literal := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(query))
	like := "%" + literal + "%"
	from := ` FROM catalog_history h JOIN catalog_assets a ON a.id=h.asset_id JOIN catalog_works w ON w.id=a.work_id WHERE (w.title LIKE ? ESCAPE '\' OR w.code LIKE ? ESCAPE '\')`
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*)"+from, like, like).Scan(&result.Total); err != nil {
		return result, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT h.id,a.id,a.work_id,w.title,w.code,a.path,a.available,
 COALESCE((SELECT v.id FROM catalog_assets v WHERE v.work_id=w.id AND v.available=1 AND v.poster_path!='' ORDER BY v.path LIMIT 1),''),
 h.position,h.duration,h.completed,h.started_at,h.updated_at`+from+" ORDER BY h.updated_at DESC,h.id LIMIT ? OFFSET ?", like, like, limit, offset)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var item HistoryEntry
		if err := rows.Scan(&item.ID, &item.AssetID, &item.WorkID, &item.Title, &item.Code, &item.Path, &item.Available, &item.CoverAssetID, &item.Position, &item.Duration, &item.Completed, &item.StartedAt, &item.UpdatedAt); err != nil {
			return result, err
		}
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}
func (s *Store) ClearHistory(ctx context.Context, ids []string) (int, error) {
	if len(ids) < 1 || len(ids) > 100 {
		return 0, ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if len(id) < 1 || len(id) > 100 || seen[id] {
			return 0, ErrInvalid
		}
		seen[id] = true
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	count := 0
	for _, id := range ids {
		result, err := tx.ExecContext(ctx, "DELETE FROM catalog_history WHERE id=?", id)
		if err != nil {
			return 0, err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		count += int(changed)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

const watchedExpression = `COALESCE((SELECT watched FROM catalog_work_notes n WHERE n.work_id=w.id),
 EXISTS(SELECT 1 FROM catalog_progress g JOIN catalog_assets a ON a.id=g.asset_id WHERE a.work_id=w.id AND g.completed=1))`

type PersonalStats struct {
	Works           int `json:"works"`
	WatchedWorks    int `json:"watched_works"`
	Favorites       int `json:"favorites"`
	WatchLater      int `json:"watch_later"`
	HistorySessions int `json:"history_sessions"`
	RatedWorks      int `json:"rated_works"`
	Lists           int `json:"lists"`
}

func (s *Store) PersonalStats(ctx context.Context) (PersonalStats, error) {
	var stats PersonalStats
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(`+watchedExpression+`),0),
 (SELECT COUNT(*) FROM catalog_personal WHERE favorite=1),(SELECT COUNT(*) FROM catalog_personal WHERE watch_later=1),
 (SELECT COUNT(*) FROM catalog_history),(SELECT COUNT(*) FROM catalog_work_notes WHERE rating>0),(SELECT COUNT(*) FROM catalog_lists)
 FROM catalog_works w WHERE EXISTS(SELECT 1 FROM catalog_assets a WHERE a.work_id=w.id)`).Scan(&stats.Works, &stats.WatchedWorks, &stats.Favorites, &stats.WatchLater, &stats.HistorySessions, &stats.RatedWorks, &stats.Lists)
	return stats, err
}

func updateWorkNotes(ctx context.Context, tx *sql.Tx, id string, patch PersonalPatch) error {
	if patch.Watched == nil && patch.Rating == nil && patch.Feedback == nil {
		return nil
	}
	if patch.NotesRevision < 0 || patch.Rating != nil && (*patch.Rating < 0 || *patch.Rating > 10) || patch.Feedback != nil && *patch.Feedback != "" && *patch.Feedback != "like" && *patch.Feedback != "dislike" {
		return ErrInvalid
	}
	var revision int64
	err := tx.QueryRowContext(ctx, "SELECT revision FROM catalog_work_notes WHERE work_id=?", id).Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if revision != patch.NotesRevision {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO catalog_work_notes(work_id,revision) VALUES(?,0)", id); err != nil {
		return err
	}
	for _, field := range []struct {
		name    string
		value   any
		present bool
	}{{"watched", patch.Watched, patch.Watched != nil}, {"rating", patch.Rating, patch.Rating != nil}, {"feedback", patch.Feedback, patch.Feedback != nil}} {
		if field.present {
			if _, err := tx.ExecContext(ctx, "UPDATE catalog_work_notes SET "+field.name+"=? WHERE work_id=?", field.value, id); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, "UPDATE catalog_work_notes SET revision=revision+1 WHERE work_id=?", id)
	return err
}

func validSessionID(value string) bool {
	if len(value) < 16 || len(value) > 80 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validListText(name, description string) bool {
	return strings.TrimSpace(name) != "" && utf8.ValidString(name) && utf8.RuneCountInString(name) <= 160 && utf8.ValidString(description) && len(description) <= 10000
}
