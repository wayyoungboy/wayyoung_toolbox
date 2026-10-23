package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Store struct {
	db          *sql.DB
	resolveCode func(string) (string, bool)
	scanMu      sync.Mutex
}

func New(db *sql.DB, resolveCode func(string) (string, bool)) (*Store, error) {
	s := &Store{db: db, resolveCode: resolveCode}
	const schema = `
CREATE TABLE IF NOT EXISTS catalog_libraries (
 id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, root TEXT NOT NULL UNIQUE, root_identity TEXT NOT NULL,
 created_at TEXT NOT NULL, scanned_at TEXT NOT NULL DEFAULT '', scan_error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS catalog_works (
 id TEXT PRIMARY KEY, code TEXT NOT NULL DEFAULT '', title TEXT NOT NULL,
 original_title TEXT NOT NULL DEFAULT '', plot TEXT NOT NULL DEFAULT '', studio TEXT NOT NULL DEFAULT '',
 release_date TEXT NOT NULL DEFAULT '', year INTEGER NOT NULL DEFAULT 0,
 actors TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(actors)),
 genres TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(genres)),
 metadata_source TEXT NOT NULL DEFAULT 'filename', source_rank INTEGER NOT NULL DEFAULT 0,
 title_locked INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS catalog_assets (
 id TEXT PRIMARY KEY, library_id INTEGER NOT NULL REFERENCES catalog_libraries(id),
 work_id TEXT NOT NULL REFERENCES catalog_works(id), path TEXT NOT NULL,
 size INTEGER NOT NULL, modified_ns INTEGER NOT NULL, file_identity TEXT NOT NULL DEFAULT '', available INTEGER NOT NULL DEFAULT 1,
 nfo_path TEXT NOT NULL DEFAULT '', poster_path TEXT NOT NULL DEFAULT '',
 issues TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(issues)),
 subtitles TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(subtitles)),
 metadata TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(metadata)), scan_token TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS catalog_assets_work ON catalog_assets(work_id,available);
CREATE TABLE IF NOT EXISTS catalog_personal (
 work_id TEXT PRIMARY KEY REFERENCES catalog_works(id), favorite INTEGER NOT NULL DEFAULT 0,
 watch_later INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS catalog_progress (
 asset_id TEXT PRIMARY KEY REFERENCES catalog_assets(id), position REAL NOT NULL,
 duration REAL NOT NULL, revision INTEGER NOT NULL, completed INTEGER NOT NULL,
 played_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS catalog_progress_played ON catalog_progress(played_at DESC);
`
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("initialize catalog: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(schema); err != nil {
		return nil, fmt.Errorf("initialize catalog schema: %w", err)
	}
	// Early development catalogs did not pin directory identity. Preserve every
	// record, but require explicit relinking rather than trust whichever directory
	// happens to occupy the old path at upgrade time.
	columns, err := tx.Query("PRAGMA table_info(catalog_libraries)")
	if err != nil {
		return nil, err
	}
	hasIdentity := false
	for columns.Next() {
		var index, notNull, primary int
		var name, kind string
		var defaultValue any
		if err := columns.Scan(&index, &name, &kind, &notNull, &defaultValue, &primary); err != nil {
			columns.Close()
			return nil, err
		}
		hasIdentity = hasIdentity || name == "root_identity"
	}
	if err := columns.Err(); err != nil {
		columns.Close()
		return nil, err
	}
	if err := columns.Close(); err != nil {
		return nil, err
	}
	if !hasIdentity {
		if _, err := tx.Exec("ALTER TABLE catalog_libraries ADD COLUMN root_identity TEXT NOT NULL DEFAULT ''"); err != nil {
			return nil, err
		}
	}
	if err := migrateAssetVersions(tx); err != nil {
		return nil, fmt.Errorf("migrate catalog versions: %w", err)
	}
	if err := initializePersonal(tx); err != nil {
		return nil, fmt.Errorf("initialize personal media: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit catalog schema: %w", err)
	}
	return s, nil
}

func timestamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func stableID(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(hash[:16])
}

func jsonText(value any) string {
	encoded, _ := json.Marshal(value) // callers supply bounded, JSON-compatible structs and slices
	return string(encoded)
}

func (s *Store) Libraries(ctx context.Context) ([]Library, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT l.id,l.name,l.root,l.created_at,l.scanned_at,l.scan_error,
 (SELECT COUNT(*) FROM catalog_assets a WHERE a.library_id=l.id) FROM catalog_libraries l ORDER BY l.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Library{}
	for rows.Next() {
		var item Library
		if err := rows.Scan(&item.ID, &item.Name, &item.Root, &item.CreatedAt, &item.ScannedAt, &item.ScanError, &item.Assets); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) library(ctx context.Context, id int64) (Library, error) {
	var item Library
	err := s.db.QueryRowContext(ctx, `SELECT id,name,root,root_identity,created_at,scanned_at,scan_error FROM catalog_libraries WHERE id=?`, id).
		Scan(&item.ID, &item.Name, &item.Root, &item.RootIdentity, &item.CreatedAt, &item.ScannedAt, &item.ScanError)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound
	}
	return item, err
}

