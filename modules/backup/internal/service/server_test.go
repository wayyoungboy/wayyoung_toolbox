package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"fnos-backup-center/internal/backup"
	"fnos-backup-center/internal/tasks"
)

type testMounts struct{ m backup.Mount }

func (p testMounts) List() ([]backup.Mount, error) { return []backup.Mount{p.m}, nil }
func (p testMounts) FileID(*os.File) (int, error)  { return p.m.ID, nil }

func fixture(t *testing.T) (*Server, backup.Selection) {
	return fixtureWithPrefix(t, "")
}

func fixtureWithPrefix(t *testing.T, prefix string) (*Server, backup.Selection) {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	source, target := filepath.Join(base, "source"), filepath.Join(base, "cloud")
	for _, p := range []string{source, target, filepath.Join(base, "public")} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "public", "index.html"), []byte("backup UI"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "a.txt"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	m := backup.Mount{ID: 5, Point: target, Source: "fixture:remote", Type: "fuse.rclone", Root: "/"}
	s, err := New(Options{DataDir: filepath.Join(base, "state"), PublicDir: filepath.Join(base, "public"), Prefix: prefix, LocalRoots: []string{source, filepath.Join(base, "restore-parent")}, Mounts: testMounts{m}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Manager.Close(context.Background()) })
	return s, backup.Selection{Source: source, Target: target, Mount: m}
}
func req(t *testing.T, s *Server, method, path string, body any, trusted, admin bool, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, "http://nas.test/app/backup_center"+path, &buf)
	if trusted {
		r = r.WithContext(context.WithValue(r.Context(), gatewayKey{}, true))
	}
	if admin {
		r.Header.Set("X-Trim-Isadmin", "true")
		r.Header.Set("X-Trim-Userid", "1000")
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://nas.test")
	if csrf {
		r.Header.Set("X-Backup-CSRF", s.csrf)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestAuthCSRFAndPrefix(t *testing.T) {
	s, _ := fixture(t)
	for _, flags := range [][2]bool{{false, false}, {false, true}, {true, false}} {
		if w := req(t, s, "GET", "/api/state", nil, flags[0], flags[1], false); w.Code != 403 {
			t.Fatalf("auth accepted %v: %d", flags, w.Code)
		}
	}
	if w := req(t, s, "GET", "/api/state", nil, true, true, false); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := req(t, s, "PUT", "/api/settings", map[string]int{"retain": 20}, true, true, false); w.Code != 403 {
		t.Fatal("CSRF accepted")
	}
	if w := req(t, s, "PUT", "/api/settings", map[string]int{"retain": 20}, true, true, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	r := httptest.NewRequest("PUT", "http://nas.test/app/backup_center/api/settings", bytes.NewBufferString(`{"retain":20}`))
	r = r.WithContext(context.WithValue(r.Context(), gatewayKey{}, true))
	r.Header.Set("X-Trim-Isadmin", "true")
	r.Header.Set("X-Trim-Userid", "1000")
	r.Header.Set("X-Backup-CSRF", s.csrf)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://evil.test")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("wrong origin accepted")
	}
	if w := req(t, s, "GET", "", nil, true, true, false); w.Code != http.StatusPermanentRedirect {
		t.Fatal("relative assets redirect missing")
	}
}

func TestNativeGatewayRewrittenHostStillRequiresBrowserSameOriginAndCSRF(t *testing.T) {
	s, _ := fixture(t)
	for _, tc := range []struct {
		site, origin string
		token        bool
		want         int
	}{
		{"same-origin", "https://nas.test:8002", true, 200},
		{"same-origin", "https://nas.test:8002", false, 403},
		{"cross-site", "https://evil.test", true, 403},
		{"same-site", "https://evil.nas.test", true, 403},
		{"", "https://evil.test", true, 403},
		{"same-origin", "null", true, 403},
		{"same-origin", "https://nas.test/path", true, 403},
	} {
		r := httptest.NewRequest("PUT", "http://localhost/app/backup_center/api/settings", bytes.NewBufferString(`{"retain":20}`))
		r = r.WithContext(context.WithValue(r.Context(), gatewayKey{}, true))
		r.Header.Set("X-Trim-Isadmin", "true")
		r.Header.Set("X-Trim-Userid", "1000")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Sec-Fetch-Site", tc.site)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("X-Forwarded-Host", "evil.test") // Never trust caller-provided forwarding headers.
		if tc.token {
			r.Header.Set("X-Backup-CSRF", s.csrf)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("site=%s origin=%s token=%t: %d, want %d", tc.site, tc.origin, tc.token, w.Code, tc.want)
		}
	}
}

func TestHTTPGatewayOriginFallsBackToMatchingAppReferrer(t *testing.T) {
	s, _ := fixture(t)
	for _, tc := range []struct {
		referrer, site string
		want           int
	}{
		{"http://nas.test:8001/app/backup_center/", "", 200},
		{"http://other.test/app/backup_center/", "", 403},
		{"http://nas.test:8001/unrelated", "", 403},
		{"http://nas.test:8001/app/backup_center/", "cross-site", 403},
		{"http://nas.test:8001/app/backup_center/", "same-site", 403},
		{"", "", 403},
	} {
		r := httptest.NewRequest("PUT", "http://localhost/app/backup_center/api/settings", bytes.NewBufferString(`{"retain":20}`))
		r = r.WithContext(context.WithValue(r.Context(), gatewayKey{}, true))
		r.Header.Set("X-Trim-Isadmin", "true")
		r.Header.Set("X-Trim-Userid", "1000")
		r.Header.Set("X-Backup-CSRF", s.csrf)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://nas.test:8001")
		r.Header.Set("Referer", tc.referrer)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("referrer=%q site=%s: %d want %d", tc.referrer, tc.site, w.Code, tc.want)
		}
	}
}

func TestCreatePreviewBackupAndUnauthorizedPath(t *testing.T) {
	s, selection := fixture(t)
	w := req(t, s, "POST", "/api/tasks", tasks.Task{Name: "test", Selection: selection, Schedule: tasks.Schedule{Enabled: true, Kind: "interval", Minutes: 60, Zone: "UTC"}}, true, true, true)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var task tasks.Task
	if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if !task.Schedule.Enabled || task.Next.IsZero() || task.Selection.RepoID == "" || task.Selection.Mode != backup.ModeDirect {
		t.Fatal(task)
	}
	for _, kind := range []string{"preview", "backup"} {
		w = req(t, s, "POST", "/api/tasks/"+task.ID+"/run", map[string]string{"kind": kind}, true, true, true)
		if w.Code != 202 {
			t.Fatal(w.Body.String())
		}
		if err := s.Manager.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if s.Manager.State().Runs[0].Status != "pending" {
		t.Fatal("incorrect backup status")
	}
	w = req(t, s, "GET", "/api/tasks/"+task.ID+"/snapshots", nil, true, true, false)
	if w.Code != 400 {
		t.Fatal("direct copy unexpectedly exposed version snapshots", w.Body.String())
	}
	parent := s.options.LocalRoots[1]
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	w = req(t, s, "POST", "/api/tasks/"+task.ID+"/run", map[string]string{
		"kind": "restore", "snapshot_id": backup.NewID(), "destination": filepath.Join(parent, "restore"),
	}, true, true, true)
	if w.Code != 400 {
		t.Fatal("direct copy unexpectedly allowed snapshot restore", w.Body.String())
	}
	selection.Source = filepath.Dir(selection.Source)
	w = req(t, s, "POST", "/api/tasks", tasks.Task{Name: "escape", Selection: selection}, true, true, true)
	if w.Code != 400 {
		t.Fatal("unauthorized source accepted")
	}
	w = req(t, s, "GET", "/api/directories?kind=source&path=/etc", nil, true, true, false)
	if w.Code != 400 {
		t.Fatal("unauthorized browsing accepted")
	}
	w = req(t, s, "GET", "/api/directories?kind=source", nil, true, true, false)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = req(t, s, "GET", "/api/directories?kind=target", nil, true, true, false)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}

func TestLiveAuthorizationUpdatesWithoutRestart(t *testing.T) {
	s, sel := fixture(t)
	s.options.LocalRoots = nil
	allowed := true
	s.options.Grants = func(context.Context) ([]string, error) {
		if allowed {
			return []string{sel.Source}, nil
		}
		return nil, nil
	}
	w := req(t, s, "POST", "/api/tasks", tasks.Task{Name: "authorized", Selection: sel}, true, true, true)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	allowed = false
	w = req(t, s, "GET", "/api/directories?kind=source&path="+sel.Source, nil, true, true, false)
	if w.Code != 400 {
		t.Fatal("revoked authorization remained usable")
	}
}
