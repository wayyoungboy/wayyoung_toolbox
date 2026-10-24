package service

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

func (s *Service) registerCatalogRoutes(mux *http.ServeMux) {
	s.registerCatalogPersonalRoutes(mux)
	s.registerCatalogAutomationRoutes(mux)
	s.registerCatalogScanRoutes(mux)
	mux.HandleFunc("GET /api/resources/search", s.handleResourceSearchCache)
	mux.HandleFunc("POST /api/resources/search", s.catalogSameOrigin(s.handleResourceSearch))
	mux.HandleFunc("GET /api/resources/downloads", s.handleResourceDownloads)
	mux.HandleFunc("GET /api/resources/downloads/selection", s.handleResourceDownloadSelection)
	mux.HandleFunc("POST /api/resources/downloads", s.catalogSameOrigin(s.handleResourceDownload))
	mux.HandleFunc("GET /api/catalog/session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]string{"csrf": s.catalogCSRF})
	})
	mux.HandleFunc("GET /api/catalog/libraries", s.handleCatalogLibraries)
	mux.HandleFunc("POST /api/catalog/libraries", s.catalogSameOrigin(s.handleCatalogAddLibrary))
	mux.HandleFunc("PATCH /api/catalog/libraries/{id}", s.catalogSameOrigin(s.handleCatalogRelink))
	mux.HandleFunc("POST /api/catalog/libraries/{id}/scan", s.catalogSameOrigin(s.handleCatalogScan))
	mux.HandleFunc("GET /api/catalog/works", s.handleCatalogWorks)
	mux.HandleFunc("POST /api/catalog/deletion/preview", s.catalogSameOrigin(s.handleCatalogDeletePreview))
	mux.HandleFunc("POST /api/catalog/deletion/execute", s.catalogSameOrigin(s.handleCatalogDeleteExecute))
	mux.HandleFunc("POST /api/queue/cleanup", s.catalogSameOrigin(s.handleQueueCleanup))
	mux.HandleFunc("GET /api/queue/cleanup", s.handleQueueCleanupCandidates)
	mux.HandleFunc("GET /api/catalog/works/{id}", s.handleCatalogWork)
	mux.HandleFunc("PATCH /api/catalog/works/{id}", s.catalogSameOrigin(s.handleCatalogTitle))
	mux.HandleFunc("PATCH /api/catalog/works/{id}/personal", s.catalogSameOrigin(s.handleCatalogPersonal))
	mux.HandleFunc("PUT /api/catalog/assets/{id}/progress", s.catalogSameOrigin(s.handleCatalogProgress))
	mux.HandleFunc("GET /api/catalog/assets/{id}/stream", s.handleCatalogStream)
	mux.HandleFunc("GET /api/catalog/assets/{id}/poster", s.handleCatalogPoster)
	mux.HandleFunc("GET /api/catalog/assets/{id}/subtitles/{index}", s.handleCatalogSubtitle)
}

func (s *Service) handleCatalogDeletePreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WorkIDs []string `json:"work_ids"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, 400, err)
		return
	}
	if !s.mediaFiles.TryLock() {
		catalogError(w, catalog.ErrBusy)
		return
	}
	defer s.mediaFiles.Unlock()
	plan, err := s.db.catalog.PreviewDelete(r.Context(), body.WorkIDs)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, plan)
}

func (s *Service) handleCatalogDeleteExecute(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WorkIDs      []string `json:"work_ids"`
		Token        string   `json:"token"`
		Confirmation string   `json:"confirmation"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, 400, err)
		return
	}
	if body.Confirmation != "永久删除" {
		catalogError(w, catalog.ErrInvalid)
		return
	}
	if !s.acquireMediaFileWriter() {
		catalogError(w, catalog.ErrBusy)
		return
	}
	defer s.mediaFiles.Unlock()
	result, err := s.db.catalog.Delete(r.Context(), body.WorkIDs, body.Token)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (s *Service) catalogSameOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.catalogCSRF == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Catalog-CSRF")), []byte(s.catalogCSRF)) != 1 {
			writeError(w, http.StatusForbidden, fmt.Errorf("请求校验失效，请刷新页面"))
			return
		}
		contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || contentType != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, fmt.Errorf("需要 JSON 请求"))
			return
		}
		if !s.validCatalogOrigin(r) {
			writeError(w, http.StatusForbidden, fmt.Errorf("请从本应用页面执行操作"))
			return
		}
		next(w, r)
	}
}

