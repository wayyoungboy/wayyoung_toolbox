package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type intakeQueueFixture struct {
	s              *Service
	source, target string
	stops, deletes atomic.Int32
	adds           atomic.Int32
	failDelete     atomic.Bool
	failInfo       atomic.Bool
	incomplete     atomic.Bool
	reportedPath   atomic.Pointer[string]
	stopped        atomic.Bool
	neverStop      atomic.Bool
	addedOn        atomic.Int64
	torrentHash    atomic.Pointer[string]
	onLogin        func()
}

func newIntakeQueueFixture(t *testing.T) *intakeQueueFixture {
	t.Helper()
	f := &intakeQueueFixture{s: testService(t)}
	f.addedOn.Store(1700000000)
	root := t.TempDir()
	src, dst := filepath.Join(root, "downloads"), filepath.Join(root, "pending")
	writeIntakeFixture(t, src, map[string]string{"TEST-001.mp4": "video"})
	writeIntakeFixture(t, dst, map[string]string{".placeholder": ""})
	f.source, f.target = filepath.Join(src, "TEST-001.mp4"), filepath.Join(dst, "TEST-001.mp4")
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			if f.onLogin != nil {
				f.onLogin()
			}
			fmt.Fprint(w, "Ok.")
		case "/api/v2/torrents/info":
			if f.failInfo.Load() {
				http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
				return
			}
			state := "stalledUP"
			if f.stopped.Load() {
				state = "stoppedUP"
			}
			contentPath := f.source
			if changed := f.reportedPath.Load(); changed != nil {
				contentPath = *changed
			}
			progress := 1.0
			left := 0
			if f.incomplete.Load() {
				progress = 0.5
				left = 100
			}
			hash := "queue-hash"
			if configured := f.torrentHash.Load(); configured != nil {
				hash = *configured
			}
			fmt.Fprintf(w, `[{"hash":%q,"name":"TEST-001","content_path":%q,"state":%q,"progress":%v,"amount_left":%d,"added_on":%d}]`, hash, contentPath, state, progress, left, f.addedOn.Load())
		case "/api/v2/torrents/stop":
			f.stops.Add(1)
			if !f.neverStop.Load() {
				f.stopped.Store(true)
			}
		case "/api/v2/torrents/add":
			f.adds.Add(1)
			http.NotFound(w, r)
		case "/api/v2/torrents/delete":
			f.deletes.Add(1)
			if err := r.ParseForm(); err != nil || r.Form.Get("deleteFiles") != "false" {
				t.Errorf("unsafe qB delete: %v %v", r.Form, err)
			}
			if f.failDelete.Load() {
				http.Error(w, "temporarily unavailable", 503)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(remote.Close)
	if _, err := f.s.db.UpdateSettings(map[string]any{"qb_host": remote.URL, "qb_username": "test", "qb_password": "test"}); err != nil {
		t.Fatal(err)
	}
	config := defaultIntakeConfig()
	config.SourceDirectory = src
	config.DestinationDirectory = dst
	config.RunJavsp = false
	if _, err := f.s.db.UpdateIntakeConfig(config); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestMediaQueueIntakeReaddedTorrentHasNewJob(t *testing.T) {
	f := newIntakeQueueFixture(t)
	if _, err := f.s.runIntake(context.Background(), "manual"); err != nil {
		t.Fatal(err)
	}
	f.addedOn.Add(60)
	f.stopped.Store(false)
	writeIntakeFixture(t, filepath.Dir(f.source), map[string]string{"TEST-001.mp4": "video"})
	result, err := f.s.runIntake(context.Background(), "manual")
	if err != nil || result.DuplicatesCleaned != 1 {
		t.Fatalf("new task reused old move receipt: %+v %v", result, err)
	}
	jobs, err := f.s.db.ListMediaJobs("success", 10)
	if err != nil || len(jobs) != 2 || jobs[0].ID == jobs[1].ID {
		t.Fatalf("re-added torrent collapsed into historical task: %+v %v", jobs, err)
	}
}

func TestIntakeQueueIsNotStarvedByUnfinishedDownloads(t *testing.T) {
	f := newIntakeQueueFixture(t)
	for i := 0; i < 110; i++ {
		if _, err := f.s.db.EnqueueMediaJob(MediaJob{Key: fmt.Sprintf("download:%d", i), Kind: "torrent", Phase: "download"}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := f.s.runIntake(context.Background(), "manual")
	if err != nil || result.TasksDeleted != 1 || f.deletes.Load() != 1 {
		t.Fatalf("unfinished downloads starved completed intake: %+v %v", result, err)
	}
	if _, err := os.Stat(f.target); err != nil {
		t.Fatal(err)
	}
}

func TestMediaQueueIntakeOldCleanupCannotDeleteReaddedTorrent(t *testing.T) {
	f := newIntakeQueueFixture(t)
	f.failDelete.Store(true)
	if _, err := f.s.runIntake(context.Background(), "manual"); err != nil {
		t.Fatal(err)
	}
	jobs, err := f.s.db.ListMediaJobs("failed", 10)
	if err != nil || len(jobs) != 1 || jobs[0].Phase != "qb_cleanup" {
		t.Fatalf("missing cleanup retry: %+v %v", jobs, err)
	}
	f.addedOn.Add(60)
	f.failDelete.Store(false)
	if _, err := f.s.db.RetryMediaJob(jobs[0].ID, jobs[0].Version); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.runIntake(context.Background(), "manual"); err != nil {
		t.Fatal(err)
	}
	job, err := f.s.db.MediaJob(jobs[0].ID)
	if err != nil || job.Status != "needs_attention" || f.deletes.Load() != 1 {
		t.Fatalf("old job deleted replacement torrent: %+v deletes=%d %v", job, f.deletes.Load(), err)
	}
}

func TestMediaQueueIntakeMissingTorrentGenerationDoesNotMove(t *testing.T) {
	f := newIntakeQueueFixture(t)
	f.addedOn.Store(0)
	if _, err := f.s.runIntake(context.Background(), "manual"); err != nil {
		t.Fatal(err)
	}
	if f.stops.Load() != 0 || f.deletes.Load() != 0 {
		t.Fatal("unknown torrent identity authorized mutation")
	}
	assertIntakeFixture(t, filepath.Dir(f.source), map[string]string{"TEST-001.mp4": "video"})
}

func captureQueueFixture(t *testing.T, job MediaJob) {
	t.Helper()
	target, err := os.OpenRoot(job.IntakeTargetRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	rel := filepath.Base(job.SourcePath)
	opPath := filepath.Join(job.IntakeSourceRoot, ".media-center-intake", intakeOperationName(target, rel, job.ID))
	if err := os.MkdirAll(opPath, 0700); err != nil {
		t.Fatal(err)
	}
	op, err := os.OpenRoot(opPath)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Close()
	if err := saveIntakeJournal(op, intakeCleanupJournal{JobID: job.ID, Relative: rel, DestinationRoot: target.Name(), State: "captured"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(job.SourcePath, filepath.Join(opPath, "content")); err != nil {
		t.Fatal(err)
	}
}

func TestMediaQueueCapturedRecoveryLeavesReplacementTorrentUntouched(t *testing.T) {
	f := newIntakeQueueFixture(t)
	f.s.mediaFiles.Lock()
	_, err := f.s.runIntake(context.Background(), "manual")
	f.s.mediaFiles.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := f.s.db.ListMediaJobs("pending", 10)
	if err != nil || len(jobs) != 1 {
		t.Fatal(err)
	}
	job, err := f.s.db.ClaimMediaJob(jobs[0].ID, jobs[0].Version)
	if err != nil {
		t.Fatal(err)
	}
	captureQueueFixture(t, job)
	writeIntakeFixture(t, filepath.Dir(f.target), map[string]string{"TEST-001.mp4": "video"})
	writeIntakeFixture(t, filepath.Dir(f.source), map[string]string{"TEST-001.mp4": "new torrent data"})
	f.addedOn.Add(60)
	if _, err := f.s.failQueuedIntake(job, context.Canceled, true, &IntakeRunResult{}); err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.MediaJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.RetryMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	// Drain only the existing task, without discovering/enqueuing the replacement.
	if err := f.s.drainPendingIntake(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.MediaJob(job.ID)
	if err != nil || job.HoldFiles || job.Phase != "qb_cleanup" || job.Status != "needs_attention" {
		t.Fatalf("captured recovery frozen by replacement: %+v %v", job, err)
	}
	if f.stops.Load() != 0 || f.deletes.Load() != 0 {
		t.Fatal("old recovery mutated the replacement qB task")
	}
	assertIntakeFixture(t, filepath.Dir(f.source), map[string]string{"TEST-001.mp4": "new torrent data"})
}

func TestMediaQueueTransferUsesFreshQBStateAndConfirmedStop(t *testing.T) {
	for _, mode := range []string{"stale_snapshot", "pre_rename_receipt", "stop_not_confirmed"} {
		t.Run(mode, func(t *testing.T) {
			f := newIntakeQueueFixture(t)
			f.s.mediaFiles.Lock()
			_, err := f.s.runIntake(context.Background(), "manual")
			f.s.mediaFiles.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			jobs, err := f.s.db.ListMediaJobs("pending", 10)
			if err != nil || len(jobs) != 1 {
				t.Fatal(err)
			}
			job, err := f.s.db.ClaimMediaJob(jobs[0].ID, jobs[0].Version)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "pre_rename_receipt" {
				dst, err := os.OpenRoot(job.IntakeTargetRoot)
				if err != nil {
					t.Fatal(err)
				}
				defer dst.Close()
				info, err := os.Stat(job.SourcePath)
				if err != nil {
					t.Fatal(err)
				}
				rel := filepath.Base(job.SourcePath)
				opPath := filepath.Join(job.IntakeSourceRoot, ".media-center-intake", intakeOperationName(dst, rel, job.ID))
				if err := os.MkdirAll(opPath, 0700); err != nil {
					t.Fatal(err)
				}
				op, err := os.OpenRoot(opPath)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Close()
				if err := saveIntakeJournal(op, intakeCleanupJournal{JobID: job.ID, Relative: rel, DestinationRoot: dst.Name(), State: "moving", Outcome: "moved", Identity: intakeFileIdentity(info)}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "stop_not_confirmed" {
				f.neverStop.Store(true)
			} else {
				f.incomplete.Store(true)
			}
			settings, err := f.s.db.Settings(true)
			if err != nil {
				t.Fatal(err)
			}
			session, err := f.s.collector.OpenQBSession(context.Background(), settings)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
			defer cancel()
			stale := []QBTorrent{{Hash: job.TorrentHash, ContentPath: job.SourcePath, Progress: 1, State: "stalledUP"}}
			_, err = f.s.transferQueuedIntake(ctx, session, stale, job, &IntakeRunResult{})
			if err != nil {
				t.Fatal(err)
			}
			assertIntakeFixture(t, filepath.Dir(f.source), map[string]string{"TEST-001.mp4": "video"})
			if _, err := os.Stat(f.target); !os.IsNotExist(err) {
				t.Fatalf("unsafe move using %s: %v", mode, err)
			}
		})
	}
}

func TestMediaQueueIntakeRejectsChangedQBPath(t *testing.T) {
	f := newIntakeQueueFixture(t)
	f.s.mediaFiles.Lock()
	_, err := f.s.runIntake(context.Background(), "manual")
	f.s.mediaFiles.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	newPath := f.source + ".relocated"
	f.reportedPath.Store(&newPath)
	if _, err := f.s.runIntake(context.Background(), "manual"); err != nil {
		t.Fatal(err)
	}
	if f.stops.Load() != 0 || f.deletes.Load() != 0 {
		t.Fatal("processed obsolete source after qB path change")
	}
	assertIntakeFixture(t, filepath.Dir(f.source), map[string]string{"TEST-001.mp4": "video"})
}

func TestMediaQueueTransferFailureKeepsCapturedFileGuard(t *testing.T) {
	f := newIntakeQueueFixture(t)
	f.s.mediaFiles.Lock()
	_, err := f.s.runIntake(context.Background(), "manual")
	f.s.mediaFiles.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := f.s.db.ListMediaJobs("pending", 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs: %+v %v", jobs, err)
	}
	job, err := f.s.db.ClaimMediaJob(jobs[0].ID, jobs[0].Version)
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.OpenRoot(job.IntakeSourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.OpenRoot(job.IntakeTargetRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	rel := filepath.Base(job.SourcePath)
	opPath := filepath.Join(job.IntakeSourceRoot, ".media-center-intake", intakeOperationName(target, rel, job.ID))
	if err := os.MkdirAll(opPath, 0700); err != nil {
		t.Fatal(err)
	}
	op, err := os.OpenRoot(opPath)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Close()
	if err := saveIntakeJournal(op, intakeCleanupJournal{JobID: job.ID, Relative: rel, DestinationRoot: target.Name(), State: "captured"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(job.SourcePath, filepath.Join(opPath, "content")); err != nil {
		t.Fatal(err)
	}
	// A cancellation returned before entering recovery must not release the
	// durable file transaction merely because the error lacks a pending wrapper.
	_, err = f.s.failQueuedIntake(job, context.Canceled, false, &IntakeRunResult{})
	if err != nil {
		t.Fatal(err)
	}
	if blocked, err := f.s.db.MediaFilesBlocked(); err != nil || !blocked {
		t.Fatalf("captured files released on cancel: %v %v", blocked, err)
	}
}

func TestMediaQueueIntakeRechecksDownloadBeforeMove(t *testing.T) {
	f := newIntakeQueueFixture(t)
	f.s.mediaFiles.Lock()
	_, err := f.s.runIntake(context.Background(), "manual")
	f.s.mediaFiles.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	f.incomplete.Store(true)
	if _, err := f.s.runIntake(context.Background(), "manual"); err != nil {
		t.Fatal(err)
	}
	if f.stops.Load() != 0 || f.deletes.Load() != 0 {
		t.Fatalf("incomplete torrent touched: stops=%d deletes=%d", f.stops.Load(), f.deletes.Load())
	}
	assertIntakeFixture(t, filepath.Dir(f.source), map[string]string{"TEST-001.mp4": "video"})
	if _, err := os.Stat(f.target); !os.IsNotExist(err) {
		t.Fatalf("incomplete file moved: %v", err)
	}
}

func TestMediaQueueIntakeBusyIsQueuedWithoutTouchingFiles(t *testing.T) {
	f := newIntakeQueueFixture(t)
	f.s.mediaFiles.Lock()
	result, err := f.s.runIntake(context.Background(), "manual")
	f.s.mediaFiles.Unlock()
	if err != nil || result.Failed != 0 {
		t.Fatalf("busy lost intake request: %+v %v", result, err)
	}
	jobs, err := f.s.db.ListMediaJobs("pending", 100)
	if err != nil || len(jobs) != 1 || jobs[0].Phase != "transfer" {
		t.Fatalf("not durably queued: %+v %v", jobs, err)
	}
	if f.stops.Load() != 0 || f.deletes.Load() != 0 {
		t.Fatal("touched qB while files busy")
	}
	if _, err := os.Stat(f.source); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.runIntake(context.Background(), "manual"); err != nil {
		t.Fatal(err)
	}
	job, err := f.s.db.MediaJob(jobs[0].ID)
	if err != nil || job.Status != "success" {
		t.Fatalf("queue did not drain: %+v %v", job, err)
	}
}

func TestIntakeManualRunReportsPreviouslyBlockedCandidate(t *testing.T) {
	f := newIntakeQueueFixture(t)
	if err := os.WriteFile(f.target, []byte("different content"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := f.s.runIntake(context.Background(), "manual")
	if err != nil || first.Failed != 1 {
		t.Fatalf("first collision was not recorded: %+v %v", first, err)
	}
	stops := f.stops.Load()
	second, err := f.s.runIntake(context.Background(), "manual")
	if err != nil || second.Failed != 1 || second.Moved != 0 || len(second.Errors) != 1 ||
		!strings.Contains(second.Errors[0], "仍需处理") || f.stops.Load() != stops {
		t.Fatalf("repeat action hid existing conflict or touched qB: %+v %v", second, err)
	}
	runs, err := f.s.db.ListMaintenanceRunsByKinds(1, "intake")
	if err != nil || len(runs) != 1 || runs[0]["status"] != "partial" || runs[0]["found_count"] != 1 {
		t.Fatalf("manual run showed success despite blocked candidate: %+v %v", runs, err)
	}
}

func TestMediaQueueIntakeRetriesOnlyQBDeleteAfterRestart(t *testing.T) {
	f := newIntakeQueueFixture(t)
	f.failDelete.Store(true)
	result, err := f.s.runIntake(context.Background(), "manual")
	if err != nil || result.Failed != 1 {
		t.Fatalf("first run: %+v %v", result, err)
	}
	jobs, err := f.s.db.ListMediaJobs("failed", 100)
	if err != nil || len(jobs) != 1 || jobs[0].Phase != "qb_cleanup" {
		t.Fatalf("lost completed move: %+v %v", jobs, err)
	}
	if _, err := os.Stat(f.source); !os.IsNotExist(err) {
		t.Fatalf("source not moved: %v", err)
	}
	if _, err := os.Stat(f.target); err != nil {
		t.Fatal(err)
	}
	job, err := f.s.db.RetryMediaJob(jobs[0].ID, jobs[0].Version)
	if err != nil {
		t.Fatal(err)
	}
	if f.stops.Load() != 1 || f.deletes.Load() != 1 {
		t.Fatal("unexpected first attempt")
	}
	f.failDelete.Store(false)
	// Pretend another worker claimed deletion and the service then exited.
	if _, err := f.s.db.ClaimMediaJob(job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	var dbPath string
	var seq int
	var schema string
	if err := f.s.db.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &schema, &dbPath); err != nil {
		t.Fatal(err)
	}
	if err := f.s.db.Close(); err != nil {
		t.Fatal(err)
	}
	f.s.db, err = OpenDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err = f.s.runIntake(context.Background(), "manual")
	if err != nil || result.TasksDeleted != 1 || result.Moved != 0 || f.stops.Load() != 1 || f.deletes.Load() != 2 {
		t.Fatalf("repeated completed stage: %+v %v stops=%d deletes=%d", result, err, f.stops.Load(), f.deletes.Load())
	}
	job, err = f.s.db.MediaJob(job.ID)
	if err != nil || job.Phase != "done" || job.Status != "success" {
		t.Fatalf("not completed: %+v %v", job, err)
	}
}

func TestMediaQueueIntakeCreatesTargetedMovieJobWithoutFullScan(t *testing.T) {
	f := newIntakeQueueFixture(t)
	configureIntakeJavsp(t, f)
	writeIntakeFixture(t, filepath.Dir(f.target), map[string]string{"UNRELATED-002.mp4": "untouched"})
	if _, err := f.s.runIntake(context.Background(), "manual"); err != nil {
		t.Fatal(err)
	}
	jobs, err := f.s.db.ListMediaJobs("", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected parent and one movie, got %+v", jobs)
	}
	var movie MediaJob
	for _, job := range jobs {
		if job.Kind == "movie" {
			movie = job
		}
	}
	canonicalTarget, err := filepath.EvalSymlinks(f.target)
	if err != nil {
		t.Fatal(err)
	}
	if movie.SourcePath != canonicalTarget || movie.ParentID == 0 || movie.Status != "pending" || movie.Phase != "organize" {
		t.Fatalf("wrong movie job: %+v", movie)
	}
}
