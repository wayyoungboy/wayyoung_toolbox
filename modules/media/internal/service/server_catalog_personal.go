package service

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

func (s *Service) registerCatalogPersonalRoutes(mux *http.ServeMux) {
	s.registerCatalogTasteRoutes(mux)
	s.registerBrowserRoutes(mux)
	mux.HandleFunc("GET /api/catalog/actors", s.handleCatalogActors)
	mux.HandleFunc("GET /api/catalog/actors/{id}", s.handleCatalogActor)
	mux.HandleFunc("PATCH /api/catalog/actors/{id}", s.catalogSameOrigin(s.handleCatalogActorPatch))
	mux.HandleFunc("GET /api/catalog/actors/{id}/portrait", s.handleCatalogActorPortrait)
	mux.HandleFunc("PUT /api/catalog/actors/{id}/portrait", s.catalogSameOrigin(s.handleCatalogActorPortraitWrite))
	mux.HandleFunc("DELETE /api/catalog/actors/{id}/portrait", s.catalogSameOrigin(s.handleCatalogActorPortraitWrite))
	mux.HandleFunc("GET /api/catalog/history", s.handleCatalogHistory)
	mux.HandleFunc("POST /api/catalog/history/clear", s.catalogSameOrigin(s.handleCatalogHistoryClear))
	mux.HandleFunc("GET /api/catalog/personal/stats", func(w http.ResponseWriter, r *http.Request) {
		value, err := s.db.catalog.PersonalStats(r.Context())
		if err != nil {
			catalogError(w, err)
			return
		}
		writeJSON(w, 200, value)
	})
	mux.HandleFunc("GET /api/catalog/lists", s.handleCatalogLists)
	mux.HandleFunc("POST /api/catalog/lists", s.catalogSameOrigin(s.handleCatalogListCreate))
	mux.HandleFunc("GET /api/catalog/lists/{id}", s.handleCatalogList)
	mux.HandleFunc("PATCH /api/catalog/lists/{id}", s.catalogSameOrigin(s.handleCatalogListPatch))
	mux.HandleFunc("PUT /api/catalog/lists/{id}/items", s.catalogSameOrigin(s.handleCatalogListItems))
	mux.HandleFunc("DELETE /api/catalog/lists/{id}", s.catalogSameOrigin(s.handleCatalogListDelete))
}
func catalogPagination(r *http.Request) (int, int, error) {
	limit, offset := 48, 0
	for name, dest := range map[string]*int{"limit": &limit, "offset": &offset} {
		if raw := r.URL.Query().Get(name); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil {
				return 0, 0, catalog.ErrInvalid
			}
			*dest = v
		}
	}
	if limit < 1 || limit > 100 || offset < 0 {
		return 0, 0, catalog.ErrInvalid
	}
	return limit, offset, nil
}
func (s *Service) handleCatalogActors(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := catalogPagination(r)
	if err != nil {
		catalogError(w, err)
		return
	}
	following, err := catalogFollowing(r)
	if err != nil {
		catalogError(w, err)
		return
	}
	page, err := s.db.catalog.FilterActors(r.Context(), r.URL.Query().Get("query"), limit, offset, following)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, page)
}
func (s *Service) handleCatalogActor(w http.ResponseWriter, r *http.Request) {
	value, err := s.db.catalog.Actor(r.Context(), r.PathValue("id"))
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Service) handleCatalogActorPatch(w http.ResponseWriter, r *http.Request) {
	var payload catalog.ActorPatch
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	value, err := s.db.catalog.PatchActor(r.Context(), r.PathValue("id"), payload)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Service) handleCatalogActorPortrait(w http.ResponseWriter, r *http.Request) {
	data, mime, _, err := s.db.catalog.ActorPortrait(r.Context(), r.PathValue("id"))
	if err != nil {
		catalogError(w, err)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method != "HEAD" {
		_, _ = w.Write(data)
	}
}
func (s *Service) handleCatalogActorPortraitWrite(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Image   []byte `json:"image"`
		Version int64  `json:"version"`
	}
	// A dedicated bound permits a 3 MiB image encoded as base64 without raising
	// the request limit of unrelated settings or task APIs.
	r.Body = http.MaxBytesReader(w, r.Body, (4<<20)+4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(w, 400, err)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		catalogError(w, catalog.ErrInvalid)
		return
	}
	if r.Method == "DELETE" {
		if len(payload.Image) != 0 {
			catalogError(w, catalog.ErrInvalid)
			return
		}
	} else if len(payload.Image) == 0 {
		catalogError(w, catalog.ErrInvalid)
		return
	}
	value, err := s.db.catalog.SetActorPortrait(r.Context(), r.PathValue("id"), payload.Version, payload.Image)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Service) handleCatalogHistory(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := catalogPagination(r)
	if err != nil {
		catalogError(w, err)
		return
	}
	value, err := s.db.catalog.History(r.Context(), r.URL.Query().Get("query"), limit, offset)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Service) handleCatalogHistoryClear(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		IDs []string `json:"ids"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	count, err := s.db.catalog.ClearHistory(r.Context(), payload.IDs)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, map[string]int{"deleted": count})
}
func (s *Service) handleCatalogLists(w http.ResponseWriter, r *http.Request) {
	value, err := s.db.catalog.Lists(r.Context())
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": value})
}
func (s *Service) handleCatalogList(w http.ResponseWriter, r *http.Request) {
	value, err := s.db.catalog.List(r.Context(), r.PathValue("id"))
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Service) handleCatalogListCreate(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	value, err := s.db.catalog.CreateList(r.Context(), payload.Name, payload.Description)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 201, value)
}
func (s *Service) handleCatalogListPatch(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Version     int64  `json:"version"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	value, err := s.db.catalog.UpdateList(r.Context(), r.PathValue("id"), payload.Name, payload.Description, payload.Version)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Service) handleCatalogListItems(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		WorkIDs []string `json:"work_ids"`
		Version int64    `json:"version"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	value, err := s.db.catalog.ReplaceListItems(r.Context(), r.PathValue("id"), payload.Version, payload.WorkIDs)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Service) handleCatalogListDelete(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Version int64 `json:"version"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := s.db.catalog.DeleteList(r.Context(), r.PathValue("id"), payload.Version); err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"deleted": true})
}
