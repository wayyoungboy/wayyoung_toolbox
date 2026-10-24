package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
	javspengine "github.com/yuukiy/javsp-go/engine"
)

const resourceCacheLimit = 200

type resourceSearchView struct {
	javspengine.ResourceSearchResult
	SearchedAt string `json:"searched_at"`
	Cached     bool   `json:"cached"`
	Stale      bool   `json:"stale"`
}

func (d *Database) initializeResourceSearch() error {
	_, err := d.db.Exec(`CREATE TABLE IF NOT EXISTS resource_search_cache (
 code TEXT NOT NULL, provider TEXT NOT NULL, result TEXT NOT NULL CHECK(json_valid(result)),
 searched_at TEXT NOT NULL, PRIMARY KEY(code,provider)
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
	var raw string
	err := d.db.QueryRowContext(ctx, "SELECT result,searched_at FROM resource_search_cache WHERE code=? AND provider=?", code, provider).Scan(&raw, &view.SearchedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return view, nil
	}
	if err != nil {
		return view, err
	}
	if len(raw) > 128<<10 {
		return view, errors.New("invalid resource cache size")
	}
	if err := json.Unmarshal([]byte(raw), &view.ResourceSearchResult); err != nil {
		return view, err
	}
	view.Cached = true
	return view, nil
}

func (d *Database) saveResources(ctx context.Context, result javspengine.ResourceSearchResult) (resourceSearchView, error) {
	view := resourceSearchView{ResourceSearchResult: result, SearchedAt: time.Now().UTC().Format(time.RFC3339Nano)}
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO resource_search_cache(code,provider,result,searched_at) VALUES(?,?,?,?)
 ON CONFLICT(code,provider) DO UPDATE SET result=excluded.result,searched_at=excluded.searched_at`, result.Code, result.Provider, string(raw), view.SearchedAt); err != nil {
		return view, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM resource_search_cache WHERE rowid NOT IN
 (SELECT rowid FROM resource_search_cache ORDER BY searched_at DESC,code,provider LIMIT ?)`, resourceCacheLimit); err != nil {
		return view, err
	}
	return view, tx.Commit()
}

// GET only reads the last successful result. Reloading/navigating never calls
// a provider, records a search failure, creates tasks, or submits a download.
func (s *Service) handleResourceSearchCache(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	query, err := r.URL.Query(), error(nil)
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
		catalogError(w, catalog.ErrBusy)
		return
	}
	defer s.resourceSearchMu.Unlock()
	cfg, err := s.db.JavspConfigPrivate()
	if err != nil {
		catalogError(w, err)
		return
	}
	// Use a config snapshot, not the currently running organizer; do not copy
	// runtime assets, write config.yml, acquire media-file locks or start a scan.
	raw, err := s.javsp.prepareConfigDataContext(r.Context(), cfg, false)
	if err != nil {
		writeError(w, 503, fmt.Errorf("资源搜索配置暂不可读，请检查 JAVSP 组件"))
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
			writeError(w, 503, fmt.Errorf("资源搜索配置无效，请检查已配置的站点与代理"))
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
	previous, err := s.db.cachedResources(r.Context(), code, payload.Provider)
	if err != nil {
		catalogError(w, err)
		return
	}
	view := resourceSearchView{ResourceSearchResult: result}
	if previous.Cached {
		view.Items = previous.Items
		view.SearchedAt = previous.SearchedAt
		view.Cached = true
		view.Stale = true
	}
	writeJSON(w, 200, view)
}
