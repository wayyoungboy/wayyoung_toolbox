package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func javspConfigRequest(t *testing.T, s *Service, ctx context.Context, config JavspConfig) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPut, "/api/javsp/config", bytes.NewReader(body)).WithContext(ctx)
	r.Header.Set("X-Trim-Isadmin", "true")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestJavspConfigSaveThenDiscoverWhileOrganizerBusy(t *testing.T) {
	s := testService(t)
	bundle, data := javspTestRuntime(t)
	engine := &fakeEmbeddedJavsp{complete: true}
	s.javsp = newJavspRunnerWithEngine(bundle, data, engine)
	cfg := defaultJavspConfig()
	cfg.InputDirectory, cfg.MinimumSize = t.TempDir(), "0B"
	cfg.TranslatorEngine, cfg.TranslatorAPIKey = "deepl", "test-secret-not-a-real-key"
	writeIntakeFixture(t, cfg.InputDirectory, map[string]string{"TEST-001.mp4": "video"})
	if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	cfg = readJavspHTTPConfig(t, s)
	s.javspLock.Lock()
	defer s.javspLock.Unlock()
	s.mediaFiles.Lock()
	defer s.mediaFiles.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.runCtx = ctx
	defer s.wait.Wait()

	// Match the browser: persist the form before requesting discovery.
	cfg.NetworkRetry = 9
	cfg.TranslatorAPIKey = "__UNCHANGED__"
	w := javspConfigRequest(t, s, ctx, cfg)
	if w.Code != http.StatusOK {
		t.Fatalf("save before scan rejected while organizing: %d %s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("test-secret-not-a-real-key")) {
		t.Fatal("configuration response exposed the translator secret")
	}
	stored, err := s.db.JavspConfigPrivate()
	if err != nil || stored.NetworkRetry != 9 || stored.TranslatorAPIKey != "test-secret-not-a-real-key" {
		t.Fatalf("new settings or existing secret not preserved: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/javsp/run", bytes.NewBufferString(`{}`))
	r.Header.Set("X-Trim-Isadmin", "true")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("discovery rejected: %d %s", w.Code, w.Body.String())
	}
	s.wait.Wait()
	jobs, err := s.db.ListMediaJobs("", 10)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "pending" || jobs[0].MediaID != "TEST-001" {
		t.Fatalf("discovery did not enqueue the film: %+v %v", jobs, err)
	}
	if engine.runCount != 0 {
		t.Fatal("saving/scanning invoked the organizer")
	}
	if _, err := os.Stat(s.javsp.workRoot); !os.IsNotExist(err) {
		t.Fatalf("save/discovery wrote runtime assets: %v", err)
	}
	assertIntakeFixture(t, cfg.InputDirectory, map[string]string{"TEST-001.mp4": "video"})
}

func TestJavspConfigSaveDoesNotChangeActiveEngine(t *testing.T) {
	s := testService(t)
	bundle, data := javspTestRuntime(t)
	engine := &fakeEmbeddedJavsp{started: make(chan struct{})}
	s.javsp = newJavspRunnerWithEngine(bundle, data, engine)
	cfg := defaultJavspConfig()
	cfg.InputDirectory = t.TempDir()
	if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan struct{})
	s.javspLock.Lock()
	defer s.javspLock.Unlock()
	go func() {
		defer close(done)
		_, _ = s.javsp.RunSelected(ctx, cfg, []string{filepath.Join(cfg.InputDirectory, "TEST-001.mp4")}, JavspExecutionHooks{})
	}()
	defer func() { cancel(); <-done }()
	select {
	case <-engine.started:
	case <-ctx.Done():
		t.Fatal("organizer did not start")
	}
	engine.mu.Lock()
	snapshot := bytes.Clone(engine.options.ConfigData)
	engine.mu.Unlock()
	// A config save must not refresh assets used by the running engine, even
	// if the on-disk bundle marker differs.
	marker := filepath.Join(s.javsp.workRoot, ".bundle-version")
	if err := os.WriteFile(marker, []byte("running-version\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg = readJavspHTTPConfig(t, s)
	cfg.NetworkRetry = 12
	w := javspConfigRequest(t, s, ctx, cfg)
	if w.Code != http.StatusOK {
		t.Fatalf("save rejected: %d %s", w.Code, w.Body.String())
	}
	engine.mu.Lock()
	unchanged := bytes.Equal(snapshot, engine.options.ConfigData) && engine.runCount == 1
	engine.mu.Unlock()
	if !unchanged {
		t.Fatal("save replaced or restarted the active engine configuration")
	}
	if current, err := os.ReadFile(marker); err != nil || string(current) != "running-version\n" {
		t.Fatalf("save refreshed runtime assets: %q %v", current, err)
	}
	select {
	case <-done:
		t.Fatal("save canceled the active organizer")
	default:
	}
}

func TestJavspConfigSaveIsReadOnlyUntilValidated(t *testing.T) {
	for _, scenario := range []string{"valid", "invalid", "missing_template", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			s := testService(t)
			bundle, data := javspTestRuntime(t)
			s.javsp = NewJavspRunner(bundle, data)
			cfg := defaultJavspConfig()
			cfg.InputDirectory = t.TempDir()
			if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
				t.Fatal(err)
			}
			before, err := s.db.JavspConfigPrivate()
			if err != nil {
				t.Fatal(err)
			}
			cfg = before
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg.NetworkRetry = 9
			want := http.StatusBadRequest
			switch scenario {
			case "valid":
				want = http.StatusOK
			case "invalid":
				cfg.NetworkTimeout = "invalid"
			case "missing_template":
				if err := os.Remove(filepath.Join(bundle, "runtime", "config.yml")); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				cancel()
			}
			w := javspConfigRequest(t, s, ctx, cfg)
			if w.Code != want {
				t.Fatalf("save status=%d want=%d: %s", w.Code, want, w.Body.String())
			}
			after, err := s.db.JavspConfigPrivate()
			if err != nil {
				t.Fatal(err)
			}
			if want != http.StatusOK && !reflect.DeepEqual(before, after) {
				t.Fatal("rejected save changed persisted settings")
			}
			if _, err := os.Stat(s.javsp.workRoot); !os.IsNotExist(err) {
				t.Fatalf("save wrote runtime assets: %v", err)
			}
		})
	}
}

