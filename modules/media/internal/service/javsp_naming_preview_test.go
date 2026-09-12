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
	"strings"
	"testing"

	javspengine "github.com/yuukiy/javsp-go/engine"
)

func namingPreviewRequest(t *testing.T, s *Service, cfg JavspConfig, sample map[string]any, admin bool) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"config": cfg, "sample": sample})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/javsp/naming-preview", bytes.NewReader(body))
	if admin {
		r.Header.Set("X-Trim-Isadmin", "true")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestJavspNamingPreviewIsReadOnlyAndUsesUnsavedConfig(t *testing.T) {
	s := testService(t)
	bundle, data := javspTestRuntime(t)
	engine := &fakeEmbeddedJavsp{complete: true}
	s.javsp = newJavspRunnerWithEngine(bundle, data, engine)
	cfg := defaultJavspConfig()
	cfg.InputDirectory = t.TempDir()
	cfg.OutputDirectory = t.TempDir()
	cfg.OutputFolderPattern = "{year}/{num} {title}"
	cfg.PathLengthMaximum = 4096
	if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	before, err := s.db.JavspConfigPrivate()
	if err != nil {
		t.Fatal(err)
	}
	cfg.OutputFolderPattern = "draft/{num}"
	cfg.TranslatorAPIKey = "preview-secret-not-real"
	s.javspLock.Lock()
	defer s.javspLock.Unlock()
	sample := map[string]any{"media_id": "TEST-001", "title": "示例标题", "actress": []string{"示例演员"}, "publish_date": "2026-01-02", "filenames": []string{"TEST-001-C.mp4"}}
	w := namingPreviewRequest(t, s, cfg, sample, true)
	if w.Code != 200 {
		t.Fatalf("preview unavailable: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Plan        javspengine.MoviePlan `json:"plan"`
		SameNameNFO []string              `json:"same_name_nfo"`
		NFOTitle    string                `json:"nfo_title"`
		Warnings    []string              `json:"warnings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	output, _ := canonicalDirectory(cfg.OutputDirectory)
	want := filepath.Join(output, "draft", "TEST-001-C", "TEST-001-C.mp4")
	if len(got.Plan.Files) != 1 || got.Plan.Files[0].Destination != want || len(got.SameNameNFO) != 1 || got.SameNameNFO[0] != want[:len(want)-4]+".nfo" || got.NFOTitle != "TEST-001 示例标题" {
		t.Fatalf("wrong preview: %+v", got)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("preview-secret")) {
		t.Fatal("secret leaked")
	}
	after, err := s.db.JavspConfigPrivate()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("preview saved draft")
	}
	for _, root := range []string{cfg.InputDirectory, cfg.OutputDirectory} {
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			t.Fatal("preview touched media")
		}
	}
	if _, err := os.Stat(s.javsp.workRoot); !os.IsNotExist(err) {
		t.Fatal("preview prepared runtime")
	}
	jobs, err := s.db.ListMediaJobs("", 10)
	if err != nil || len(jobs) != 0 {
		t.Fatal("preview queued jobs")
	}
	runs, err := s.db.ListJavspRuns(10)
	if err != nil || len(runs) != 0 || engine.runCount != 0 {
		t.Fatal("preview executed organizer")
	}
}
func TestJavspNamingPreviewRejectsUnsafeSamplesAndUnauthorized(t *testing.T) {
	s := testService(t)
	bundle, data := javspTestRuntime(t)
	s.javsp = NewJavspRunner(bundle, data)
	cfg := defaultJavspConfig()
	cfg.InputDirectory = t.TempDir()
	cfg.OutputDirectory = t.TempDir()
	for _, name := range []string{"../outside.mp4", "/outside.mp4", "nested/file.mp4", ".", "movie.nfo", "a\x00.mp4"} {
		w := namingPreviewRequest(t, s, cfg, map[string]any{"media_id": "TEST-001", "filenames": []string{name}}, true)
		if w.Code != 400 {
			t.Errorf("unsafe sample accepted %q: %d", name, w.Code)
		}
	}
	w := namingPreviewRequest(t, s, cfg, map[string]any{"media_id": "TEST-001", "filenames": []string{"TEST-001.mp4"}}, false)
	if w.Code != 403 {
		t.Fatalf("preview not admin protected: %d", w.Code)
	}
}

func TestJavspNamingPreviewDoesNotRequireTranslatorCredentials(t *testing.T) {
	for _, engineName := range []string{"deepl", "bing", "claude", "baidu", "openai"} {
		t.Run(engineName, func(t *testing.T) {
			s := testService(t)
			bundle, data := javspTestRuntime(t)
			runner := &fakeEmbeddedJavsp{complete: true}
			s.javsp = newJavspRunnerWithEngine(bundle, data, runner)
			cfg := defaultJavspConfig()
			cfg.InputDirectory, cfg.OutputDirectory = t.TempDir(), t.TempDir()
			cfg.OutputFolderPattern = "{num}"
			cfg.TranslatorEngine, cfg.TranslatorAPIKeySet = engineName, true
			cfg.TranslatorAPIKey = "stored-synthetic-secret"
			cfg.TranslatorAppID, cfg.TranslatorModel = "synthetic-app", "synthetic-model"
			cfg.TranslatorURL = "https://translation.invalid/v1"
			if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
				t.Fatal(err)
			}
			before, err := s.db.JavspConfigPrivate()
			if err != nil {
				t.Fatal(err)
			}
			// The browser receives only the credential-set flag; preview must not
			// need to fetch stored private configuration or contact the provider.
			cfg.TranslatorAPIKey = ""
			w := namingPreviewRequest(t, s, cfg, map[string]any{"media_id": "TEST-001", "title": "User supplied", "filenames": []string{"TEST-001.mp4"}}, true)
			if w.Code != http.StatusOK {
				t.Fatalf("credential-free preview: %d %s", w.Code, w.Body.String())
			}
			var got struct {
				NFOTitle string `json:"nfo_title"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.NFOTitle != "TEST-001 User supplied" {
				t.Fatalf("preview translated sample: %+v %v", got, err)
			}
			if bytes.Contains(w.Body.Bytes(), []byte("secret")) {
				t.Fatal("preview exposed credentials")
			}
			after, err := s.db.JavspConfigPrivate()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("preview changed saved translator")
			}
			if runner.runCount != 0 {
				t.Fatal("preview ran organizer")
			}
		})
	}
}
func TestJavspNamingPreviewReportsIntakeMismatch(t *testing.T) {
	s := testService(t)
	bundle, data := javspTestRuntime(t)
	s.javsp = NewJavspRunner(bundle, data)
	cfg := defaultJavspConfig()
	cfg.InputDirectory = t.TempDir()
	cfg.OutputDirectory = t.TempDir()
	intake := defaultIntakeConfig()
	intake.RunJavsp = true
	intake.SourceDirectory = t.TempDir()
	intake.DestinationDirectory = t.TempDir()
	if _, err := s.db.UpdateIntakeConfig(intake); err != nil {
		t.Fatal(err)
	}
	sample := map[string]any{"media_id": "TEST-001", "title": "Example", "filenames": []string{"TEST-001.mp4"}}
	w := namingPreviewRequest(t, s, cfg, sample, true)
	if w.Code != 200 {
		t.Fatalf("preview failed: %s", w.Body.String())
	}
	var got struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Warnings) == 0 {
		t.Fatal("disconnected intake destination was not reported")
	}
	cfg.InputDirectory = intake.DestinationDirectory
	w = namingPreviewRequest(t, s, cfg, sample, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("correct linkage still warns: %+v", got)
	}
}

