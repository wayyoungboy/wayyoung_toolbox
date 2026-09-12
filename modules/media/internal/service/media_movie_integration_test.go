package service

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// Use the actual embedded engine and queue hooks together. Only the upstream
// site is a local fixture; config generation, parsing, NFOs, images, moves and
// persistent queue outcomes are real. No external sites or NAS media are used.
func TestMovieQueueRealEmbeddedEngineCompletesTwoExplicitFilms(t *testing.T) {
	for _, mode := range []string{"legacy", "separate", "in_place"} {
		t.Run(mode, func(t *testing.T) { testMovieQueueRealEmbeddedOutput(t, mode) })
	}
}

func TestIntakeChildRealEnginePreservesNamingAnchor(t *testing.T) {
	for _, mode := range []string{"legacy", "separate", "in_place"} {
		t.Run(mode, func(t *testing.T) { testMovieQueueRealEmbeddedOutput(t, "intake_"+mode) })
	}
}

func testMovieQueueRealEmbeddedOutput(t *testing.T, mode string) {
	t.Helper()
	fromIntake := strings.HasPrefix(mode, "intake_")
	mode = strings.TrimPrefix(mode, "intake_")
	f := newMovieQueueFixture(t)
	var details atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, "fixture site available")
		case "/TEST-001", "/TEST-002":
			details.Add(1)
			id := strings.TrimPrefix(r.URL.Path, "/")
			fmt.Fprintf(w, `<html><body><div class="container"><h3>%s Example title</h3><a class="bigImage"><img src="/cover.png"></a><div class="col-md-3 info"><p><span>識別碼:</span> %s</p><p><span>發行日期:</span> 2024-01-02</p></div></div></body></html>`, id, id)
		case "/cover.png":
			w.Header().Set("Content-Type", "image/png")
			if err := png.Encode(w, image.NewRGBA(image.Rect(0, 0, 40, 20))); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("engine requested an unselected film: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()
	cfg, err := f.s.db.JavspConfigPrivate()
	if err != nil {
		t.Fatal(err)
	}
	if mode != "legacy" {
		cfg = separateMovieOutput(t, f)
	}
	cfg.MinimumSize = "0B"
	cfg.ProxyServer = ""
	cfg.ProxyFree = map[string]string{"javbus": remote.URL, "javdb": remote.URL, "avsox": remote.URL, "javlib": remote.URL}
	cfg.NetworkRetry = 1
	cfg.NetworkTimeout = "2s"
	cfg.NormalCrawlers = []string{"javbus"}
	cfg.RequiredKeys = []string{"title", "cover"}
	cfg.MovieConcurrency = 1
	cfg.SleepAfterScraping = "0s"
	cfg.NormalizeActressName = false
	cfg.TranslatorEngine = "none"
	cfg.MoveFiles = true
	cfg.PathBasenamePattern = "{num}"
	cfg.NFOBasenamePattern = "movie"
	cfg.CoverAddLabel = false
	cfg.CoverCropOnIDPatterns = nil
	cfg.CoverCropEngine = "none"
	cfg.ExtraFanarts = false
	if mode == "in_place" {
		cfg.MoveFiles = false
		cfg.NFOBasenamePattern = "{num}"
		cfg.CoverBasenamePattern = "{num}-poster"
		cfg.FanartBasenamePattern = "{num}-fanart"
	}
	if _, err := f.s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	bundle, data := javspTestRuntime(t)
	f.s.javsp = newJavspRunnerWithEngine(bundle, data, embeddedJavspAdapter{})
	writeIntakeFixture(t, filepath.Dir(f.source), map[string]string{"TEST-002.mp4": "second video", "UNSELECTED-003.mp4": "unselected video"})
	if fromIntake {
		children, err := f.s.discoverIntakeMovies(context.Background(), MediaJob{IntakeTargetRoot: filepath.Dir(f.source), PlannedPaths: []string{f.source, filepath.Join(filepath.Dir(f.source), "TEST-002.mp4")}})
		if err != nil || len(children) != 2 {
			t.Fatalf("intake discovery: %+v %v", children, err)
		}
		for _, child := range children {
			if child.MovieSourceRoot != filepath.Dir(f.source) {
				t.Fatal("intake source boundary was not retained")
			}
			if child.SourcePath == f.source {
				// Replace the fixture's pre-created legacy job with real discovery
				// data, keeping only its existing queue identity/lifecycle.
				_, err = f.s.db.changeMediaJob(f.job.ID, f.job.Version, "discovered", func(job *MediaJob) error {
					job.MovieSourceRoot, job.SourcePaths, job.SourceIdentities = child.MovieSourceRoot, child.SourcePaths, child.SourceIdentities
					return nil
				})
			} else {
				_, err = f.s.db.EnqueueMediaJob(child)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	} else {
		if _, err := f.s.db.EnqueueMediaJob(MediaJob{Key: "movie:second", Kind: "movie", Phase: "organize", SourcePath: filepath.Join(filepath.Dir(f.source), "TEST-002.mp4")}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := f.s.drainPendingMovies(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := f.s.db.ListMediaJobs("", 10)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("wrong queue: %+v %v", jobs, err)
	}
	for _, job := range jobs {
		if job.Status != "success" || job.Phase != "done" || len(job.OutputPaths) != 1 || job.MoviePlan == nil || len(job.MoviePlan.Artifacts) < 2 {
			history, _ := f.s.db.ListJavspRuns(10)
			t.Fatalf("real engine failed: %+v history=%+v", job, history)
		}
		path := job.OutputPaths[0]
		resolvedID := job.MoviePlan.Plan.MediaID
		if mode == "legacy" && path != filepath.Join(cfg.InputDirectory, "organized", resolvedID, resolvedID+".mp4") {
			t.Fatalf("source boundary changed relative naming anchor: %s", path)
		}
		if mode == "separate" && !pathWithinRoot(cfg.OutputDirectory, path) {
			t.Fatalf("engine did not use the separate output directory: %s", path)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
		nfo, err := os.ReadFile(strings.TrimSuffix(path, filepath.Ext(path)) + ".nfo")
		if err != nil || !strings.Contains(string(nfo), "Example title") {
			t.Fatalf("missing title in targeted NFO: %q %v", nfo, err)
		}
		if mode == "in_place" {
			if path != job.SourcePath {
				t.Fatal("in-place mode moved the video")
			}
		} else if _, err := os.Stat(job.SourcePath); !os.IsNotExist(err) {
			t.Fatal("completed source not moved")
		}
	}
	if mode == "in_place" {
		if entries, err := os.ReadDir(cfg.OutputDirectory); err != nil || len(entries) != 0 {
			t.Fatalf("in-place mode wrote to the separate output directory: %v %v", entries, err)
		}
	}
	if details.Load() != 2 {
		t.Fatalf("wrong scrape count: %d", details.Load())
	}
	assertIntakeFixture(t, filepath.Dir(f.source), map[string]string{"UNSELECTED-003.mp4": "unselected video"})
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.source), "UNSELECTED-003.nfo")); !os.IsNotExist(err) {
		t.Fatal("unselected film was finalized")
	}
}