func readJavspHTTPConfig(t *testing.T, s *Service) JavspConfig {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/javsp", nil)
	r.Header.Set("X-Trim-Isadmin", "true")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var result struct{ Config JavspConfig }
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK {
		t.Fatalf("read config failed: %d %v", w.Code, err)
	}
	return result.Config
}

func TestJavspConfigSaveRejectsConcurrentStaleForms(t *testing.T) {
	s := testService(t)
	bundle, data := javspTestRuntime(t)
	s.javsp = NewJavspRunner(bundle, data)
	cfg := defaultJavspConfig()
	cfg.InputDirectory = t.TempDir()
	if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	base := readJavspHTTPConfig(t, s)
	responses := make([]*httptest.ResponseRecorder, 2)
	var workers sync.WaitGroup
	start := make(chan struct{})
	for index := range responses {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			candidate := base
			candidate.NetworkRetry = 10 + index
			responses[index] = javspConfigRequest(t, s, context.Background(), candidate)
		}()
	}
	close(start)
	workers.Wait()
	winner := -1
	for index, response := range responses {
		switch response.Code {
		case http.StatusOK:
			if winner != -1 {
				t.Fatal("both stale forms succeeded; the first save was silently overwritten")
			}
			winner = index
		case http.StatusConflict:
		default:
			t.Fatalf("save failed unexpectedly: %d %s", response.Code, response.Body.String())
		}
	}
	if winner == -1 {
		t.Fatal("neither concurrent save succeeded")
	}
	after := readJavspHTTPConfig(t, s)
	if after.NetworkRetry != 10+winner || after.Revision != base.Revision+1 {
		t.Fatal("persisted config and revision do not match the winning save")
	}
	w := javspConfigRequest(t, s, context.Background(), base)
	if w.Code != http.StatusConflict {
		t.Fatalf("stale replay accepted: %d", w.Code)
	}
	if final := readJavspHTTPConfig(t, s); !reflect.DeepEqual(after, final) {
		t.Fatal("stale save changed configuration")
	}
}

func TestJavspConfigSavePreservesRunTimes(t *testing.T) {
	s := testService(t)
	bundle, data := javspTestRuntime(t)
	s.javsp = NewJavspRunner(bundle, data)
	cfg := defaultJavspConfig()
	cfg.InputDirectory, cfg.Enabled = t.TempDir(), true
	cfg.ScheduleType, cfg.ScheduleMinutes = "interval", 60
	if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	form := readJavspHTTPConfig(t, s)
	if err := s.db.MarkJavspStarted(); err != nil {
		t.Fatal(err)
	}
	// Use a distinct future due time so the test cannot accidentally pass
	// because save and start happened during the same clock second.
	next := time.Now().Add(37 * time.Minute).UTC().Format(time.RFC3339)
	if _, err := s.db.db.Exec("UPDATE settings SET value=? WHERE key='javsp_next_run_at'", encodeJSON(next)); err != nil {
		t.Fatal(err)
	}
	live := readJavspHTTPConfig(t, s)
	form.NetworkRetry = 9
	w := javspConfigRequest(t, s, context.Background(), form)
	if w.Code != http.StatusOK {
		t.Fatalf("run bookkeeping invalidated a config form: %d %s", w.Code, w.Body.String())
	}
	after := readJavspHTTPConfig(t, s)
	if !reflect.DeepEqual(after.LastRunAt, live.LastRunAt) || !reflect.DeepEqual(after.NextRunAt, live.NextRunAt) {
		t.Fatal("saving unrelated settings erased the latest run or postponed the next schedule")
	}
	after.ScheduleMinutes += 30
	w = javspConfigRequest(t, s, context.Background(), after)
	if w.Code != http.StatusOK {
		t.Fatalf("schedule change failed: %d %s", w.Code, w.Body.String())
	}
	changed := readJavspHTTPConfig(t, s)
	if !reflect.DeepEqual(changed.LastRunAt, live.LastRunAt) || reflect.DeepEqual(changed.NextRunAt, live.NextRunAt) {
		t.Fatal("schedule change did not update due time while preserving last run")
	}
}

func TestJavspConfigSaveRollsBackAllFieldsAndRevision(t *testing.T) {
	s := testService(t)
	bundle, data := javspTestRuntime(t)
	s.javsp = NewJavspRunner(bundle, data)
	cfg := defaultJavspConfig()
	cfg.InputDirectory = t.TempDir()
	if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	before := readJavspHTTPConfig(t, s)
	if _, err := s.db.db.Exec(`CREATE TRIGGER reject_javsp_save BEFORE UPDATE ON settings
WHEN NEW.key='javsp_network_retry' BEGIN SELECT RAISE(ABORT, 'test write failure'); END`); err != nil {
		t.Fatal(err)
	}
	cfg = before
	cfg.NetworkRetry, cfg.ScheduleMinutes = 9, 90
	w := javspConfigRequest(t, s, context.Background(), cfg)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("failed save returned %d", w.Code)
	}
	if after := readJavspHTTPConfig(t, s); !reflect.DeepEqual(before, after) {
		t.Fatal("failed save partially committed settings or advanced the revision")
	}
}