// Called only after administrator and unguessable CSRF-token checks. fnOS may
// rewrite Host at its Unix-socket gateway; no caller-supplied forwarding header
// is accepted as proof of origin. The configured application path is required
// for LAN browsers which omit Fetch Metadata.
func (s *Service) validCatalogOrigin(r *http.Request) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "same-origin" {
		return false
	}
	raw := r.Header.Get("Origin")
	if raw == "" {
		return site == "" && r.Referer() == ""
	}
	origin, err := url.Parse(raw)
	if err != nil || origin.Host == "" || origin.User != nil || origin.Opaque != "" || origin.Path != "" || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || (origin.Scheme != "http" && origin.Scheme != "https") || strings.ContainsAny(raw, "#, \t\r\n") {
		return false
	}
	appReferrer := false
	if r.Referer() != "" {
		ref, err := url.Parse(r.Referer())
		if err != nil || ref.User != nil || ref.Host != origin.Host || ref.Scheme != origin.Scheme {
			return false
		}
		clean := path.Clean(ref.Path)
		appReferrer = s.prefix != "" && (ref.Path == clean || ref.Path == clean+"/") && (clean == s.prefix || strings.HasPrefix(clean, s.prefix+"/"))
	}
	if strings.EqualFold(origin.Host, r.Host) {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		return origin.Scheme == scheme
	}
	return (s.localDev || strings.EqualFold(r.Header.Get("X-Trim-Isadmin"), "true")) && (site == "same-origin" || (site == "" && appReferrer))
}

func catalogError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "馆藏操作失败，请查看运行日志"
	switch {
	case errors.Is(err, catalog.ErrInvalid):
		status, message = http.StatusBadRequest, "请求或媒体资料格式无效，请检查输入"
	case errors.Is(err, catalog.ErrNotFound):
		status, message = http.StatusNotFound, "馆藏记录不存在"
	case errors.Is(err, catalog.ErrConflict):
		status, message = http.StatusConflict, "记录或媒体文件已变化，请刷新或重新扫描"
	case errors.Is(err, catalog.ErrUnavailable):
		status, message = http.StatusConflict, "媒体目录或文件暂不可访问，请检查挂载和权限"
	case errors.Is(err, catalog.ErrBusy):
		status, message = http.StatusConflict, "馆藏扫描正在运行，请稍后再试"
	case errors.Is(err, catalog.ErrDeletePending):
		status, message = http.StatusConflict, "检测到未完成的删除操作，已阻止继续删除；请在运行日志中核对遗留文件"
		slog.Warn("interrupted catalog deletion", "error", err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status, message = http.StatusRequestTimeout, "操作已取消或超时，上次馆藏记录保留"
	default:
		slog.Error("catalog request failed", "error", err)
	}
	writeError(w, status, errors.New(message))
}

func (s *Service) handleCatalogLibraries(w http.ResponseWriter, r *http.Request) {
	items, err := s.db.catalog.Libraries(r.Context())
	if err != nil {
		catalogError(w, err)
		return
	}
	completed := ""
	if cfg, cfgErr := s.db.JavspConfig(); cfgErr == nil {
		if root, available, rootErr := staticJavspOutputRoot(cfg); rootErr == nil && available {
			completed = root
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "completed_root": completed})
}

