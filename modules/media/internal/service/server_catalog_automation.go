package service

import (
	"net/http"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

func (s *Service) registerCatalogAutomationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/catalog/automation", func(w http.ResponseWriter, r *http.Request) {
		config, err := s.db.catalog.Automation(r.Context())
		if err != nil {
			catalogError(w, err)
			return
		}
		writeJSON(w, 200, config)
	})
	mux.HandleFunc("PUT /api/catalog/automation", s.catalogSameOrigin(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			AfterOrganize *bool `json:"after_organize"`
			Version       int64 `json:"version"`
		}
		if err := decodeJSON(w, r, &payload); err != nil || payload.AfterOrganize == nil {
			catalogError(w, catalog.ErrInvalid)
			return
		}
		config, err := s.db.catalog.SetAutomation(r.Context(), catalog.AutomationConfig{AfterOrganize: *payload.AfterOrganize, Version: payload.Version})
		if err != nil {
			catalogError(w, err)
			return
		}
		writeJSON(w, 200, config)
	}))
}
