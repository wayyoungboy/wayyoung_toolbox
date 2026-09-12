package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fnos-backup-center/internal/backup"
	"fnos-backup-center/internal/tasks"
)

func TestFullAPIBackupRestoreImportRebindAndDelete(t *testing.T) {
	s, selection := fixture(t)
	create := req(t, s, "POST", "/api/tasks", tasks.Task{Name: "API backup", Selection: selection}, true, true, true)
	if create.Code != 201 {
		t.Fatal(create.Body.String())
	}
	var task tasks.Task
	if err := json.Unmarshal(create.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"/api/mounts", "/api/directories?kind=target&path=" + selection.Target, "/api/directories?kind=source&path=" + selection.Source} {
		if w := req(t, s, "GET", endpoint, nil, true, true, false); w.Code != 200 {
			t.Fatal(endpoint, w.Body.String())
		}
	}
	if w := req(t, s, "POST", "/api/tasks/"+task.ID+"/run", map[string]string{"kind": "backup"}, true, true, true); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	if err := s.Manager.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := req(t, s, "GET", "/api/tasks/"+task.ID+"/snapshots", nil, true, true, false)
	var snapshots []backup.Manifest
	if err := json.Unmarshal(w.Body.Bytes(), &snapshots); err != nil || len(snapshots) != 1 {
		t.Fatal(w.Body.String(), err)
	}
	parent := s.options.LocalRoots[1]
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(parent, "restored")
	w = req(t, s, "POST", "/api/tasks/"+task.ID+"/run", map[string]string{"kind": "restore", "snapshot_id": snapshots[0].ID, "destination": dest}, true, true, true)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	if err := s.Manager.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Manager.State().Runs[0].Status != "restored" {
		t.Fatal(s.Manager.State().Runs[0])
	}
	if data, err := os.ReadFile(filepath.Join(dest, "a.txt")); err != nil || string(data) != "fixture" {
		t.Fatal(err)
	}
	w = req(t, s, "POST", "/api/import", map[string]string{"name": "readonly", "target": task.Selection.Target, "repo_id": task.Selection.RepoID}, true, true, true)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var imported tasks.Task
	if err := json.Unmarshal(w.Body.Bytes(), &imported); err != nil || !imported.ReadOnly {
		t.Fatal(w.Body.String(), err)
	}
	if w := req(t, s, "POST", "/api/tasks/"+imported.ID+"/run", map[string]string{"kind": "backup"}, true, true, true); w.Code < 400 {
		t.Fatal("read-only task wrote")
	}
	w = req(t, s, "POST", "/api/tasks/"+task.ID+"/rebind", map[string]string{"target": task.Selection.Target}, true, true, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if task.Schedule.Enabled {
		t.Fatal("rebind enabled schedule")
	}
	task.Name = "Renamed"
	if w := req(t, s, "PUT", "/api/tasks/"+task.ID, task, true, true, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := req(t, s, "DELETE", "/api/tasks/"+task.ID, map[string]any{}, true, true, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(selection.Target, backup.RepositoryPath(task.Selection.RepoID), "repository.json")); err != nil {
		t.Fatal("deleting config removed backup", err)
	}
}

func TestInvalidInputsLimitsAndMissingTasks(t *testing.T) {
	s, selection := fixture(t)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/tasks", map[string]string{"unknown": "field"}},
		{"POST", "/api/import", map[string]string{"name": "bad", "target": selection.Target, "repo_id": backup.NewID()}},
		{"POST", "/api/import", map[string]string{"name": "bad", "target": "/etc", "repo_id": backup.NewID()}},
		{"POST", "/api/tasks/missing/rebind", map[string]string{"target": selection.Target}},
		{"POST", "/api/tasks/missing/run", map[string]string{"kind": "backup"}},
		{"GET", "/api/tasks/missing/snapshots", nil},
		{"POST", "/api/runs/missing/cancel", map[string]any{}},
		{"PUT", "/api/settings", map[string]string{"retain": "not-number"}},
		{"GET", "/api/directories?kind=bad", nil},
		{"GET", "/api/directories?kind=target&path=/etc", nil},
	} {
		if w := req(t, s, tc.method, tc.path, tc.body, true, true, true); w.Code < 400 {
			t.Fatal("accepted", tc.path, w.Body.String())
		}
	}
	for _, body := range []string{`{"retain":10} {"retain":20}`, strings.Repeat(" ", 1<<20) + `{"retain":20}`} {
		r := httptest.NewRequest("PUT", "http://nas.test"+Prefix+"/api/settings", strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), gatewayKey{}, true))
		r.Header.Set("X-Trim-Isadmin", "true")
		r.Header.Set("X-Trim-Userid", "1000")
		r.Header.Set("X-Backup-CSRF", s.csrf)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code < 400 {
			t.Fatal("body limit or trailing JSON accepted")
		}
	}
	for range 4 {
		s.metadata <- struct{}{}
	}
	if w := req(t, s, "GET", "/api/directories?kind=source", nil, true, true, false); w.Code != 429 {
		t.Fatal("metadata concurrency unbounded")
	}
	for range 4 {
		<-s.metadata
	}
	s.options.Grants = func(context.Context) ([]string, error) { return nil, errors.New("grant service unavailable") }
	if w := req(t, s, "GET", "/api/directories?kind=source", nil, true, true, false); w.Code < 400 {
		t.Fatal("failed grants accepted")
	}
}

func TestWrongMediaTypeCrossSiteAndUntrustedConnection(t *testing.T) {
	s, _ := fixture(t)
	for _, tc := range []struct {
		media, site string
		want        int
	}{{"text/plain", "", 415}, {"application/json", "cross-site", 403}} {
		r := httptest.NewRequest("PUT", "http://nas.test"+Prefix+"/api/settings", bytes.NewBufferString(`{"retain":10}`))
		r = r.WithContext(context.WithValue(r.Context(), gatewayKey{}, true))
		r.Header.Set("X-Trim-Isadmin", "true")
		r.Header.Set("X-Trim-Userid", "1000")
		r.Header.Set("X-Backup-CSRF", s.csrf)
		r.Header.Set("Content-Type", tc.media)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if GatewayConnection(context.Background(), a).Value(gatewayKey{}) != false {
		t.Fatal("non-Unix peer trusted")
	}
}
