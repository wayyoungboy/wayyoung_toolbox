package service

import (
	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
	"net/http"
	"strconv"
)

func (s *Service) registerCatalogTasteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/catalog/taste", s.handleCatalogTaste)
	mux.HandleFunc("GET /api/catalog/taste/config", func(w http.ResponseWriter, r *http.Request) {
		value, err := s.db.catalog.TasteConfig(r.Context())
		if err != nil {
			catalogError(w, err)
			return
		}
		writeJSON(w, 200, value)
	})
	mux.HandleFunc("PUT /api/catalog/taste/config", s.catalogSameOrigin(s.handleCatalogTasteConfig))
	mux.HandleFunc("POST /api/catalog/taste/reset", s.catalogSameOrigin(s.handleCatalogTasteReset))
	mux.HandleFunc("PUT /api/catalog/actors/{id}/follow", s.catalogSameOrigin(s.handleCatalogActorFollow))
}
func (s *Service) handleCatalogTaste(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := catalogPagination(r)
	if err != nil {
		catalogError(w, err)
		return
	}
	value, err := s.db.catalog.Taste(r.Context(), limit, offset)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Service) handleCatalogTasteConfig(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Enabled *bool           `json:"enabled"`
		Sources map[string]bool `json:"sources"`
		Version int64           `json:"version"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	if payload.Enabled == nil {
		catalogError(w, catalog.ErrInvalid)
		return
	}
	value, err := s.db.catalog.SetTasteConfig(r.Context(), catalog.TasteConfig{Enabled: *payload.Enabled, Sources: payload.Sources, Version: payload.Version})
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Service) handleCatalogTasteReset(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Version      int64  `json:"version"`
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	value, err := s.db.catalog.ResetTaste(r.Context(), payload.Version, payload.Confirmation)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Service) handleCatalogActorFollow(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Following *bool  `json:"following"`
		Version   *int64 `json:"version"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	if payload.Following == nil || payload.Version == nil {
		catalogError(w, catalog.ErrInvalid)
		return
	}
	value, err := s.db.catalog.SetActorFollow(r.Context(), r.PathValue("id"), *payload.Following, *payload.Version)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, value)
}
func catalogFollowing(r *http.Request) (bool, error) {
	if raw := r.URL.Query().Get("following"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return false, catalog.ErrInvalid
		}
		return v, nil
	}
	return false, nil
}