const workColumns = `w.id,w.code,w.title,w.original_title,w.plot,w.studio,w.release_date,w.year,
w.actors,w.genres,w.metadata_source,w.title_locked,w.created_at,
(SELECT COUNT(*) FROM catalog_assets a WHERE a.work_id=w.id),
(SELECT COUNT(*) FROM catalog_assets a WHERE a.work_id=w.id AND a.available=1),
COALESCE((SELECT a.id FROM catalog_assets a WHERE a.work_id=w.id AND a.available=1 AND a.poster_path!='' ORDER BY a.path LIMIT 1),''),
COALESCE(p.favorite,0),COALESCE(p.watch_later,0),
COALESCE((SELECT MAX(g.played_at) FROM catalog_progress g JOIN catalog_assets a ON a.id=g.asset_id WHERE a.work_id=w.id),'') AS last_played_at,` + watchedExpression + `,
COALESCE((SELECT rating FROM catalog_work_notes n WHERE n.work_id=w.id),0),
COALESCE((SELECT feedback FROM catalog_work_notes n WHERE n.work_id=w.id),''),
COALESCE((SELECT revision FROM catalog_work_notes n WHERE n.work_id=w.id),0)`

type rowScanner interface{ Scan(...any) error }

func scanWork(row rowScanner) (Work, error) {
	var item Work
	var actors, genres string
	err := row.Scan(&item.ID, &item.Code, &item.Title, &item.OriginalTitle, &item.Plot, &item.Studio,
		&item.ReleaseDate, &item.Year, &actors, &genres, &item.MetadataSource, &item.TitleLocked, &item.CreatedAt,
		&item.AssetCount, &item.AvailableCount, &item.CoverAssetID, &item.Favorite, &item.WatchLater, &item.LastPlayedAt, &item.Watched, &item.Rating, &item.Feedback, &item.NotesRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	if err := json.Unmarshal([]byte(actors), &item.Actors); err != nil {
		return item, err
	}
	if err := json.Unmarshal([]byte(genres), &item.Genres); err != nil {
		return item, err
	}
	return item, nil
}

func (s *Store) Works(ctx context.Context, filter Filter) (Page, error) {
	result := Page{Items: []Work{}, Limit: filter.Limit, Offset: filter.Offset}
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 || filter.LibraryID < 0 || len(filter.Query) > 500 || len(filter.Actor) > 500 || len(filter.Tag) > 500 {
		return result, ErrInvalid
	}
	conditions := []string{"EXISTS (SELECT 1 FROM catalog_assets a WHERE a.work_id=w.id)"}
	if len(filter.ActorID) > 100 || len(filter.ListID) > 100 {
		return result, ErrInvalid
	}
	if filter.Watched != nil {
		conditions = append(conditions, watchedExpression+"=?")
	}
	if filter.Sort != "" && filter.Sort != "recent" && filter.Sort != "title" && filter.Sort != "year" {
		return result, ErrInvalid
	}
	args := []any{}
	if filter.Watched != nil {
		args = append(args, *filter.Watched)
	}
	if filter.ActorID != "" {
		conditions = append(conditions, `EXISTS(SELECT 1 FROM catalog_work_actors wa WHERE wa.work_id=w.id AND wa.actor_id=?)`)
		args = append(args, filter.ActorID)
	}
	if filter.ListID != "" {
		conditions = append(conditions, `EXISTS(SELECT 1 FROM catalog_list_items li WHERE li.work_id=w.id AND li.list_id=?)`)
		args = append(args, filter.ListID)
	}
	if filter.Query != "" {
		literal := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(filter.Query))
		like := "%" + literal + "%"
		conditions = append(conditions, `(w.title LIKE ? ESCAPE '\' OR w.code LIKE ? ESCAPE '\' OR w.actors LIKE ? ESCAPE '\'
OR w.genres LIKE ? ESCAPE '\' OR w.original_title LIKE ? ESCAPE '\' OR w.plot LIKE ? ESCAPE '\' OR w.studio LIKE ? ESCAPE '\'
OR EXISTS(SELECT 1 FROM catalog_work_actors wa JOIN catalog_actor_names n ON n.actor_id=wa.actor_id WHERE wa.work_id=w.id AND n.name_key LIKE ? ESCAPE '\'))`)
		args = append(args, like, like, like, like, like, like, like, "%"+strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(actorNameKey(filter.Query))+"%")
	}
	if actor := strings.TrimSpace(filter.Actor); actor != "" {
		conditions = append(conditions, `EXISTS (SELECT 1 FROM catalog_work_actors wa JOIN catalog_actor_names n ON n.actor_id=wa.actor_id WHERE wa.work_id=w.id AND n.name_key=?)`)
		args = append(args, actorNameKey(actor))
	}
	if tag := strings.TrimSpace(filter.Tag); tag != "" {
		conditions = append(conditions, `EXISTS (SELECT 1 FROM json_each(w.genres) WHERE value=? COLLATE NOCASE)`)
		args = append(args, tag)
	}
	if filter.LibraryID != 0 {
		conditions = append(conditions, `EXISTS (SELECT 1 FROM catalog_assets a WHERE a.work_id=w.id AND a.library_id=?)`)
		args = append(args, filter.LibraryID)
	}
	if filter.Favorite {
		conditions = append(conditions, "p.favorite=1")
	}
	if filter.WatchLater {
		conditions = append(conditions, "p.watch_later=1")
	}
	if filter.Continue {
		conditions = append(conditions, `EXISTS (SELECT 1 FROM catalog_assets a JOIN catalog_progress g ON g.asset_id=a.id
WHERE a.work_id=w.id AND a.available=1 AND g.position>0 AND g.completed=0)`)
	}
	from := " FROM catalog_works w LEFT JOIN catalog_personal p ON p.work_id=w.id WHERE " + strings.Join(conditions, " AND ")
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*)"+from, args...).Scan(&result.Total); err != nil {
		return result, err
	}
	order := "w.created_at DESC,w.id"
	if !filter.Continue && filter.ListID != "" && (filter.Sort == "" || filter.Sort == "recent") {
		order = `(SELECT position FROM catalog_list_items li WHERE li.work_id=w.id AND li.list_id=?)`
		args = append(args, filter.ListID)
	}
	if filter.Continue {
		order = "last_played_at DESC,w.id"
	} else if filter.Sort == "title" {
		order = "w.title COLLATE NOCASE,w.id"
	} else if filter.Sort == "year" {
		order = "w.year DESC,w.title,w.id"
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+workColumns+from+" ORDER BY "+order+" LIMIT ? OFFSET ?", append(args, filter.Limit, filter.Offset)...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanWork(rows)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}

