package service

import (
	"context"
	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/playback"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlaybackAPIRequiresAdministratorAndCSRF(t *testing.T) {
	s := testService(t)
	// Authorization/error mapping is a unit test, not a host-tool check.
	// The missing asset must fail before either synthetic tool path executes.
	t.Setenv("PATH", t.TempDir())
	manager, err := playback.New(playback.Options{Dir: filepath.Join(t.TempDir(), "cache"), FFmpeg: "unused-ffmpeg", FFprobe: "unused-ffprobe", Open: func(context.Context, string) (playback.Source, error) { return playback.Source{}, catalog.ErrNotFound }})
	if err != nil {
		t.Fatal(err)
	}
	s.playback = manager
	for _, path := range []string{"/api/playback/resources", "/api/catalog/assets/missing/info", "/api/playback/sessions/missing/index.m3u8"} {
		denied := httptest.NewRecorder()
		s.Handler().ServeHTTP(denied, httptest.NewRequest("GET", path, nil))
		if denied.Code != 403 {
			t.Fatalf("%s unauthenticated=%d", path, denied.Code)
		}
	}
	for _, path := range []string{"/api/playback/capabilities", "/api/playback/sessions", "/api/playback/sessions/id/heartbeat"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		r.Header.Set("X-Trim-Isadmin", "true")
		r.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, r)
		if response.Code != 403 {
			t.Fatalf("%s missing CSRF=%d", path, response.Code)
		}
	}
	if response := catalogRequest(t, s, "GET", "/api/playback/resources", nil); response.Code != 200 || !strings.Contains(response.Body.String(), `"active":0`) {
		t.Fatalf("resources: %d %s", response.Code, response.Body.String())
	}
	if response := catalogRequest(t, s, "POST", "/api/playback/sessions", map[string]any{"asset_id": "missing", "mode": "invalid"}); response.Code != 400 {
		t.Fatalf("invalid mode=%d %s", response.Code, response.Body.String())
	}
	if response := catalogRequest(t, s, "GET", "/api/catalog/assets/missing/info", nil); response.Code != 404 {
		t.Fatalf("unregistered probe=%d %s", response.Code, response.Body.String())
	}
	if response := catalogRequest(t, s, "DELETE", "/api/playback/sessions/missing", map[string]any{}); response.Code != 200 {
		t.Fatalf("idempotent stop=%d", response.Code)
	}
}
