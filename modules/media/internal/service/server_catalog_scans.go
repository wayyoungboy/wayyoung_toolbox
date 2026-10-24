package service

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

func (s *Service) registerCatalogScanRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/catalog/libraries/{id}/scans", s.catalogSameOrigin(s.handleCatalogStartScan))
	mux.HandleFunc("GET /api/catalog/libraries/{id}/scans", s.handleCatalogScanJobs)
	mux.HandleFunc("GET /api/catalog/scans", s.handleCatalogScanJobs)
	mux.HandleFunc("GET /api/catalog/scans/{id}", s.handleCatalogScanJob)
	for _, action := range []string{"pause", "resume", "cancel", "retry"} {
		mux.HandleFunc("POST /api/catalog/scans/{id}/"+action, s.catalogSameOrigin(func(w http.ResponseWriter, r *http.Request) {
			s.handleCatalogScanAction(w, r, action)
		}))
	}
}

func catalogScanID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		catalogError(w, catalog.ErrInvalid)
		return 0, false
	}
	return id, true
}

func (s *Service) handleCatalogStartScan(w http.ResponseWriter, r *http.Request) {
	var payload *struct{}
	if err := decodeJSON(w, r, &payload); err != nil || payload == nil {
		catalogError(w, catalog.ErrInvalid)
		return
	}
	id, ok := catalogScanID(w, r)
	if !ok {
		return
	}
	job, err := s.db.catalog.StartScan(r.Context(), id)
	if err != nil {
		catalogError(w, err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("%s/api/catalog/scans/%d", s.prefix, job.ID))
	s.wakeCatalogScans()
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Service) handleCatalogScanJob(w http.ResponseWriter, r *http.Request) {
	id, ok := catalogScanID(w, r)
	if !ok {
		return
	}
	job, err := s.db.catalog.ScanJob(r.Context(), id)
	if err != nil {
		catalogError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, job)
}

func (s *Service) handleCatalogScanJobs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	for key, values := range query {
		if (key != "before_id" && key != "limit") || len(values) != 1 || values[0] == "" {
			catalogError(w, catalog.ErrInvalid)
			return
		}
	}
	before, limit := int64(0), 20
	var err error
	if raw := query.Get("before_id"); raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before < 0 {
			catalogError(w, catalog.ErrInvalid)
			return
		}
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			catalogError(w, catalog.ErrInvalid)
			return
		}
	}
	var jobs []catalog.ScanJob
	var active *catalog.ScanJob
	if r.PathValue("id") != "" {
		id, ok := catalogScanID(w, r)
		if !ok {
			return
		}
		jobs, err = s.db.catalog.LibraryScanJobs(r.Context(), id, before, limit)
		if err == nil {
			active, err = s.db.catalog.ActiveLibraryScan(r.Context(), id)
		}
	} else {
		jobs, err = s.db.catalog.ScanJobs(r.Context(), before, limit)
	}
	if err != nil {
		catalogError(w, err)
		return
	}
	next := int64(0)
	if len(jobs) == limit {
		next = jobs[len(jobs)-1].ID
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"items": jobs, "next_cursor": next, "active": active})
}

func (s *Service) handleCatalogScanAction(w http.ResponseWriter, r *http.Request, action string) {
	var payload struct {
		Version int64 `json:"version"`
	}
	if err := decodeJSON(w, r, &payload); err != nil || payload.Version < 1 {
		catalogError(w, catalog.ErrInvalid)
		return
	}
	id, ok := catalogScanID(w, r)
	if !ok {
		return
	}
	// Cancel the exact attempt before waiting for the sole SQLite connection.
	// A publishing transaction can then observe cancellation and roll back;
	// a stale browser revision cannot interrupt a newer attempt.
	if action == "pause" || action == "cancel" {
		unlock := s.lockCatalogScanControl(id, payload.Version)
		defer unlock()
	}
	job, err := s.db.catalog.ChangeScanJob(r.Context(), id, payload.Version, action)
	if err != nil {
		catalogError(w, err)
		return
	}
	if action == "resume" || action == "retry" {
		s.wakeCatalogScans()
	}
	writeJSON(w, http.StatusOK, job)
}
