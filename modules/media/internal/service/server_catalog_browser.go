package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/browserdata"
	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

func (s *Service) registerBrowserRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/catalog/browser/sources", func(w http.ResponseWriter, r *http.Request) {
		items, err := s.db.catalog.BrowserSources(r.Context())
		if err != nil {
			catalogError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": items})
	})
	mux.HandleFunc("POST /api/catalog/browser/import/preview", s.catalogSameOrigin(s.handleBrowserImport))
	mux.HandleFunc("POST /api/catalog/browser/import", s.catalogSameOrigin(s.handleBrowserImport))
	mux.HandleFunc("PUT /api/catalog/browser/sources/{id}", s.catalogSameOrigin(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Version int64 `json:"version"`
			Reading *bool `json:"reading"`
		}
		if err := decodeJSON(w, r, &payload); err != nil || payload.Reading == nil {
			catalogError(w, catalog.ErrInvalid)
			return
		}
		value, err := s.db.catalog.SetBrowserReading(r.Context(), r.PathValue("id"), payload.Version, *payload.Reading)
		if err != nil {
			catalogError(w, err)
			return
		}
		writeJSON(w, 200, value)
	}))
	mux.HandleFunc("DELETE /api/catalog/browser/sources/{id}", s.catalogSameOrigin(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Version      int64  `json:"version"`
			Confirmation string `json:"confirmation"`
		}
		if err := decodeJSON(w, r, &payload); err != nil {
			catalogError(w, catalog.ErrInvalid)
			return
		}
		if err := s.db.catalog.DeleteBrowserSource(r.Context(), r.PathValue("id"), payload.Version, payload.Confirmation); err != nil {
			catalogError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"deleted": true})
	}))
}
func (s *Service) handleBrowserImport(w http.ResponseWriter, r *http.Request) {
	if !s.browserImportMu.TryLock() {
		writeError(w, 409, errors.New("已有浏览器资料正在解析，请稍后重试"))
		return
	}
	defer s.browserImportMu.Unlock()
	// A dedicated JSON/base64 upload bound leaves all normal configuration API
	// limits unchanged; no multipart disk spill or user-controlled path is used.
	r.Body = http.MaxBytesReader(w, r.Body, (browserdata.MaxUpload*4/3)+(1<<20))
	var payload catalog.BrowserUpload
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&payload); err != nil {
		catalogError(w, catalog.ErrInvalid)
		return
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		catalogError(w, catalog.ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if r.URL.Path == "/api/catalog/browser/import/preview" {
		value, err := s.db.catalog.PreviewBrowser(ctx, payload)
		if err != nil {
			browserImportError(w, err)
			return
		}
		writeJSON(w, 200, value)
		return
	}
	value, err := s.db.catalog.ImportBrowser(ctx, payload)
	if err != nil {
		browserImportError(w, err)
		return
	}
	writeJSON(w, 200, value)
}
func browserImportError(w http.ResponseWriter, err error) {
	if errors.Is(err, browserdata.ErrFormat) || errors.Is(err, browserdata.ErrLimit) {
		writeError(w, 400, err)
		return
	}
	catalogError(w, err)
}
