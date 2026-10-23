package service

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/playback"
)

// Lazily create the manager: opening other media pages never starts probes or encoders.
func (s *Service) playbackManager() (*playback.Manager, error) {
	s.playbackMu.Lock()
	defer s.playbackMu.Unlock()
	if s.playbackClosed {
		return nil, playback.ErrUnavailable
	}
	if s.playback == nil {
		manager, err := playback.New(playback.Options{Dir: filepath.Join(s.dataDir, "playback-cache"), Open: func(ctx context.Context, id string) (playback.Source, error) {
			file, err := s.db.catalog.OpenStream(ctx, id)
			if err != nil {
				return playback.Source{}, err
			}
			return playback.Source{File: file.File, Name: file.Name}, nil
		}})
		if err != nil {
			return nil, err
		}
		s.playback = manager
	}
	return s.playback, nil
}
func (s *Service) closePlayback(ctx context.Context) error {
	s.playbackMu.Lock()
	s.playbackClosed = true
	manager := s.playback
	s.playbackMu.Unlock()
	if manager != nil {
		return manager.Close(ctx)
	}
	return nil
}
func (s *Service) registerPlaybackRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/playback/resources", s.withPlayback(func(m *playback.Manager, w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, m.Snapshot()) }))
	mux.HandleFunc("GET /api/catalog/assets/{id}/info", s.withPlayback(func(m *playback.Manager, w http.ResponseWriter, r *http.Request) {
		result, err := m.Probe(r.Context(), r.PathValue("id"))
		if err != nil {
			playbackError(w, err)
			return
		}
		writeJSON(w, 200, result)
	}))
	mux.HandleFunc("POST /api/playback/capabilities", s.catalogSameOrigin(s.withPlayback(func(m *playback.Manager, w http.ResponseWriter, r *http.Request) {
		var body struct{}
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, 400, err)
			return
		}
		writeJSON(w, 200, m.Capabilities(r.Context()))
	})))
	mux.HandleFunc("POST /api/playback/sessions", s.catalogSameOrigin(s.withPlayback(func(m *playback.Manager, w http.ResponseWriter, r *http.Request) {
		var body playback.Request
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, 400, err)
			return
		}
		result, err := m.Start(r.Context(), body)
		if err != nil {
			playbackError(w, err)
			return
		}
		writeJSON(w, 201, result)
	})))
	mux.HandleFunc("GET /api/playback/sessions/{id}", s.withPlayback(func(m *playback.Manager, w http.ResponseWriter, r *http.Request) {
		result, err := m.Status(r.Context(), r.PathValue("id"))
		if err != nil {
			playbackError(w, err)
			return
		}
		writeJSON(w, 200, result)
	}))
	mux.HandleFunc("POST /api/playback/sessions/{id}/heartbeat", s.catalogSameOrigin(s.withPlayback(func(m *playback.Manager, w http.ResponseWriter, r *http.Request) {
		var body struct {
			Paused *bool `json:"paused"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, 400, err)
			return
		}
		if body.Paused == nil {
			playbackError(w, playback.ErrInvalid)
			return
		}
		result, err := m.Pulse(r.Context(), r.PathValue("id"), *body.Paused)
		if err != nil {
			playbackError(w, err)
			return
		}
		writeJSON(w, 200, result)
	})))
	mux.HandleFunc("DELETE /api/playback/sessions/{id}", s.catalogSameOrigin(s.withPlayback(func(m *playback.Manager, w http.ResponseWriter, r *http.Request) {
		var body struct{}
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, 400, err)
			return
		}
		if err := m.Stop(r.Context(), r.PathValue("id")); err != nil {
			playbackError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})))
	mux.HandleFunc("GET /api/playback/sessions/{id}/{file}", s.withPlayback(func(m *playback.Manager, w http.ResponseWriter, r *http.Request) {
		data, err := m.Read(r.Context(), r.PathValue("id"), r.PathValue("file"))
		if err != nil {
			playbackError(w, err)
			return
		}
		contentType := "video/mp2t"
		if strings.HasSuffix(r.PathValue("file"), ".m3u8") {
			contentType = "application/vnd.apple.mpegurl"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(data)
	}))
}
func (s *Service) withPlayback(next func(*playback.Manager, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		manager, err := s.playbackManager()
		if err != nil {
			playbackError(w, err)
			return
		}
		next(manager, w, r)
	}
}
func playbackError(w http.ResponseWriter, err error) {
	status, message := 500, "兼容播放失败，请重新打开播放器或选择原文件播放"
	switch {
	case errors.Is(err, playback.ErrInvalid):
		status, message = 400, "播放参数无效"
	case errors.Is(err, playback.ErrNotFound):
		status, message = 404, "播放会话或片段已过期，请重新播放"
	case errors.Is(err, playback.ErrBusy):
		status, message = 409, "兼容播放并发已满，请关闭其他播放器后重试"
	case errors.Is(err, playback.ErrUnavailable):
		status, message = 503, "未找到可用的 FFmpeg/FFprobe，仍可尝试原文件播放"
	case errors.Is(err, playback.ErrUnsupported), errors.Is(err, playback.ErrProbe):
		status, message = 422, "无法识别此文件或播放方式，请检查媒体文件"
	case errors.Is(err, playback.ErrChanged):
		status, message = 409, "媒体文件或缓存目录已变化，会话已停止，请刷新后重试"
	case errors.Is(err, playback.ErrCacheLimit):
		status, message = 409, "播放缓存已达到限制，会话已停止"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status, message = 408, "播放准备已取消或超时"
	case errors.Is(err, catalog.ErrNotFound), errors.Is(err, catalog.ErrUnavailable), errors.Is(err, catalog.ErrConflict):
		catalogError(w, err)
		return
	}
	writeError(w, status, errors.New(message))
}
