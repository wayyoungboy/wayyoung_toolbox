package webapp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMediaCSPAllowsOnlyLocalPlaybackBlobs(t *testing.T) {
	for _, path := range []string{"/modules/media/", "/modules/media/api/playback/resources"} {
		response := httptest.NewRecorder()
		securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		policy := response.Header().Get("Content-Security-Policy")
		if !strings.Contains(policy, "media-src 'self' blob:") || !strings.Contains(policy, "worker-src 'self' blob:") {
			t.Fatal("local HLS or subtitle blobs blocked", policy)
		}
		if strings.Contains(policy, "unsafe-eval") || strings.Contains(policy, "https:") {
			t.Fatal("unrelated policy relaxed", policy)
		}
	}
}
