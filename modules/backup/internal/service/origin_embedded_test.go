package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"fnos-backup-center/internal/tasks"
)

// HTTP LAN browsers omit Fetch Metadata and the fnOS gateway rewrites Host.
// The configured module prefix must be used, not the standalone app constant.
func TestEmbeddedHTTPGatewayCreatesScheduledTask(t *testing.T) {
	const prefix = "/app/wayyoung_toolbox/modules/backup"
	for _, tc := range []struct {
		name, ref, site      string
		trusted, admin, csrf bool
		want                 int
	}{
		{"module root", prefix + "/", "", true, true, true, 201},
		{"module page", prefix + "/tasks", "", true, true, true, 201},
		{"standalone path", Prefix + "/", "", true, true, true, 403},
		{"other module", "/app/wayyoung_toolbox/modules/media/", "", true, true, true, 403},
		{"toolbox root", "/app/wayyoung_toolbox/", "", true, true, true, 403},
		{"prefix lookalike", prefix + "_evil/", "", true, true, true, 403},
		{"path traversal", prefix + "/../media/", "", true, true, true, 403},
		{"encoded traversal", prefix + "/%2e%2e/media/", "", true, true, true, 403},
		{"cross site", prefix + "/", "cross-site", true, true, true, 403},
		{"same site", prefix + "/", "same-site", true, true, true, 403},
		{"no gateway", prefix + "/", "", false, true, true, 403},
		{"no admin", prefix + "/", "", true, false, true, 403},
		{"no token", prefix + "/", "", true, true, false, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, selection := fixtureWithPrefix(t, prefix)
			body, err := json.Marshal(tasks.Task{Name: "fixture", Selection: selection, Schedule: tasks.Schedule{Enabled: true, Kind: "daily", At: "02:00", Zone: "Asia/Shanghai"}})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "http://localhost"+prefix+"/api/tasks", bytes.NewReader(body))
			r = r.WithContext(context.WithValue(r.Context(), gatewayKey{}, tc.trusted))
			r.Header.Set("X-Trim-Userid", "1000")
			if tc.admin {
				r.Header.Set("X-Trim-Isadmin", "true")
			}
			if tc.csrf {
				r.Header.Set("X-Backup-CSRF", s.csrf)
			}
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "http://nas.test:8001")
			r.Header.Set("Referer", "http://nas.test:8001"+tc.ref)
			r.Header.Set("Sec-Fetch-Site", tc.site)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d, want=%d: %s", w.Code, tc.want, w.Body.String())
			}
			state := s.Manager.State()
			if tc.want == 201 {
				if len(state.Tasks) != 1 || !state.Tasks[0].Schedule.Enabled || state.Tasks[0].Next.IsZero() {
					t.Fatalf("scheduled task not persisted: %+v", state.Tasks)
				}
			} else if len(state.Tasks) != 0 {
				t.Fatal("rejected request created a task")
			}
			if len(state.Runs) != 0 {
				t.Fatal("saving a task must not start a backup")
			}
		})
	}
}
