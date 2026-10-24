package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

func (s *Service) registerWantRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/wants", s.handleWants)
	mux.HandleFunc("POST /api/wants", s.catalogSameOrigin(s.handleWantAdd))
	mux.HandleFunc("GET /api/wants/{id}", s.handleWant)
	mux.HandleFunc("PATCH /api/wants/{id}", s.catalogSameOrigin(s.handleWantPatch))
	mux.HandleFunc("DELETE /api/wants/{id}", s.catalogSameOrigin(s.handleWantRemove))
}

func wantError(w http.ResponseWriter, err error) {
	if errors.Is(err, catalog.ErrAlreadyAcquired) {
		writeError(w, 409, errors.New("作品已有可用馆藏，无需再次加入想要；查找其他版本请使用资源搜索"))
		return
	}
	catalogError(w, err)
}

func (s *Service) handleWants(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	query, err := url.ParseQuery(r.URL.RawQuery)
	for key, values := range query {
		if (key != "query" && key != "phase" && key != "code" && key != "limit" && key != "offset") || len(values) != 1 {
			err = catalog.ErrInvalid
		}
	}
	filter := catalog.WantFilter{Query: query.Get("query"), Phase: query.Get("phase"), Code: query.Get("code"), Limit: 48}
	for key, target := range map[string]*int{"limit": &filter.Limit, "offset": &filter.Offset} {
		if value, ok := query[key]; ok {
			parsed, parseErr := strconv.Atoi(value[0])
			if parseErr != nil {
				err = catalog.ErrInvalid
			} else {
				*target = parsed
			}
		}
	}
	if err != nil {
		catalogError(w, catalog.ErrInvalid)
		return
	}
	page, err := s.db.catalog.Wants(r.Context(), filter)
	if err != nil {
		wantError(w, err)
		return
	}
	writeJSON(w, 200, page)
}

func (s *Service) handleWant(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, ok := catalogScanID(w, r)
	if !ok {
		return
	}
	item, err := s.db.catalog.Want(r.Context(), id)
	if err != nil {
		wantError(w, err)
		return
	}
	writeJSON(w, 200, item)
}

// Fields are explicit and non-null; a client cannot supply sources, URLs,
// downloader identity, acquisition evidence or an acquired state.
func decodeWant(w http.ResponseWriter, r *http.Request, target any, allowed ...string) bool {
	var raw map[string]json.RawMessage
	if err := decodeJSON(w, r, &raw); err != nil || len(raw) == 0 {
		catalogError(w, catalog.ErrInvalid)
		return false
	}
	keys := map[string]bool{}
	for _, key := range allowed {
		keys[key] = true
	}
	for key, value := range raw {
		if !keys[key] || string(value) == "null" {
			catalogError(w, catalog.ErrInvalid)
			return false
		}
	}
	encoded, err := json.Marshal(raw)
	if err != nil || json.Unmarshal(encoded, target) != nil {
		catalogError(w, catalog.ErrInvalid)
		return false
	}
	return true
}

func (s *Service) handleWantAdd(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body catalog.WantInput
	if !decodeWant(w, r, &body, "code", "title", "release_date", "studio", "actors") {
		return
	}
	item, created, err := s.db.catalog.AddWant(r.Context(), body)
	if err != nil {
		wantError(w, err)
		return
	}
	status := 200
	if created {
		status = 201
		w.Header().Set("Location", fmt.Sprintf("%s/api/wants/%d", s.prefix, item.ID))
	}
	writeJSON(w, status, map[string]any{"created": created, "want": item})
}

func (s *Service) handleWantPatch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, ok := catalogScanID(w, r)
	if !ok {
		return
	}
	var body catalog.WantPatch
	if !decodeWant(w, r, &body, "version", "title", "release_date", "studio", "actors", "state") {
		return
	}
	item, err := s.db.catalog.PatchWant(r.Context(), id, body)
	if err != nil {
		wantError(w, err)
		return
	}
	writeJSON(w, 200, item)
}

func (s *Service) handleWantRemove(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, ok := catalogScanID(w, r)
	if !ok {
		return
	}
	var body struct {
		Version int64 `json:"version"`
	}
	if !decodeWant(w, r, &body, "version") {
		return
	}
	if err := s.db.catalog.RemoveWant(r.Context(), id, body.Version); err != nil {
		wantError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"removed": true})
}