func (s *Store) Work(ctx context.Context, id string) (Work, error) {
	item, err := scanWork(s.db.QueryRowContext(ctx, "SELECT "+workColumns+" FROM catalog_works w LEFT JOIN catalog_personal p ON p.work_id=w.id WHERE w.id=?", id))
	if err != nil {
		return item, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.id,a.library_id,a.work_id,a.path,a.size,a.available,a.nfo_path,a.poster_path!='',
a.issues,a.subtitles,COALESCE(g.position,0),COALESCE(g.duration,0),COALESCE(g.revision,0),COALESCE(g.played_at,''),COALESCE(g.completed,0)
FROM catalog_assets a LEFT JOIN catalog_progress g ON g.asset_id=a.id WHERE a.work_id=? ORDER BY a.path,a.id`, id)
	if err != nil {
		return item, err
	}
	defer rows.Close()
	item.Assets = []Asset{}
	for rows.Next() {
		var asset Asset
		var issues, subtitles string
		if err := rows.Scan(&asset.ID, &asset.LibraryID, &asset.WorkID, &asset.Path, &asset.Size, &asset.Available,
			&asset.NFOPath, &asset.Poster, &issues, &subtitles, &asset.Progress.Position, &asset.Progress.Duration,
			&asset.Progress.Revision, &asset.Progress.PlayedAt, &asset.Progress.Completed); err != nil {
			return item, err
		}
		if err := json.Unmarshal([]byte(issues), &asset.Issues); err != nil {
			return item, err
		}
		if err := json.Unmarshal([]byte(subtitles), &asset.Subtitles); err != nil {
			return item, err
		}
		item.Assets = append(item.Assets, asset)
	}
	if err := rows.Err(); err != nil {
		return item, err
	}
	if err := rows.Close(); err != nil {
		return item, err
	}
	item.ActorProfiles, err = s.workActors(ctx, id)
	return item, err
}

func ValidateProgress(value Progress) error {
	if value.SessionID != "" && !validSessionID(value.SessionID) {
		return ErrInvalid
	}
	if math.IsNaN(value.Position) || math.IsNaN(value.Duration) || math.IsInf(value.Position, 0) || math.IsInf(value.Duration, 0) ||
		value.Position < 0 || value.Duration <= 0 || value.Duration > 7*24*3600 || value.Position > value.Duration || value.Revision < 0 {
		return ErrInvalid
	}
	return nil
}

func (s *Store) SaveProgress(ctx context.Context, assetID string, value Progress) (Progress, error) {
	if err := ValidateProgress(value); err != nil {
		return value, err
	}
	value.PlayedAt = timestamp()
	value.Completed = value.Position >= value.Duration*0.95
	if value.SessionID == "" {
		value.SessionID = stableID("legacy", assetID, value.PlayedAt[:10])
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return value, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO catalog_progress(asset_id,position,duration,revision,completed,played_at)
SELECT ?,?,?,1,?,? WHERE ?=0 AND EXISTS(SELECT 1 FROM catalog_assets WHERE id=?)
ON CONFLICT(asset_id) DO UPDATE SET position=excluded.position,duration=excluded.duration,revision=catalog_progress.revision+1,
completed=excluded.completed,played_at=excluded.played_at WHERE catalog_progress.revision=?`,
		assetID, value.Position, value.Duration, value.Completed, value.PlayedAt, value.Revision, assetID, value.Revision)
	if err != nil {
		return value, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return value, err
	}
	if changed == 0 {
		// Existing rows need a CAS UPDATE: the insert SELECT must not create a
		// brand-new row from an arbitrary client-supplied nonzero revision.
		result, err = tx.ExecContext(ctx, `UPDATE catalog_progress SET position=?,duration=?,revision=revision+1,completed=?,played_at=? WHERE asset_id=? AND revision=?`,
			value.Position, value.Duration, value.Completed, value.PlayedAt, assetID, value.Revision)
		if err != nil {
			return value, err
		}
		changed, err = result.RowsAffected()
		if err != nil {
			return value, err
		}
	}
	if changed == 0 {
		var present int
		if err := tx.QueryRowContext(ctx, "SELECT 1 FROM catalog_assets WHERE id=?", assetID).Scan(&present); errors.Is(err, sql.ErrNoRows) {
			return value, ErrNotFound
		} else if err != nil {
			return value, err
		}
		return value, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_history(id,asset_id,session_id,position,duration,completed,started_at,updated_at)
 VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(asset_id,session_id) DO UPDATE SET position=excluded.position,duration=excluded.duration,
 completed=MAX(catalog_history.completed,excluded.completed),updated_at=excluded.updated_at`, stableID("history", assetID, value.SessionID), assetID, value.SessionID, value.Position, value.Duration, value.Completed, value.PlayedAt, value.PlayedAt); err != nil {
		return value, err
	}
	if value.Completed {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_work_notes(work_id,watched,revision)
 SELECT work_id,1,1 FROM catalog_assets WHERE id=?
 ON CONFLICT(work_id) DO UPDATE SET watched=1,revision=revision+CASE WHEN COALESCE(catalog_work_notes.watched,0)=1 THEN 0 ELSE 1 END`, assetID); err != nil {
			return value, err
		}
	}
	if err := tx.Commit(); err != nil {
		return value, err
	}
	value.Revision++
	return value, nil
}

