package service

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletedLibraryRecognizesVideoIDsWithoutTrustingEmptyMetadata(t *testing.T) {
	cfg, _ := localIndexFixture(t)
	for _, name := range []string{"actor/TEST-001_CH.HD.mp4", "actor/[TEST-002] sample title.mkv", "FC2PPV-123456-C.mp4", "test-001-C.mp4"} {
		putIndexFile(t, cfg.OutputDirectory, name, "video")
	}
	putIndexFile(t, cfg.OutputDirectory, "TEST-003.mp4", "")
	putIndexFile(t, cfg.OutputDirectory, "TEST-004/movie.nfo", "metadata only")
	putIndexFile(t, cfg.OutputDirectory, "TEST-005 TEST-006.mp4", "ambiguous")
	putIndexFile(t, cfg.OutputDirectory, "unknown.mp4", "video")
	putIndexFile(t, cfg.OutputDirectory, "TEST-007.mp4.part", "incomplete")
	outside := putIndexFile(t, t.TempDir(), "TEST-008.mp4", "outside")
	if err := os.Symlink(outside, filepath.Join(cfg.OutputDirectory, "TEST-008.mp4")); err != nil {
		t.Fatal(err)
	}
	index, err := scanCompletedLibrary(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"TEST-001", "TEST-002", "FC2-123456"} {
		if _, ok := index.MatchTitle(id); !ok {
			t.Errorf("missing %s", id)
		}
	}
	for _, title := range []string{"TEST-003", "TEST-004", "TEST-005", "TEST-006", "TEST-007", "TEST-008", "TEST-001 TEST-009"} {
		if _, ok := index.MatchTitle(title); ok {
			t.Errorf("unsafe duplicate %s", title)
		}
	}
	if index.report.VideoCount != 6 || index.report.UnrecognizedCount != 2 || len(index.report.Items) != 3 {
		t.Fatalf("report=%+v", index.report)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := scanCompletedLibrary(ctx, cfg); err == nil {
		t.Fatal("canceled scan accepted")
	}
}

func TestCompletedLibraryAPIIsReadOnlyBoundedAndConfigAware(t *testing.T) {
	s := testService(t)
	cfg, _ := localIndexFixture(t)
	putIndexFile(t, cfg.OutputDirectory, "TEST-001_CH.HD.mp4", "video")
	if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, admin bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader("{}"))
		if admin {
			r.Header.Set("X-Trim-Isadmin", "true")
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request("POST", "/api/library/completed/scan", false); w.Code != 403 {
		t.Fatalf("auth=%d", w.Code)
	}
	if w := request("GET", "/api/library/completed", true); w.Code != 200 || !strings.Contains(w.Body.String(), `"scanned":false`) {
		t.Fatalf("initial=%s", w.Body)
	}
	if w := request("POST", "/api/library/completed/scan", true); w.Code != 200 || !strings.Contains(w.Body.String(), "TEST-001") {
		t.Fatalf("scan=%d %s", w.Code, w.Body)
	}
	if w := request("GET", "/api/library/completed?query=absent", true); w.Code != 200 || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatalf("filter=%s", w.Body)
	}
	if w := request("GET", "/api/library/completed?limit=10000", true); w.Code != 400 {
		t.Fatalf("bound=%d", w.Code)
	}
	if w := request("GET", "/api/library/completed?offset=-1", true); w.Code != 400 {
		t.Fatalf("offset=%d", w.Code)
	}
	if w := request("GET", "/api/library/completed?offset=999", true); w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatalf("page=%s", w.Body)
	}
	other := cfg
	other.OutputDirectory = t.TempDir()
	if _, err := s.db.UpdateJavspConfig(other); err != nil {
		t.Fatal(err)
	}
	if w := request("GET", "/api/library/completed", true); w.Code != 200 || strings.Contains(w.Body.String(), "TEST-001") || !strings.Contains(w.Body.String(), `"scanned":false`) {
		t.Fatalf("config retained stale index: %s", w.Body)
	}
	if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(cfg.OutputDirectory); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/api/library/completed/scan", true); w.Code == 200 {
		t.Fatalf("missing root treated as empty: %s", w.Body)
	}
	if w := request("GET", "/api/library/completed", true); strings.Contains(w.Body.String(), "TEST-001") {
		t.Fatalf("stale snapshot trusted: %s", w.Body)
	}
}

func TestRSSCompletedLibraryStopsFreshDispatchForNamedVideos(t *testing.T) {
	f := newRSSExecutionFixture(t)
	cfg, _ := localIndexFixture(t)
	putIndexFile(t, cfg.OutputDirectory, "actor/TEST-001_CH.HD.mp4", "video")
	if _, err := f.s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	job, detail := f.run(t)
	if job.Status != "skipped" || detail.Status != "skipped" || f.adds.Load() != 0 {
		t.Fatalf("duplicate dispatched: %+v %+v", job, detail)
	}
}
