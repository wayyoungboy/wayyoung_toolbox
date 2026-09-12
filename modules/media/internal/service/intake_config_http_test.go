package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func intakeConfigRequest(t *testing.T, s *Service, ctx context.Context, cfg IntakeConfig) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPut, "/api/intake/config", bytes.NewReader(body)).WithContext(ctx)
	r.Header.Set("X-Trim-Isadmin", "true")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestIntakeActionHTTPRequiresCurrentRevisionBeforeSideEffects(t *testing.T) {
	for _, action := range []string{"preview", "run"} {
		for _, scenario := range []string{"missing", "null", "negative", "fractional", "stale", "current"} {
			t.Run(action+"/"+scenario, func(t *testing.T) {
				f := newIntakeQueueFixture(t)
				config, err := f.s.db.IntakeConfig()
				if err != nil {
					t.Fatal(err)
				}
				body := fmt.Sprintf(`{"revision":%d}`, config.Revision)
				want := http.StatusBadRequest
				switch scenario {
				case "missing":
					body = `{}`
				case "null":
					body = `{"revision":null}`
				case "negative":
					body = `{"revision":-1}`
				case "fractional":
					body = `{"revision":1.5}`
				case "stale":
					config.DestinationDirectory = t.TempDir()
					if _, err := f.s.db.UpdateIntakeConfig(config); err != nil {
						t.Fatal(err)
					}
					want = http.StatusConflict
				case "current":
					want = http.StatusOK
				}
				before, err := f.s.db.allSettings()
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest(http.MethodPost, "/api/intake/"+action, strings.NewReader(body))
				r.Header.Set("X-Trim-Isadmin", "true")
				w := httptest.NewRecorder()
				f.s.Handler().ServeHTTP(w, r)
				if w.Code != want {
					t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
				}
				if scenario != "current" || action == "preview" {
					if f.stops.Load() != 0 || f.deletes.Load() != 0 {
						t.Fatal("unconfirmed action changed qB")
					}
					after, err := f.s.db.allSettings()
					if err != nil || !reflect.DeepEqual(before, after) {
						t.Fatal("unconfirmed action changed settings")
					}
					jobs, err := f.s.db.ListMediaJobs("", 10)
					if err != nil || len(jobs) != 0 {
						t.Fatal("unconfirmed action created media jobs")
					}
					runs, err := f.s.db.ListMaintenanceRunsByKinds(20, "intake")
					if err != nil || len(runs) != 0 {
						t.Fatal("unconfirmed action started an intake run")
					}
				} else if f.deletes.Load() != 1 {
					t.Fatal("confirmed action did not process download")
				}
			})
		}
	}
}

func TestIntakeConfigHTTPRejectsStaleAndCanceledSaves(t *testing.T) {
	for _, scenario := range []string{"stale", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			s, paths := setupMediaPathSettings(t)
			old, err := s.db.IntakeConfig()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := http.StatusBadRequest
			if scenario == "stale" {
				paths.DownloadDirectory = t.TempDir()
				if _, err := s.db.UpdateMediaPathSettings(ctx, paths); err != nil {
					t.Fatal(err)
				}
				want = http.StatusConflict
			} else {
				cancel()
			}
			before, err := s.db.allSettings()
			if err != nil {
				t.Fatal(err)
			}
			old.RunJavsp = !old.RunJavsp
			w := intakeConfigRequest(t, s, ctx, old)
			if w.Code != want {
				t.Errorf("save returned %d, want %d: %s", w.Code, want, w.Body.String())
			}
			after, err := s.db.allSettings()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("rejected save changed settings")
			}
		})
	}
}

func TestIntakeActionKeepsConfirmedPathsAcrossConcurrentSettingsUpdate(t *testing.T) {
	f := newIntakeQueueFixture(t)
	config, err := f.s.db.IntakeConfig()
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	f.onLogin = func() { close(entered); <-release }
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/intake/run", strings.NewReader(fmt.Sprintf(`{"revision":%d}`, config.Revision)))
	r.Header.Set("X-Trim-Isadmin", "true")
	done := make(chan struct{})
	go func() { defer close(done); f.s.Handler().ServeHTTP(w, r) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("action did not reach qB")
	}
	changed := config
	changed.DestinationDirectory = t.TempDir()
	if _, err := f.s.db.UpdateIntakeConfig(changed); err != nil {
		t.Fatal(err)
	}
	unblock.Do(func() { close(release) })
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("action did not finish")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("action failed: %s", w.Body.String())
	}
	var result IntakeRunResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Moved != 1 || len(result.Candidates) != 1 || result.Candidates[0].Destination != filepath.Join(config.DestinationDirectory, "TEST-001.mp4") {
		t.Fatalf("accepted target was replaced: %+v", result)
	}
	assertIntakeFixture(t, config.DestinationDirectory, map[string]string{"TEST-001.mp4": "video", ".placeholder": ""})
}

func TestIntakeActionCanceledWhileWaitingForDatabaseHasNoSideEffects(t *testing.T) {
	for _, action := range []string{"preview", "run"} {
		t.Run(action, func(t *testing.T) {
			f := newIntakeQueueFixture(t)
			config, err := f.s.db.IntakeConfig()
			if err != nil {
				t.Fatal(err)
			}
			conn, err := f.s.db.db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			r := httptest.NewRequest(http.MethodPost, "/api/intake/"+action, strings.NewReader(fmt.Sprintf(`{"revision":%d}`, config.Revision))).WithContext(ctx)
			r.Header.Set("X-Trim-Isadmin", "true")
			w, done := httptest.NewRecorder(), make(chan struct{})
			go func() { defer close(done); f.s.Handler().ServeHTTP(w, r) }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				conn.Close()
				<-done
				t.Fatal("canceled action kept waiting")
			}
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status=%d: %s", w.Code, w.Body.String())
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			after, err := f.s.db.IntakeConfig()
			if err != nil || !reflect.DeepEqual(config, after) || f.stops.Load() != 0 || f.deletes.Load() != 0 {
				t.Fatal("canceled action had side effects")
			}
		})
	}
}