func (s *Store) PatchPersonal(ctx context.Context, id string, patch PersonalPatch) (Work, error) {
	if patch.Favorite == nil && patch.WatchLater == nil && patch.Watched == nil && patch.Rating == nil && patch.Feedback == nil {
		return Work{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Work{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_personal(work_id) SELECT id FROM catalog_works WHERE id=?`, id)
	if err != nil {
		return Work{}, err
	}
	var present int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM catalog_personal WHERE work_id=?", id).Scan(&present); errors.Is(err, sql.ErrNoRows) {
		return Work{}, ErrNotFound
	} else if err != nil {
		return Work{}, err
	}
	if patch.Favorite != nil {
		if _, err := tx.ExecContext(ctx, "UPDATE catalog_personal SET favorite=? WHERE work_id=?", *patch.Favorite, id); err != nil {
			return Work{}, err
		}
	}
	if patch.WatchLater != nil {
		if _, err := tx.ExecContext(ctx, "UPDATE catalog_personal SET watch_later=? WHERE work_id=?", *patch.WatchLater, id); err != nil {
			return Work{}, err
		}
	}
	if err := updateWorkNotes(ctx, tx, id, patch); err != nil {
		return Work{}, err
	}
	if err := tx.Commit(); err != nil {
		return Work{}, err
	}
	return s.Work(ctx, id)
}

func (s *Store) SetTitle(ctx context.Context, id, title string) (Work, error) {
	title = strings.TrimSpace(title)
	if title == "" || !utf8.ValidString(title) || utf8.RuneCountInString(title) > 1000 {
		return Work{}, ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, "UPDATE catalog_works SET title=?,title_locked=1,updated_at=? WHERE id=?", title, timestamp(), id)
	if err != nil {
		return Work{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Work{}, err
	}
	if count == 0 {
		return Work{}, ErrNotFound
	}
	return s.Work(ctx, id)
}
