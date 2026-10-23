package browserdata

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"
)

type dbShape struct {
	kind, query  string
	tables       map[string][]string
	unit, offset float64
	firefoxTime  bool
}

var shapes = []dbShape{
	{"history", `SELECT u.url,u.title,v.visit_time FROM visits v JOIN urls u ON u.id=v.url LIMIT 50001`, map[string][]string{"urls": {"id", "url", "title"}, "visits": {"url", "visit_time"}}, 1e6, -11644473600, false},
	{"history", `SELECT p.url,p.title,v.visit_date FROM moz_historyvisits v JOIN moz_places p ON p.id=v.place_id LIMIT 50001`, map[string][]string{"moz_places": {"id", "url", "title"}, "moz_historyvisits": {"place_id", "visit_date"}}, 1e6, 0, true},
	{"history", `SELECT i.url,v.title,v.visit_time FROM history_visits v JOIN history_items i ON i.id=v.history_item LIMIT 50001`, map[string][]string{"history_items": {"id", "url"}, "history_visits": {"history_item", "visit_time", "title"}}, 1, 978307200, false},
	{"history", `SELECT h.url,h.title,v.date FROM visits v JOIN history h ON h.guid=v.history_guid LIMIT 50001`, map[string][]string{"history": {"guid", "url", "title"}, "visits": {"history_guid", "date"}}, 1e6, 0, false},
	{"history", `SELECT url,title,atime FROM History LIMIT 50001`, map[string][]string{"History": {"url", "title", "atime"}}, 1, 0, false},
	{"bookmarks", `SELECT p.url,b.title,b.dateAdded FROM moz_bookmarks b JOIN moz_places p ON p.id=b.fk WHERE b.type=1 LIMIT 50001`, map[string][]string{"moz_places": {"id", "url"}, "moz_bookmarks": {"fk", "type", "title", "dateAdded"}}, 1e6, 0, false},
}

func (p *parser) sqlite(data []byte) error {
	// Upload bytes only: the original profile, WAL, application database and any
	// supplied path are never opened or ATTACHed. A deleted private copy is used.
	dir, err := os.MkdirTemp("", "media-browser-upload-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "snapshot.db")
	if err := os.WriteFile(file, data, 0600); err != nil {
		return err
	}
	u := url.URL{Scheme: "file", Path: file}
	q := url.Values{"mode": {"ro"}, "immutable": {"1"}, "_pragma": {"trusted_schema(0)", "query_only(1)", "cache_size(-2048)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return invalid("SQLite")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return invalid("SQLite")
	}
	defer conn.Close()
	// SQLITE_LIMIT_LENGTH,SQL_LENGTH,COLUMN,EXPR_DEPTH,COMPOUND_SELECT,
	// VDBE_OP,ATTACHED,VARIABLE_NUMBER. Queries below are fixed and bounded.
	for _, limit := range [][2]int{{0, 1 << 20}, {1, 64 << 10}, {2, 128}, {3, 50}, {4, 16}, {5, 100000}, {7, 0}, {9, 100}} {
		if _, err := sqlite.Limit(conn, limit[0], limit[1]); err != nil {
			return invalid("SQLite limits")
		}
	}
	for _, shape := range shapes {
		if shape.kind != p.kind {
			continue
		}
		matches := true
		for table, required := range shape.tables {
			var ddl string
			if err := conn.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE name=? COLLATE NOCASE AND type='table'", table).Scan(&ddl); err != nil || strings.Contains(strings.ToUpper(ddl), "VIRTUAL TABLE") {
				matches = false
				break
			}
			// Only constant table names from shapes above are interpolated.
			rows, err := conn.QueryContext(ctx, `PRAGMA table_info("`+table+`")`)
			if err != nil {
				return invalid("SQLite schema")
			}
			columns := map[string]bool{}
			for rows.Next() {
				var n, notNull, pk int
				var name, typ string
				var def any
				if err := rows.Scan(&n, &name, &typ, &notNull, &def, &pk); err != nil {
					rows.Close()
					return invalid("SQLite schema")
				}
				columns[name] = true
			}
			rowErr := rows.Err()
			rows.Close()
			if rowErr != nil {
				return invalid("SQLite schema")
			}
			for _, name := range required {
				if !columns[name] {
					matches = false
				}
			}
		}
		if !matches {
			continue
		}
		rows, err := conn.QueryContext(ctx, shape.query)
		if err != nil {
			return invalid("SQLite data")
		}
		for rows.Next() {
			var raw, title sql.NullString
			var when sql.NullFloat64
			if err := rows.Scan(&raw, &title, &when); err != nil {
				rows.Close()
				return invalid("SQLite record")
			}
			unit := shape.unit
			if shape.firefoxTime && when.Float64 <= 300000000*1e6 {
				unit = 1e3
			}
			at := stamp(when.Float64, unit, shape.offset)
			if !when.Valid || at == "" {
				rows.Close()
				return invalid("SQLite timestamp")
			}
			if err := p.add(raw.String, title.String, at); err != nil {
				rows.Close()
				return err
			}
		}
		err = rows.Err()
		rows.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return invalid("SQLite record")
		}
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return invalid("SQLite browser schema")
}
