package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"unicode"
	"unicode/utf8"

	_ "golang.org/x/image/webp"
	"golang.org/x/text/unicode/norm"
)

type Actor struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	DisplayName      string   `json:"display_name"`
	Biography        string   `json:"biography"`
	Aliases          []string `json:"aliases"`
	Version          int64    `json:"version"`
	WorkCount        int      `json:"work_count"`
	HasPortrait      bool     `json:"has_portrait"`
	PortraitRevision int64    `json:"portrait_revision"`
}
type ActorPatch struct {
	DisplayName string   `json:"display_name"`
	Biography   string   `json:"biography"`
	Aliases     []string `json:"aliases"`
	Version     int64    `json:"version"`
}
type ActorPage struct {
	Items  []Actor `json:"items"`
	Total  int     `json:"total"`
	Offset int     `json:"offset"`
	Limit  int     `json:"limit"`
}

func actorNameKey(value string) string {
	return strings.ToLower(norm.NFC.String(strings.TrimSpace(value)))
}
func validActorName(value string) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= 160 && strings.IndexFunc(value, unicode.IsControl) < 0
}

const actorColumns = `a.id,a.name,a.display_name,a.biography,a.aliases,a.version,
 (SELECT COUNT(*) FROM catalog_work_actors wa WHERE wa.actor_id=a.id),COALESCE(length(a.portrait),0)>0,a.portrait_revision`

func scanActor(row rowScanner) (Actor, error) {
	a := Actor{}
	var aliases string
	err := row.Scan(&a.ID, &a.Name, &a.DisplayName, &a.Biography, &aliases, &a.Version, &a.WorkCount, &a.HasPortrait, &a.PortraitRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, err
	}
	if err := json.Unmarshal([]byte(aliases), &a.Aliases); err != nil {
		return a, err
	}
	return a, nil
}
func (s *Store) Actor(ctx context.Context, id string) (Actor, error) {
	return scanActor(s.db.QueryRowContext(ctx, "SELECT "+actorColumns+" FROM catalog_actors a WHERE id=?", id))
}
func (s *Store) Actors(ctx context.Context, query string, limit, offset int) (ActorPage, error) {
	page := ActorPage{Items: []Actor{}, Limit: limit, Offset: offset}
	if len(query) > 500 || limit < 1 || limit > 100 || offset < 0 {
		return page, ErrInvalid
	}
	literal := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(actorNameKey(query))
	like := "%" + literal + "%"
	where := ` WHERE (a.display_name LIKE ? ESCAPE '\' OR EXISTS(SELECT 1 FROM catalog_actor_names n WHERE n.actor_id=a.id AND n.name_key LIKE ? ESCAPE '\'))`
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_actors a"+where, like, like).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+actorColumns+" FROM catalog_actors a"+where+" ORDER BY a.display_name COLLATE NOCASE,a.id LIMIT ? OFFSET ?", like, like, limit, offset)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanActor(rows)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, a)
	}
	return page, rows.Err()
}
func (s *Store) PatchActor(ctx context.Context, id string, patch ActorPatch) (Actor, error) {
	patch.DisplayName = strings.TrimSpace(patch.DisplayName)
	patch.Biography = strings.TrimSpace(patch.Biography)
	if !validActorName(patch.DisplayName) || !utf8.ValidString(patch.Biography) || len(patch.Biography) > 20000 || len(patch.Aliases) > 32 || patch.Version < 1 {
		return Actor{}, ErrInvalid
	}
	aliases := []string{}
	keys := map[string]bool{}
	for _, alias := range patch.Aliases {
		alias = strings.TrimSpace(alias)
		if !validActorName(alias) {
			return Actor{}, ErrInvalid
		}
		key := actorNameKey(alias)
		if !keys[key] {
			aliases = append(aliases, alias)
			keys[key] = true
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Actor{}, err
	}
	defer tx.Rollback()
	var name string
	var version int64
	if err := tx.QueryRowContext(ctx, "SELECT name,version FROM catalog_actors WHERE id=?", id).Scan(&name, &version); errors.Is(err, sql.ErrNoRows) {
		return Actor{}, ErrNotFound
	} else if err != nil {
		return Actor{}, err
	}
	if version != patch.Version {
		return Actor{}, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM catalog_actor_names WHERE actor_id=?", id); err != nil {
		return Actor{}, err
	}
	for _, alias := range append([]string{name, patch.DisplayName}, aliases...) {
		key := actorNameKey(alias)
		var owner string
		err := tx.QueryRowContext(ctx, "SELECT actor_id FROM catalog_actor_names WHERE name_key=?", key).Scan(&owner)
		if err == nil && owner != id {
			return Actor{}, ErrConflict
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Actor{}, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO catalog_actor_names(name_key,actor_id) VALUES(?,?)", key, id); err != nil {
			return Actor{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE catalog_actors SET display_name=?,biography=?,aliases=?,version=version+1 WHERE id=?", patch.DisplayName, patch.Biography, jsonText(aliases), id); err != nil {
		return Actor{}, err
	}
	if err := syncActorsTx(ctx, tx); err != nil {
		return Actor{}, err
	}
	if err := tx.Commit(); err != nil {
		return Actor{}, err
	}
	return s.Actor(ctx, id)
}
func validatePortrait(data []byte) (string, error) {
	if len(data) == 0 || len(data) > 3<<20 {
		return "", ErrInvalid
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", ErrInvalid
	}
	if config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 || int64(config.Width)*int64(config.Height) > 4_000_000 {
		return "", ErrInvalid
	}
	if format != "jpeg" && format != "png" && format != "webp" {
		return "", ErrInvalid
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return "", ErrInvalid
	}
	return "image/" + format, nil
}
func (s *Store) SetActorPortrait(ctx context.Context, id string, version int64, data []byte) (Actor, error) {
	mime := ""
	var err error
	if len(data) > 0 {
		mime, err = validatePortrait(data)
		if err != nil {
			return Actor{}, err
		}
	}
	if version < 1 {
		return Actor{}, ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, "UPDATE catalog_actors SET portrait=?,portrait_mime=?,portrait_revision=portrait_revision+1,version=version+1 WHERE id=? AND version=?", data, mime, id, version)
	if err != nil {
		return Actor{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Actor{}, err
	}
	if count != 1 {
		if _, err := s.Actor(ctx, id); err != nil {
			return Actor{}, err
		}
		return Actor{}, ErrConflict
	}
	return s.Actor(ctx, id)
}
func (s *Store) ActorPortrait(ctx context.Context, id string) ([]byte, string, int64, error) {
	var data []byte
	var mime string
	var revision int64
	err := s.db.QueryRowContext(ctx, "SELECT portrait,portrait_mime,portrait_revision FROM catalog_actors WHERE id=?", id).Scan(&data, &mime, &revision)
	if errors.Is(err, sql.ErrNoRows) || err == nil && len(data) == 0 {
		return nil, "", 0, ErrNotFound
	}
	return data, mime, revision, err
}
func (s *Store) workActors(ctx context.Context, id string) ([]Actor, error) {
	result := []Actor{}
	rows, err := s.db.QueryContext(ctx, "SELECT "+actorColumns+" FROM catalog_actors a JOIN catalog_work_actors wa ON wa.actor_id=a.id WHERE wa.work_id=? ORDER BY a.display_name,a.id", id)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanActor(rows)
		if err != nil {
			return result, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}
