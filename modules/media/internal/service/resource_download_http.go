package service

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

func (s *Service) handleResourceDownloadSelection(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	query, err := url.ParseQuery(r.URL.RawQuery)
	for key, values := range query {
		if (key != "code" && key != "provider" && key != "infohash") || len(values) != 1 {
			err = catalog.ErrInvalid
		}
	}
	input, inputErr := resourceDownloadIdentity(ResourceDownloadSelection{Code: query.Get("code"), Provider: query.Get("provider"), InfoHash: query.Get("infohash")})
	if err != nil || inputErr != nil {
		catalogError(w, catalog.ErrInvalid)
		return
	}
	job, err := findResourceDownloadOwner(r.Context(), s.db.db, input)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 200, map[string]any{"job": nil})
		return
	}
	if err != nil {
		catalogError(w, err)
		return
	}
	if job.MediaID != "" && job.MediaID != input.Code {
		catalogError(w, catalog.ErrConflict)
		return
	}
	writeJSON(w, 200, map[string]any{"job": job})
}

// The POST only persists explicit intent. The shared in-process queue performs
// dispatch; HTTP retries never replay a qB request or reset the durable state.
func (s *Service) handleResourceDownload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body struct {
		ResourceDownloadSelection
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSON(w, r, &body); err != nil || body.Confirmation != "下载资源" {
		catalogError(w, catalog.ErrInvalid)
		return
	}
	job, created, err := s.db.EnqueueResourceDownload(r.Context(), body.ResourceDownloadSelection)
	if err != nil {
		catalogError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(w, status, map[string]any{"job": job, "created": created})
}

func (s *Service) handleResourceDownloads(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	query, err := url.ParseQuery(r.URL.RawQuery)
	for key, values := range query {
		if key != "limit" || len(values) != 1 {
			err = catalog.ErrInvalid
		}
	}
	limit := 20
	if values, ok := query["limit"]; ok {
		var parseErr error
		limit, parseErr = strconv.Atoi(values[0])
		if parseErr != nil || limit < 1 || limit > 100 {
			err = catalog.ErrInvalid
		}
	}
	if err != nil {
		catalogError(w, catalog.ErrInvalid)
		return
	}
	rows, err := s.db.db.QueryContext(r.Context(), `SELECT `+mediaJobColumns+` FROM media_jobs WHERE kind='torrent' AND json_extract(payload,'$.resource') IS NOT NULL ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		catalogError(w, err)
		return
	}
	defer rows.Close()
	jobs := []MediaJob{}
	for rows.Next() {
		job, err := scanMediaJob(rows)
		if err != nil {
			catalogError(w, err)
			return
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": jobs})
}