func TestJavspOutputDirectoryBrowserUsesSelectedRoot(t *testing.T) {
	s := testService(t)
	cfg := defaultJavspConfig()
	cfg.InputDirectory, cfg.OutputDirectory = t.TempDir(), t.TempDir()
	if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	want, err := canonicalDirectory(cfg.OutputDirectory)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/javsp/directories?target=output", nil)
	r.Header.Set("X-Trim-Isadmin", "true")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var listing directoryListing
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil || w.Code != 200 || listing.Path != want {
		t.Fatalf("output browser opened input instead: %+v %v", listing, err)
	}
}

func TestJavspNamingPreviewWarnsForExcludedIntakeDestination(t *testing.T) {
	for _, tc := range []struct {
		name, destination               string
		legacy, inPlace, disableHandoff bool
		wantExcluded                    bool
	}{
		{name: "output root", destination: "complete", wantExcluded: true},
		{name: "output descendant", destination: "complete/pending", wantExcluded: true},
		{name: "legacy output root", destination: "complete", legacy: true, wantExcluded: true},
		{name: "legacy output descendant", destination: "complete/pending", legacy: true, wantExcluded: true},
		{name: "prefix sibling", destination: "complete-other"},
		{name: "in place", destination: "complete", inPlace: true},
		{name: "handoff disabled", destination: "complete", disableHandoff: true, wantExcluded: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testService(t)
			bundle, data := javspTestRuntime(t)
			s.javsp = NewJavspRunner(bundle, data)
			cfg := defaultJavspConfig()
			cfg.InputDirectory, cfg.MinimumSize = t.TempDir(), "0B"
			cfg.OutputDirectory = filepath.Join(cfg.InputDirectory, "complete")
			cfg.OutputFolderPattern, cfg.IgnoredFolderPatterns = "{num}", nil
			cfg.MoveFiles = !tc.inPlace
			if tc.legacy {
				cfg.OutputDirectory, cfg.OutputFolderPattern = "", "complete/{num}"
			}
			fixture := map[string]string{filepath.Join(tc.destination, "TEST-001.mp4"): "synthetic video"}
			writeIntakeFixture(t, cfg.InputDirectory, fixture)
			if err := os.MkdirAll(filepath.Join(cfg.InputDirectory, "complete"), 0700); err != nil {
				t.Fatal(err)
			}
			cfg, err := validateJavspConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
				t.Fatal(err)
			}
			intake := defaultIntakeConfig()
			intake.SourceDirectory = t.TempDir()
			intake.DestinationDirectory = filepath.Join(cfg.InputDirectory, tc.destination)
			intake.RunJavsp = !tc.disableHandoff
			if _, err := s.db.UpdateIntakeConfig(intake); err != nil {
				t.Fatal(err)
			}

			// Compare the read-only preview with real explicit-file discovery, not
			// a duplicate of the exclusion calculation or a mocked scanner.
			w := namingPreviewRequest(t, s, cfg, map[string]any{"media_id": "TEST-001", "title": "Example", "filenames": []string{"TEST-001.mp4"}}, true)
			if w.Code != http.StatusOK {
				t.Fatalf("preview: %d %s", w.Code, w.Body.String())
			}
			var got struct {
				Warnings []string `json:"warnings"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if want := tc.wantExcluded && intake.RunJavsp; (len(got.Warnings) != 0) != want {
				t.Errorf("intake exclusion warning = %v, want warning %v", got.Warnings, want)
			}
			jobs, err := s.discoverIntakeMovies(context.Background(), MediaJob{ID: 12, IntakeTargetRoot: intake.DestinationDirectory, PlannedPaths: []string{filepath.Join(intake.DestinationDirectory, "TEST-001.mp4")}})
			if tc.wantExcluded {
				if err == nil || !strings.Contains(err.Error(), "整理完成目录") || len(jobs) != 0 {
					t.Fatalf("expected real discovery exclusion, got %v %v", jobs, err)
				}
			} else if err != nil || len(jobs) != 1 || jobs[0].MediaID != "TEST-001" {
				t.Fatalf("valid intake could not discover: %v %v", jobs, err)
			}
			assertIntakeFixture(t, cfg.InputDirectory, fixture)
		})
	}
}
