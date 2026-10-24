package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
	javspengine "github.com/yuukiy/javsp-go/engine"
)

const resourceCacheLimit = 200

type resourceSearchView struct {
	javspengine.ResourceSearchResult
	SearchedAt  string `json:"searched_at"`
	AttemptedAt string `json:"attempted_at"`
	Cached      bool   `json:"cached"`
	Stale       bool   `json:"stale"`
}

func (d *Database) initializeResourceSearch() error {
	_, err := d.db.Exec(`CREATE TABLE IF NOT EXISTS resource_search_cache (
 code TEXT NOT NULL, provider TEXT NOT NULL, result TEXT CHECK(result IS NULL OR json_valid(result)),
 searched_at TEXT NOT NULL DEFAULT '', last_state TEXT NOT NULL, attempted_at TEXT NOT NULL,
 PRIMARY KEY(code,provider)
);`)
	return err
}

func resourceSearchInput(code, provider string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if provider != "javdb" || !javspengine.ValidResourceCode(code) {
		return "", catalog.ErrInvalid
	}
	return code, nil
}

func (d *Database) cachedResources(ctx context.Context, code, provider string) (resourceSearchView, error) {
	view := resourceSearchView{ResourceSearchResult: javspengine.ResourceSearchResult{Code: code, Provider: provider, State: "not_searched", Items: []javspengine.ResourceCandidate{}}}
	var raw sql.NullString
	var state string
	err := d.db.QueryRowContext(ctx, "SELECT result,searched_at,last_state,attempted_at FROM resource_search_cache WHERE code=? AND provider=?", code, provider).Scan(&raw, &view.SearchedAt, &state, &view.AttemptedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return view, nil
	}
	if err != nil {
		return view, err
	}
	if len(raw.String) > 128<<10 {
		return view, errors.New("invalid resource cache size")
	}
	if raw.Valid {
		if err := json.Unmarshal([]byte(raw.String), &view.ResourceSearchResult); err != nil {
			return view, err
		}
		view.Cached = true
	}
	view.State = state
	view.Stale = view.Cached && state != "ready" && state != "empty"
	return view, nil
}

func (d *Database) saveResources(ctx context.Context, result javspengine.ResourceSearchResult) (resourceSearchView, error) {
	view := resourceSearchView{ResourceSearchResult: result, SearchedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	view.AttemptedAt = view.SearchedAt
	if result.State != "ready" && result.State != "empty" {
		return view, catalog.ErrInvalid
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return view, err
	}
	if len(raw) > 128<<10 {
		return view, catalog.ErrInvalid
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return view, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO resource_search_cache(code,provider,result,searched_at,last_state,attempted_at) VALUES(?,?,?,?,?,?)
 ON CONFLICT(code,provider) DO UPDATE SET result=excluded.result,searched_at=excluded.searched_at,last_state=excluded.last_state,attempted_at=excluded.attempted_at`, result.Code, result.Provider, string(raw), view.SearchedAt, result.State, view.AttemptedAt); err != nil {
		return view, err
	}
	if err := trimResourceCache(ctx, tx); err != nil {
		return view, err
	}
	return view, tx.Commit()
}

func trimResourceCache(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM resource_search_cache WHERE rowid NOT IN
 (SELECT rowid FROM resource_search_cache ORDER BY attempted_at DESC,code,provider LIMIT ?)`, resourceCacheLimit)
	return err
}

func (d *Database) recordResourceFailure(ctx context.Context, result javspengine.ResourceSearchResult) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO resource_search_cache(code,provider,last_state,attempted_at) VALUES(?,?,?,?)
 ON CONFLICT(code,provider) DO UPDATE SET last_state=excluded.last_state,attempted_at=excluded.attempted_at`, result.Code, result.Provider, result.State, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	if err := trimResourceCache(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// GET only reads the last successful result. Reloading/navigating never calls
// a provider, records a search failure, creates tasks, or submits a download.
func (s *Service) handleResourceSearchCache(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	query, err := url.ParseQuery(r.URL.RawQuery)
	for key, values := range query {
		if (key != "code" && key != "provider") || len(values) != 1 {
			err = catalog.ErrInvalid
		}
	}
	code, inputErr := resourceSearchInput(query.Get("code"), query.Get("provider"))
	if err != nil || inputErr != nil {
		catalogError(w, catalog.ErrInvalid)
		return
	}
	result, err := s.db.cachedResources(r.Context(), code, query.Get("provider"))
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (s *Service) handleResourceSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var payload struct {
		Code     string `json:"code"`
		Provider string `json:"provider"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		catalogError(w, catalog.ErrInvalid)
		return
	}
	code, err := resourceSearchInput(payload.Code, payload.Provider)
	if err != nil {
		catalogError(w, err)
		return
	}
	if !s.resourceSearchMu.TryLock() {
		writeError(w, 409, fmt.Errorf("另一个资源查询正在运行，请稍后再试"))
		return
	}
	defer s.resourceSearchMu.Unlock()
	configFailure := func(message string) {
		if r.Context().Err() == nil {
			if err := s.db.recordResourceFailure(r.Context(), javspengine.ResourceSearchResult{Code: code, Provider: payload.Provider, State: "config_unavailable"}); err != nil {
				catalogError(w, err)
				return
			}
		}
		writeError(w, 503, errors.New(message))
	}
	cfg, err := s.db.JavspConfigPrivate()
	if err != nil {
		catalogError(w, err)
		return
	}
	// Use a config snapshot, not the currently running organizer; do not copy
	// runtime assets, write config.yml, acquire media-file locks or start a scan.
	raw, err := s.javsp.prepareConfigDataContext(r.Context(), cfg, false)
	if err != nil {
		configFailure("资源搜索配置暂不可读，请检查 JAVSP 组件")
		return
	}
	defer clear(raw)
	result, err := javspengine.SearchResources(r.Context(), javspengine.ResourceSearchOptions{Options: javspengine.Options{ConfigData: raw, ResourceRoot: s.javsp.workRoot}, Code: code, Provider: payload.Provider})
	if err != nil {
		if r.Context().Err() != nil {
			writeError(w, 408, fmt.Errorf("资源查询已取消"))
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			result.State = "timed_out"
		} else {
			configFailure("资源搜索配置无效，请检查已配置的站点与代理")
			return
		}
	}
	if result.State == "ready" || result.State == "empty" {
		view, err := s.db.saveResources(r.Context(), result)
		if err != nil {
			catalogError(w, err)
			return
		}
		writeJSON(w, 200, view)
		return
	}
	if err := s.db.recordResourceFailure(r.Context(), result); err != nil {
		catalogError(w, err)
		return
	}
	previous, err := s.db.cachedResources(r.Context(), code, payload.Provider)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, previous)
}
