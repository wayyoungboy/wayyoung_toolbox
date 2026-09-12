package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	javspengine "github.com/yuukiy/javsp-go/engine"
)

func mediaPathRequest(t *testing.T, s *Service, ctx context.Context, method, path string, payload any, admin bool) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(body)).WithContext(ctx)
	if admin {
		r.Header.Set("X-Trim-Isadmin", "true")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

type testMediaPathPreview struct {
	Config  MediaPathSettings `json:"config"`
	Preview struct {
		Plan        javspengine.MoviePlan `json:"plan"`
		SameNameNFO []string              `json:"same_name_nfo"`
	} `json:"preview"`
	Digest string `json:"preview_digest"`
}

func setupMediaPathHTTP(t *testing.T) (*Service, MediaPathSettings, javspNamingSample) {
	t.Helper()
	s, paths := setupMediaPathSettings(t)
	bundle, data := javspTestRuntime(t)
	s.javsp = NewJavspRunner(bundle, data)
	return s, paths, javspNamingSample{MediaID: "TEST-001", Title: "Example", Filenames: []string{"TEST-001-C.mp4"}}
}

func TestMediaPathHTTPPreviewThenAtomicSave(t *testing.T) {
	s, paths, sample := setupMediaPathHTTP(t)
	ctx := context.Background()
	before, err := s.db.allSettings()
	if err != nil {
		t.Fatal(err)
	}
	w := mediaPathRequest(t, s, ctx, "GET", "/api/settings/paths", nil, true)
	var got testMediaPathPreview
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || got.Config != paths {
		t.Fatalf("missing shared settings resource: %d %s", w.Code, w.Body.String())
	}
	paths.DownloadDirectory, paths.PendingDirectory, paths.CompletedDirectory = t.TempDir(), t.TempDir(), t.TempDir()
	paths.OutputFolderPattern = "preview/{num}"
	w = mediaPathRequest(t, s, ctx, "POST", "/api/settings/paths/preview", map[string]any{"config": paths, "sample": sample}, true)
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || len(got.Digest) != 64 {
		t.Fatalf("missing preview: %d %s", w.Code, w.Body.String())
	}
	if len(got.Preview.Plan.Files) != 1 || got.Preview.Plan.Files[0].Source != filepath.Join(got.Config.PendingDirectory, sample.Filenames[0]) || got.Preview.Plan.Files[0].Destination != filepath.Join(got.Config.CompletedDirectory, "preview", "TEST-001-C", "TEST-001-C.mp4") || len(got.Preview.SameNameNFO) != 1 {
		t.Fatalf("preview does not use proposed shared settings: %+v", got)
	}
	after, err := s.db.allSettings()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("GET/preview changed settings")
	}
	if strings.Contains(w.Body.String(), "stored-synthetic-secret") {
		t.Fatal("preview leaked secret")
	}
	for _, root := range []string{paths.DownloadDirectory, paths.PendingDirectory, paths.CompletedDirectory} {
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			t.Fatal("preview created media files")
		}
	}
	if _, err := os.Stat(s.javsp.workRoot); !os.IsNotExist(err) {
		t.Fatal("preview prepared execution assets")
	}
	w = mediaPathRequest(t, s, ctx, "PUT", "/api/settings/paths", map[string]any{"config": got.Config, "sample": sample, "preview_digest": got.Digest}, true)
	var saved struct {
		Config MediaPathSettings `json:"config"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || w.Code != 200 || saved.Config.IntakeRevision != paths.IntakeRevision+1 || saved.Config.JavspRevision != paths.JavspRevision+1 {
		t.Fatalf("save failed: %d %s", w.Code, w.Body.String())
	}
	jobs, err := s.db.ListMediaJobs("", 10)
	if err != nil || len(jobs) != 0 {
		t.Fatal("saving settings queued media")
	}
	private, err := s.db.JavspConfigPrivate()
	if err != nil || private.TranslatorAPIKey != "stored-synthetic-secret" {
		t.Fatal("shared save changed secret")
	}
}

func TestMediaPathHTTPSaveRejectsUnpreviewedChangedAndStale(t *testing.T) {
	for _, scenario := range []string{"no preview", "changed config", "changed sample", "stale", "canceled", "unsafe"} {
		t.Run(scenario, func(t *testing.T) {
			s, paths, sample := setupMediaPathHTTP(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := mediaPathRequest(t, s, ctx, "POST", "/api/settings/paths/preview", map[string]any{"config": paths, "sample": sample}, true)
			var preview testMediaPathPreview
			if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || w.Code != 200 {
				t.Fatalf("preview failed: %d %s", w.Code, w.Body.String())
			}
			paths = preview.Config
			digest, want := preview.Digest, 400
			switch scenario {
			case "no preview":
				digest = ""
			case "changed config":
				paths.PathBasenamePattern = "{num} {title}"
			case "changed sample":
				sample.Filenames = []string{"TEST-001-CD1.mp4"}
			case "stale":
				cfg, err := s.db.IntakeConfig()
				if err != nil {
					t.Fatal(err)
				}
				cfg.RunJavsp = !cfg.RunJavsp
				if _, err := s.db.UpdateIntakeConfig(cfg); err != nil {
					t.Fatal(err)
				}
				want = 409
			case "canceled":
				cancel()
			case "unsafe":
				paths.OutputFolderPattern = "../outside"
			}
			before, err := s.db.allSettings()
			if err != nil {
				t.Fatal(err)
			}
			w = mediaPathRequest(t, s, ctx, "PUT", "/api/settings/paths", map[string]any{"config": paths, "sample": sample, "preview_digest": digest}, true)
			if w.Code != want {
				t.Errorf("rejected save status=%d want=%d: %s", w.Code, want, w.Body.String())
			}
			after, err := s.db.allSettings()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("invalid save changed settings")
			}
		})
	}
}

func TestMediaPathHTTPAdminOnlyAndStrictPayload(t *testing.T) {
	s, paths, sample := setupMediaPathHTTP(t)
	ctx := context.Background()
	for _, endpoint := range []struct{ method, path string }{{"GET", "/api/settings/paths"}, {"POST", "/api/settings/paths/preview"}, {"PUT", "/api/settings/paths"}} {
		w := mediaPathRequest(t, s, ctx, endpoint.method, endpoint.path, map[string]any{"config": paths, "sample": sample}, false)
		if w.Code != 403 {
			t.Errorf("endpoint not admin protected: %d", w.Code)
		}
	}
	for _, field := range []string{"translator_api_key", "extra"} {
		w := mediaPathRequest(t, s, ctx, "POST", "/api/settings/paths/preview", map[string]any{"config": paths, "sample": sample, field: "unexpected"}, true)
		if w.Code != 400 {
			t.Errorf("unknown field accepted: %d", w.Code)
		}
	}
}
