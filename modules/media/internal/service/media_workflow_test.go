package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPipelineShowsResolvedLibraryAndWarnsAboutLegacyOutput(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pending, completed := filepath.Join(root, "pending"), filepath.Join(root, "completed")
	for _, dir := range []string{pending, completed} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	intake := IntakeConfig{SourceDirectory: filepath.Join(root, "downloads"), DestinationDirectory: pending}
	cfg := JavspConfig{InputDirectory: pending, OutputDirectory: completed, OutputFolderPattern: "{actress}/{num}", MoveFiles: true}
	paths := buildPipelinePaths(nil, intake, cfg)
	if paths.CompletedDirectory != completed || !paths.Consistent {
		t.Fatalf("explicit library omitted: %+v", paths)
	}
	cfg.OutputDirectory, cfg.OutputFolderPattern = "", "complete/{num}"
	paths = buildPipelinePaths(nil, intake, cfg)
	if paths.CompletedDirectory != filepath.Join(pending, "complete") || !strings.Contains(strings.Join(paths.Warnings, " "), "扫描目录") {
		t.Fatalf("legacy output ambiguity hidden: %+v", paths)
	}
	cfg.MoveFiles = false
	paths = buildPipelinePaths(nil, intake, cfg)
	if paths.CompletedDirectory != "" || !strings.Contains(strings.Join(paths.Warnings, " "), "原地") {
		t.Fatalf("in-place mode shown as library import: %+v", paths)
	}
}

func TestRSSExplicitLibraryIsCheckedBeforeDispatchAndAfterQueueRecovery(t *testing.T) {
	for _, mode := range []string{"feed-missing", "queue-missing", "queue-already-completed"} {
		t.Run(mode, func(t *testing.T) {
			var submissions atomic.Int32
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/feed":
					_, _ = w.Write([]byte(`<rss version="2.0"><channel><title>test</title><item><title>HD TEST-001</title><description>magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567</description></item></channel></rss>`))
				case "/api/v2/auth/login":
					_, _ = w.Write([]byte("Ok."))
				case "/api/v2/torrents/info":
					_, _ = w.Write([]byte("[]"))
				case "/api/v2/torrents/add":
					submissions.Add(1)
					_, _ = w.Write([]byte("Ok."))
				default:
					http.NotFound(w, r)
				}
			}))
			defer remote.Close()
			s := testService(t)
			if _, err := s.db.UpdateSettings(map[string]any{"preview_mode": false, "qb_host": remote.URL}); err != nil {
				t.Fatal(err)
			}
			cfg := defaultJavspConfig()
			cfg.InputDirectory, cfg.OutputDirectory = t.TempDir(), t.TempDir()
			if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
				t.Fatal(err)
			}
			task, err := validateTask(Task{Name: "test", FeedURL: remote.URL + "/feed", Enabled: true, ScheduleType: "interval", ScheduleMinutes: 60, ScheduleTime: "03:00", Extractor: "direct"})
			if err != nil {
				t.Fatal(err)
			}
			task, err = s.db.CreateTask(task)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "queue-already-completed" {
				writeIntakeFixture(t, cfg.OutputDirectory, map[string]string{"TEST-001.mp4": "already organized"})
			} else if err := os.Remove(cfg.OutputDirectory); err != nil {
				t.Fatal(err)
			}
			if mode == "feed-missing" {
				dry := false
				_, err := s.executeTaskReserved(context.Background(), task.ID, "test", &dry)
				if err == nil || !strings.Contains(err.Error(), "成品媒体库无法核对") {
					t.Fatalf("unavailable library silently ignored: %v", err)
				}
			} else {
				job, err := s.db.EnqueueRSSAcquisition(task, FeedEntry{EntryID: "item-1", Title: "HD TEST-001", Summary: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"})
				if err != nil {
					t.Fatal(err)
				}
				settings, _ := s.db.Settings(true)
				var session *QBSession
				_, err = s.executeRSSAcquisition(context.Background(), job, settings, &session)
				if session != nil {
					session.Close()
				}
				if err != nil {
					t.Fatal(err)
				}
				job, err = s.db.MediaJob(job.ID)
				if err != nil {
					t.Fatal(err)
				}
				want := "failed"
				if mode == "queue-already-completed" {
					want = "skipped"
				}
				if job.Status != want {
					t.Fatalf("queue status=%s want=%s error=%s", job.Status, want, job.Error)
				}
			}
			if submissions.Load() != 0 {
				t.Fatal("submitted despite unavailable/existing library")
			}
		})
	}
}

func TestPipelineCountsFailuresAndDistinguishesIntakeOrganizingFromScan(t *testing.T) {
	f := newMovieQueueFixture(t)
	intake, _ := f.s.db.IntakeConfig()
	intake.SourceDirectory = filepath.Join(f.root, "downloads")
	intake.DestinationDirectory = filepath.Dir(f.source)
	if err := os.Mkdir(intake.SourceDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	intake.RunJavsp = true
	if _, err := f.s.db.UpdateIntakeConfig(intake); err != nil {
		t.Fatal(err)
	}
	for i, status := range []string{"failed", "partial", "timed_out", "needs_attention", "canceled", "success"} {
		job, err := f.s.db.EnqueueMediaJob(MediaJob{Key: fmt.Sprintf("workflow:%d", i), Kind: "movie", Phase: "organize", SourcePath: f.source, Title: "test"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.db.db.Exec("UPDATE media_jobs SET status=? WHERE id=?", status, job.ID); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/pipeline", nil)
	req.Header.Set("X-Trim-Isadmin", "true")
	w := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("pipeline: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Stages []pipelineStage `json:"stages"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	stage := body.Stages[3]
	if stage.Status != "warning" || stage.AttentionCount != 4 || !strings.Contains(stage.Detail, "入库后整理已启用") || !strings.Contains(stage.Detail, "定时扫描已关闭") {
		t.Fatalf("misleading organizer status: %+v", stage)
	}
}

func TestMovieResumeCannotPublishToPreviousLibrary(t *testing.T) {
	f := newMovieQueueFixture(t)
	f.afterMoveError = context.DeadlineExceeded
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := f.s.db.MediaJob(f.job.ID)
	if err != nil || job.MoviePlan == nil || !job.MoviePlan.MetadataReady {
		t.Fatalf("missing checkpoint: %+v %v", job, err)
	}
	cfg, _ := f.s.db.JavspConfigPrivate()
	cfg.OutputDirectory = filepath.Join(f.root, "new-library")
	if err := os.Mkdir(cfg.OutputDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.RetryMediaJob(job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.MediaJob(job.ID)
	if err != nil || job.Status != "needs_attention" || !strings.Contains(job.Error, "整理完成目录已变更") || f.runs != 1 {
		t.Fatalf("old plan bypassed library setting: %+v %v", job, err)
	}
	if _, err := os.Stat(strings.TrimSuffix(f.target, ".mp4") + ".nfo"); !os.IsNotExist(err) {
		t.Fatal("published another artifact in the old library")
	}
	assertIntakeFixture(t, filepath.Dir(f.target), map[string]string{"TEST-001.mp4": "video", "movie.nfo": f.nfo})
}
