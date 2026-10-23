package catalog

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/browserdata"
)

type BrowserUpload struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Filename     string `json:"filename"`
	Data         []byte `json:"data"`
	SourceID     string `json:"source_id"`
	Version      int64  `json:"version"`
	Digest       string `json:"digest,omitempty"`
	Confirmation string `json:"confirmation,omitempty"`
}
type BrowserSource struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Reading   bool   `json:"reading"`
	Version   int64  `json:"version"`
	Records   int    `json:"records"`
	Matched   int    `json:"matched"`
	Format    string `json:"format"`
	UpdatedAt string `json:"updated_at"`
}
type BrowserPreview struct {
	Digest     string              `json:"digest"`
	Format     string              `json:"format"`
	Records    int                 `json:"records"`
	Matched    int                 `json:"matched"`
	Skipped    int                 `json:"skipped"`
	Duplicates int                 `json:"duplicates"`
	Samples    []browserdata.Entry `json:"samples"`
	snapshot   browserdata.Snapshot
	codes      []string
}
type BrowserImportResult struct {
	Source    BrowserSource `json:"source"`
	Added     int           `json:"added"`
	Duplicate bool          `json:"duplicate"`
}

func initializeBrowser(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS catalog_browser_sources(
 id TEXT PRIMARY KEY,name TEXT NOT NULL,name_key TEXT NOT NULL,kind TEXT NOT NULL CHECK(kind IN ('history','bookmarks')),
 reading INTEGER NOT NULL DEFAULT 1,version INTEGER NOT NULL DEFAULT 1,digest TEXT NOT NULL,format TEXT NOT NULL,updated_at TEXT NOT NULL,
 UNIQUE(kind,name_key));
 CREATE TABLE IF NOT EXISTS catalog_browser_entries(
 source_id TEXT NOT NULL REFERENCES catalog_browser_sources(id) ON DELETE CASCADE,event_key TEXT NOT NULL,
 url TEXT NOT NULL,title TEXT NOT NULL,visited_at TEXT NOT NULL,code TEXT NOT NULL DEFAULT '',PRIMARY KEY(source_id,event_key));
 CREATE INDEX IF NOT EXISTS catalog_browser_code ON catalog_browser_entries(code);`)
	return err
}
func validBrowserUpload(u BrowserUpload) bool {
	return u.Name != "" && strings.TrimSpace(u.Name) == u.Name && len(u.Name) <= 160 && utf8.ValidString(u.Name) && !strings.ContainsAny(u.Name, "\x00\r\n") && (u.Kind == "history" || u.Kind == "bookmarks") && u.Filename != "" && len(u.Filename) <= 200 && path.Base(u.Filename) == u.Filename && !strings.ContainsAny(u.Filename, "\\\x00") && u.Version >= 0 && len(u.SourceID) <= 100 && (u.SourceID != "" || u.Version == 0)
}

// Only an explicit unambiguous media code from a recognized media page may
// link browser evidence to a work. Unknown sites remain inspectable records,
// not guessed actors/tags or invented catalog entries.
func (s *Store) browserCode(e browserdata.Entry) string {
	u, err := url.Parse(e.URL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	known := false
	for _, domain := range []string{"javbus.com", "javdb.com", "javlibrary.com", "c97k.com", "avsox.click", "jav321.com", "dmm.co.jp", "dmm.com"} {
		if host == domain {
			known = true
		}
	}
	if !known {
		return ""
	}
	raw, _ := url.PathUnescape(u.Path)
	raw += " " + u.Query().Get("v") + " " + u.Query().Get("id")
	code, pathOK := s.resolveCode(raw)
	titleCode, titleOK := s.resolveCode(e.Title)
	if pathOK && titleOK && code != titleCode {
		return ""
	}
	if pathOK {
		return code
	}
	if titleOK {
		return titleCode
	}
	return ""
}
func (s *Store) PreviewBrowser(ctx context.Context, u BrowserUpload) (BrowserPreview, error) {
	result := BrowserPreview{Samples: []browserdata.Entry{}}
	if !validBrowserUpload(u) {
		return result, ErrInvalid
	}
	snapshot, err := browserdata.Parse(ctx, u.Filename, u.Kind, u.Data)
	if err != nil {
		return result, err
	}
	result.Digest = snapshot.Digest
	result.Format = snapshot.Format
	result.Records = len(snapshot.Entries)
	result.Skipped = snapshot.Skipped
	result.Duplicates = snapshot.Duplicates
	result.snapshot = snapshot
	known := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT code FROM catalog_works WHERE code<>''")
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			rows.Close()
			return result, err
		}
		known[code] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	for _, e := range snapshot.Entries {
		code := s.browserCode(e)
		result.codes = append(result.codes, code)
		if code != "" && known[code] {
			result.Matched++
		}
		if len(result.Samples) < 5 {
			result.Samples = append(result.Samples, e)
		}
	}
	return result, nil
}

const browserSourceColumns = `s.id,s.name,s.kind,s.reading,s.version,s.format,s.updated_at,
 (SELECT COUNT(*) FROM catalog_browser_entries e WHERE e.source_id=s.id),
 (SELECT COUNT(*) FROM catalog_browser_entries e WHERE e.source_id=s.id AND e.code<>'' AND EXISTS(SELECT 1 FROM catalog_works w WHERE w.code=e.code))`

func scanBrowserSource(row rowScanner) (BrowserSource, error) {
	var s BrowserSource
	err := row.Scan(&s.ID, &s.Name, &s.Kind, &s.Reading, &s.Version, &s.Format, &s.UpdatedAt, &s.Records, &s.Matched)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return s, err
}
func (s *Store) BrowserSources(ctx context.Context) ([]BrowserSource, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+browserSourceColumns+" FROM catalog_browser_sources s ORDER BY s.updated_at DESC,s.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []BrowserSource{}
	for rows.Next() {
		item, err := scanBrowserSource(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *Store) ImportBrowser(ctx context.Context, u BrowserUpload) (BrowserImportResult, error) {
	result := BrowserImportResult{}
	if u.Confirmation != "导入资料" {
		return result, ErrInvalid
	}
	prepared, err := s.PreviewBrowser(ctx, u)
	if err != nil {
		return result, err
	}
	if prepared.Digest != u.Digest {
		return result, ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	id := u.SourceID
	if id == "" {
		existing, err := scanBrowserSource(tx.QueryRowContext(ctx, "SELECT "+browserSourceColumns+" FROM catalog_browser_sources s WHERE kind=? AND digest=? ORDER BY s.id LIMIT 1", u.Kind, u.Digest))
		if err == nil {
			return BrowserImportResult{Source: existing, Duplicate: true}, tx.Commit()
		}
		if !errors.Is(err, ErrNotFound) {
			return result, err
		}
		var count, total int
		if err := tx.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM catalog_browser_sources),(SELECT COUNT(*) FROM catalog_browser_entries)").Scan(&count, &total); err != nil {
			return result, err
		}
		if count >= 100 || total+prepared.Records > 200000 {
			return result, browserdata.ErrLimit
		}
		var nameExists int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_browser_sources WHERE kind=? AND name_key=?", u.Kind, actorNameKey(u.Name)).Scan(&nameExists); err != nil {
			return result, err
		}
		if nameExists > 0 {
			return result, ErrConflict
		}
		id = stableID("browser-source", u.Kind, u.Name, timestamp())
		_, err = tx.ExecContext(ctx, "INSERT INTO catalog_browser_sources(id,name,name_key,kind,digest,format,updated_at) VALUES(?,?,?,?,?,?,?)", id, u.Name, actorNameKey(u.Name), u.Kind, u.Digest, prepared.Format, timestamp())
		if err != nil {
			return result, err
		}
	} else {
		current, err := scanBrowserSource(tx.QueryRowContext(ctx, "SELECT "+browserSourceColumns+" FROM catalog_browser_sources s WHERE id=?", id))
		if err != nil {
			return result, err
		}
		if current.Version != u.Version || current.Kind != u.Kind || !current.Reading {
			return result, ErrConflict
		}
		var digest string
		if err := tx.QueryRowContext(ctx, "SELECT digest FROM catalog_browser_sources WHERE id=?", id).Scan(&digest); err != nil {
			return result, err
		}
		if digest == u.Digest {
			return BrowserImportResult{Source: current, Duplicate: true}, tx.Commit()
		}
		if u.Kind == "bookmarks" {
			if _, err := tx.ExecContext(ctx, "DELETE FROM catalog_browser_entries WHERE source_id=?", id); err != nil {
				return result, err
			}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE catalog_browser_sources SET name=?,name_key=?,digest=?,format=?,version=version+1,updated_at=? WHERE id=? AND version=?", u.Name, actorNameKey(u.Name), u.Digest, prepared.Format, timestamp(), id, u.Version); err != nil {
			return result, err
		}
	}
	for i, e := range prepared.snapshot.Entries {
		key := e.URL
		if u.Kind == "history" {
			key += "\x00" + e.VisitedAt
		}
		key = stableID(key)
		changed, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_browser_entries(source_id,event_key,url,title,visited_at,code) VALUES(?,?,?,?,?,?)`, id, key, e.URL, e.Title, e.VisitedAt, prepared.codes[i])
		if err != nil {
			return result, err
		}
		n, err := changed.RowsAffected()
		if err != nil {
			return result, err
		}
		result.Added += int(n)
	}
	var total int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_browser_entries").Scan(&total); err != nil {
		return result, err
	}
	if total > 200000 {
		return result, browserdata.ErrLimit
	}
	result.Source, err = scanBrowserSource(tx.QueryRowContext(ctx, "SELECT "+browserSourceColumns+" FROM catalog_browser_sources s WHERE id=?", id))
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func (s *Store) SetBrowserReading(ctx context.Context, id string, version int64, reading bool) (BrowserSource, error) {
	if version < 1 || id == "" || len(id) > 100 {
		return BrowserSource{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BrowserSource{}, err
	}
	defer tx.Rollback()
	current, err := scanBrowserSource(tx.QueryRowContext(ctx, "SELECT "+browserSourceColumns+" FROM catalog_browser_sources s WHERE id=?", id))
	if err != nil {
		return current, err
	}
	if current.Version != version {
		return current, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, "UPDATE catalog_browser_sources SET reading=?,version=version+1,updated_at=? WHERE id=? AND version=?", reading, timestamp(), id, version); err != nil {
		return current, err
	}
	current, err = scanBrowserSource(tx.QueryRowContext(ctx, "SELECT "+browserSourceColumns+" FROM catalog_browser_sources s WHERE id=?", id))
	if err != nil {
		return current, err
	}
	return current, tx.Commit()
}
func (s *Store) DeleteBrowserSource(ctx context.Context, id string, version int64, confirmation string) error {
	if version < 1 || id == "" || len(id) > 100 || confirmation != "删除来源" {
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := scanBrowserSource(tx.QueryRowContext(ctx, "SELECT "+browserSourceColumns+" FROM catalog_browser_sources s WHERE id=?", id))
	if err != nil {
		return err
	}
	if current.Version != version {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM catalog_browser_entries WHERE source_id=?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM catalog_browser_sources WHERE id=? AND version=?", id, version); err != nil {
		return err
	}
	return tx.Commit()
}
