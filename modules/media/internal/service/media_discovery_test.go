package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	javspengine "github.com/yuukiy/javsp-go/engine"
)

func TestManualJavspDiscoversIntoDurableQueueWhileOrganizerIsBusy(t *testing.T) {
	s := testService(t)
	bundle, data := javspTestRuntime(t)
	engine := &fakeEmbeddedJavsp{complete: true}
	s.javsp = newJavspRunnerWithEngine(bundle, data, engine)
	root := t.TempDir()
	writeIntakeFixture(t, root, map[string]string{
		"TEST-001.1.mp4": "first", "TEST-001.2.mp4": "second", "TEST-002.mp4": "another", "[未识别].mp4": "unknown",
		"#整理完成/TEST-003.mp4": "already organized",
	})
	cfg := defaultJavspConfig()
	cfg.InputDirectory, cfg.MinimumSize = root, "0B"
	if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.runCtx = ctx
	s.mediaFiles.Lock()
	defer s.mediaFiles.Unlock()
	s.javspLock.Lock()
	defer s.javspLock.Unlock()
	for iteration := 0; iteration < 2; iteration++ {
		runID, err := s.startJavsp(context.Background(), "manual")
		if err != nil {
			t.Fatalf("busy organizer rejected read-only discovery: %v", err)
		}
		done := make(chan struct{})
		go func() { s.wait.Wait(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("discovery did not finish")
		}
		jobs, err := s.db.ListMediaJobs("", 10)
		if err != nil || len(jobs) != 3 {
			t.Fatalf("discovery was lost or duplicated: %+v %v", jobs, err)
		}
		for _, job := range jobs {
			if iteration == 0 && job.DiscoveryRunID != runID {
				t.Fatalf("missing discovery provenance: %+v", job)
			}
			if job.MediaID == "TEST-001" && len(job.SourcePaths) != 2 {
				t.Fatalf("split multipart film into separate jobs: %+v", job)
			}
			if job.MediaID == "" {
				if job.Status != "needs_attention" || job.Error == "" {
					t.Fatalf("unrecognized film hidden: %+v", job)
				}
			} else if job.Status != "pending" || job.Phase != "organize" {
				t.Fatalf("discovery ran organizer: %+v", job)
			}
			for _, path := range job.movieSources() {
				if _, err := os.Stat(path); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	engine.mu.Lock()
	runs := engine.runCount
	engine.mu.Unlock()
	if runs != 0 {
		t.Fatal("discovery invoked scraping instead of enqueuing")
	}
	if _, err := os.Stat(filepath.Join(root, "TEST-001.nfo")); !os.IsNotExist(err) {
		t.Fatal("discovery wrote metadata")
	}
	history, err := s.db.ListJavspRuns(10)
	if err != nil || len(history) != 2 || history[0]["trigger_type"] != "manual_scan" {
		t.Fatalf("discovery history is not distinguished from actual execution: %+v %v", history, err)
	}
}

func TestMovieDiscoveryExposesProgressAndCanCancelWithoutStoppingOrganizer(t *testing.T) {
	s := testService(t)
	bundle, data := javspTestRuntime(t)
	s.javsp = NewJavspRunner(bundle, data)
	cfg := defaultJavspConfig()
	cfg.InputDirectory = t.TempDir()
	if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	s.discoverMovies = func(ctx context.Context, _ javspengine.Options) (javspengine.DiscoveryResult, error) {
		close(started)
		<-ctx.Done()
		return javspengine.DiscoveryResult{}, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.runCtx = ctx
	runID, err := s.startJavsp(context.Background(), "schedule")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("scanner did not start")
	}
	s.javsp.BeginRun() // a separate queued organizer has been accepted
	defer s.javsp.StopCurrent()
	status := httptest.NewRecorder()
	s.handleJavsp(status, httptest.NewRequest(http.MethodGet, "/api/javsp", nil))
	var payload struct {
		Running   bool
		Discovery struct {
			RunID  int64 `json:"run_id"`
			Status string
		}
	}
	if err := json.Unmarshal(status.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Running || payload.Discovery.RunID != runID || payload.Discovery.Status != "running" {
		t.Fatalf("scan appears idle: %s", status.Body.String())
	}
	for _, id := range []int64{runID + 1, runID} {
		r := httptest.NewRequest(http.MethodPost, "/api/javsp/discovery/cancel", strings.NewReader(fmt.Sprintf(`{"run_id":%d}`, id)))
		r.Header.Set("X-Trim-Isadmin", "true")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		want := http.StatusAccepted
		if id != runID {
			want = http.StatusConflict
		}
		if w.Code != want {
			t.Fatalf("cancel status=%d want=%d body=%s", w.Code, want, w.Body.String())
		}
	}
	s.wait.Wait()
	history, err := s.db.ListJavspRuns(10)
	if err != nil || len(history) != 1 || history[0]["status"] != "canceled" || history[0]["trigger_type"] != "schedule_scan" {
		t.Fatalf("incorrect scan settlement: %+v %v", history, err)
	}
	s.javsp.mu.Lock()
	stopped := s.javsp.stopRequested
	s.javsp.mu.Unlock()
	if stopped {
		t.Fatal("canceling discovery stopped an unrelated organizer")
	}
	jobs, err := s.db.ListMediaJobs("", 10)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("canceled discovery left partial batch: %+v %v", jobs, err)
	}
}

func TestDiscoveredMovieRejectsReplacedSourceBeforeScrapingAndMetadata(t *testing.T) {
	for _, checkpoint := range []string{"before_scraping", "before_metadata"} {
		t.Run(checkpoint, func(t *testing.T) {
			f := newMovieQueueFixture(t)
			candidate, err := movieDiscoveryJob(context.Background(), f.root, "TEST-001", []string{f.source}, 1)
			if err != nil {
				t.Fatal(err)
			}
			job, err := f.s.db.changeMediaJob(f.job.ID, f.job.Version, "discovered", func(job *MediaJob) error {
				job.SourcePaths, job.SourceIdentities = candidate.SourcePaths, candidate.SourceIdentities
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(f.source)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(f.source, f.source+".original"); err != nil {
				t.Fatal(err)
			}
			writeIntakeFixture(t, filepath.Dir(f.source), map[string]string{filepath.Base(f.source): "video"})
			if err := os.Chtimes(f.source, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			if checkpoint == "before_scraping" {
				if err := f.s.drainPendingMovies(context.Background()); err != nil {
					t.Fatal(err)
				}
				current, err := f.s.db.MediaJob(job.ID)
				if err != nil {
					t.Fatal(err)
				}
				if current.Status != "needs_attention" || f.runs != 0 {
					t.Fatalf("replaced queued source was scraped: status=%s engine_runs=%d", current.Status, f.runs)
				}
			} else {
				cfg, err := f.s.db.JavspConfigPrivate()
				if err != nil {
					t.Fatal(err)
				}
				_, err = prepareMoviePlan(context.Background(), job, cfg, JavspMoviePlan{MediaID: "TEST-001", MoveFiles: true, NFOPath: filepath.Join(filepath.Dir(f.target), "movie.nfo"), Files: []JavspFileMove{{Source: f.source, Destination: f.target}}})
				if !errors.Is(err, errIntakeConflict) {
					t.Fatalf("replaced source accepted before metadata writes: %v", err)
				}
			}
			assertIntakeFixture(t, filepath.Dir(f.source), map[string]string{filepath.Base(f.source): "video", filepath.Base(f.source) + ".original": "video"})
			if _, err := os.Stat(f.target); !os.IsNotExist(err) {
				t.Fatal("replacement was moved")
			}
		})
	}
}

func TestMovieDiscoveryExcludesCustomOrganizedRoot(t *testing.T) {
	s := testService(t)
	bundle, data := javspTestRuntime(t)
	s.javsp = NewJavspRunner(bundle, data)
	cfg := defaultJavspConfig()
	cfg.InputDirectory, cfg.MinimumSize = t.TempDir(), "0B"
	cfg.OutputFolderPattern = "library/archive/{num}"
	cfg.IgnoredFolderPatterns = []string{}
	writeIntakeFixture(t, cfg.InputDirectory, map[string]string{
		"library/archive/TEST-001.1.mp4": "organized",
		"TEST-001.2.mp4":                 "new part", "pending/archive/TEST-002.mp4": "new film",
	})
	if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := s.startJavsp(context.Background(), "manual"); err != nil {
		t.Fatal(err)
	}
	s.wait.Wait()
	jobs, err := s.db.ListMediaJobs("", 10)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("output exclusion lost or duplicated films: %+v %v", jobs, err)
	}
	for _, job := range jobs {
		if len(job.SourcePaths) != 1 {
			t.Fatalf("organized input entered queued multipart selection: %+v", job)
		}
		if job.MediaID == "TEST-001" && filepath.Base(job.SourcePath) != "TEST-001.2.mp4" {
			t.Fatalf("organized file displaced the pending file: %+v", job)
		}
	}
}