func (s *Service) handleCatalogAddLibrary(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name string `json:"name"`
		Root string `json:"root"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	item, err := s.db.catalog.AddLibrary(r.Context(), payload.Name, payload.Root)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Service) handleCatalogScan(w http.ResponseWriter, r *http.Request) {
	var payload struct{}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		catalogError(w, catalog.ErrInvalid)
		return
	}
	if !s.mediaFiles.TryLock() {
		catalogError(w, catalog.ErrBusy)
		return
	}
	defer s.mediaFiles.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 110*time.Second)
	defer cancel()
	result, err := s.db.catalog.Scan(ctx, id)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) handleCatalogRelink(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Root string `json:"root"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		catalogError(w, catalog.ErrInvalid)
		return
	}
	if !s.mediaFiles.TryLock() {
		catalogError(w, catalog.ErrBusy)
		return
	}
	defer s.mediaFiles.Unlock()
	item, err := s.db.catalog.RelinkLibrary(r.Context(), id, payload.Root)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Service) handleCatalogWorks(w http.ResponseWriter, r *http.Request) {
	filter := catalog.Filter{Query: r.URL.Query().Get("query"), Actor: r.URL.Query().Get("actor"), Tag: r.URL.Query().Get("tag"), Sort: r.URL.Query().Get("sort"), Limit: 48}
	filter.ActorID = r.URL.Query().Get("actor_id")
	filter.ListID = r.URL.Query().Get("list_id")
	if raw := r.URL.Query().Get("watched"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			catalogError(w, catalog.ErrInvalid)
			return
		}
		filter.Watched = &value
	}
	for key, destination := range map[string]*int{"limit": &filter.Limit, "offset": &filter.Offset} {
		if value := r.URL.Query().Get(key); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				catalogError(w, catalog.ErrInvalid)
				return
			}
			*destination = parsed
		}
	}
	if value := r.URL.Query().Get("library_id"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			catalogError(w, catalog.ErrInvalid)
			return
		}
		filter.LibraryID = parsed
	}
	for key, destination := range map[string]*bool{"favorite": &filter.Favorite, "continue": &filter.Continue, "watch_later": &filter.WatchLater} {
		if value := r.URL.Query().Get(key); value != "" {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				catalogError(w, catalog.ErrInvalid)
				return
			}
			*destination = parsed
		}
	}
	page, err := s.db.catalog.Works(r.Context(), filter)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Service) handleCatalogWork(w http.ResponseWriter, r *http.Request) {
	work, err := s.db.catalog.Work(r.Context(), r.PathValue("id"))
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, work)
}

func (s *Service) handleCatalogPersonal(w http.ResponseWriter, r *http.Request) {
	var payload catalog.PersonalPatch
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	work, err := s.db.catalog.PatchPersonal(r.Context(), r.PathValue("id"), payload)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, work)
}

func (s *Service) handleCatalogTitle(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Title string `json:"title"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	work, err := s.db.catalog.SetTitle(r.Context(), r.PathValue("id"), payload.Title)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, work)
}

func (s *Service) handleCatalogProgress(w http.ResponseWriter, r *http.Request) {
	var payload catalog.Progress
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	value, err := s.db.catalog.SaveProgress(r.Context(), r.PathValue("id"), payload)
	if err != nil {
		catalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func serveCatalogFile(w http.ResponseWriter, r *http.Request, file catalog.OpenedFile) {
	defer file.File.Close()
	// A video response can last longer than the JSON API write timeout.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		slog.Warn("clear media write deadline", "error", err)
	}
	w.Header().Set("Content-Type", file.MIME)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, file.Name, file.Modified, file.File)
}

func (s *Service) handleCatalogStream(w http.ResponseWriter, r *http.Request) {
	file, err := s.db.catalog.OpenStream(r.Context(), r.PathValue("id"))
	if err != nil {
		catalogError(w, err)
		return
	}
	serveCatalogFile(w, r, file)
}

func (s *Service) handleCatalogPoster(w http.ResponseWriter, r *http.Request) {
	file, err := s.db.catalog.OpenPoster(r.Context(), r.PathValue("id"))
	if err != nil {
		catalogError(w, err)
		return
	}
	serveCatalogFile(w, r, file)
}

func (s *Service) handleCatalogSubtitle(w http.ResponseWriter, r *http.Request) {
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		catalogError(w, catalog.ErrInvalid)
		return
	}
	data, err := s.db.catalog.Subtitle(r.Context(), r.PathValue("id"), index)
	if err != nil {
		catalogError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		slog.Debug("subtitle response closed", "error", err)
	}
}
