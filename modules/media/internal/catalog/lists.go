package catalog

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"
)

type Playlist struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     int64    `json:"version"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	WorkCount   int      `json:"work_count"`
	WorkIDs     []string `json:"work_ids,omitempty"`
}

const listColumns = `l.id,l.name,l.description,l.version,l.created_at,l.updated_at,(SELECT COUNT(*) FROM catalog_list_items i WHERE i.list_id=l.id)`

func scanList(row rowScanner) (Playlist, error) {
	var item Playlist
	err := row.Scan(&item.ID, &item.Name, &item.Description, &item.Version, &item.CreatedAt, &item.UpdatedAt, &item.WorkCount)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return item, err
}
func (s *Store) Lists(ctx context.Context) ([]Playlist, error) {
	items := []Playlist{}
	rows, err := s.db.QueryContext(ctx, "SELECT "+listColumns+" FROM catalog_lists l ORDER BY l.updated_at DESC,l.id LIMIT 200")
	if err != nil {
		return items, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanList(rows)
		if err != nil {
			return items, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *Store) List(ctx context.Context, id string) (Playlist, error) {
	item, err := scanList(s.db.QueryRowContext(ctx, "SELECT "+listColumns+" FROM catalog_lists l WHERE l.id=?", id))
	if err != nil {
		return item, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT work_id FROM catalog_list_items WHERE list_id=? ORDER BY position", id)
	if err != nil {
		return item, err
	}
	defer rows.Close()
	item.WorkIDs = []string{}
	for rows.Next() {
		var workID string
		if err := rows.Scan(&workID); err != nil {
			return item, err
		}
		item.WorkIDs = append(item.WorkIDs, workID)
	}
	return item, rows.Err()
}
func (s *Store) CreateList(ctx context.Context, name, description string) (Playlist, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if !validListText(name, description) {
		return Playlist{}, ErrInvalid
	}
	id, stamp := rand.Text(), timestamp()
	result, err := s.db.ExecContext(ctx, `INSERT INTO catalog_lists(id,name,description,created_at,updated_at) SELECT ?,?,?,?,? WHERE (SELECT COUNT(*) FROM catalog_lists)<200`, id, name, description, stamp, stamp)
	if err != nil {
		return Playlist{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Playlist{}, err
	}
	if count != 1 {
		return Playlist{}, ErrInvalid
	}
	return s.List(ctx, id)
}
func (s *Store) UpdateList(ctx context.Context, id, name, description string, version int64) (Playlist, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if !validListText(name, description) || version < 1 {
		return Playlist{}, ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, "UPDATE catalog_lists SET name=?,description=?,version=version+1,updated_at=? WHERE id=? AND version=?", name, description, timestamp(), id, version)
	if err != nil {
		return Playlist{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Playlist{}, err
	}
	if count == 0 {
		if _, err := s.List(ctx, id); err != nil {
			return Playlist{}, err
		}
		return Playlist{}, ErrConflict
	}
	return s.List(ctx, id)
}
func (s *Store) ReplaceListItems(ctx context.Context, id string, version int64, ids []string) (Playlist, error) {
	if version < 1 || len(ids) > 1000 {
		return Playlist{}, ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if len(id) < 1 || len(id) > 100 || seen[id] {
			return Playlist{}, ErrInvalid
		}
		seen[id] = true
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Playlist{}, err
	}
	defer tx.Rollback()
	var current int64
	if err := tx.QueryRowContext(ctx, "SELECT version FROM catalog_lists WHERE id=?", id).Scan(&current); errors.Is(err, sql.ErrNoRows) {
		return Playlist{}, ErrNotFound
	} else if err != nil {
		return Playlist{}, err
	}
	if current != version {
		return Playlist{}, ErrConflict
	}
	for _, workID := range ids {
		var found int
		if err := tx.QueryRowContext(ctx, "SELECT 1 FROM catalog_works WHERE id=? AND EXISTS(SELECT 1 FROM catalog_assets WHERE work_id=?)", workID, workID).Scan(&found); errors.Is(err, sql.ErrNoRows) {
			return Playlist{}, ErrNotFound
		} else if err != nil {
			return Playlist{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM catalog_list_items WHERE list_id=?", id); err != nil {
		return Playlist{}, err
	}
	for position, workID := range ids {
		if _, err := tx.ExecContext(ctx, "INSERT INTO catalog_list_items(list_id,work_id,position) VALUES(?,?,?)", id, workID, position); err != nil {
			return Playlist{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE catalog_lists SET version=version+1,updated_at=? WHERE id=?", timestamp(), id); err != nil {
		return Playlist{}, err
	}
	if err := tx.Commit(); err != nil {
		return Playlist{}, err
	}
	return s.List(ctx, id)
}
func (s *Store) DeleteList(ctx context.Context, id string, version int64) error {
	if version < 1 {
		return ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, "DELETE FROM catalog_lists WHERE id=? AND version=?", id, version)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		if _, err := s.List(ctx, id); err != nil {
			return err
		}
		return ErrConflict
	}
	return nil
}
