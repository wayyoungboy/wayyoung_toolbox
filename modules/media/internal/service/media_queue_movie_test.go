package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type movieQueueFixture struct {
	s                    *Service
	job                  MediaJob
	source, target, root string
	runs                 int
	afterMoveError       error
	afterPlanError       error
	afterArtifactError   error
	nfo                  string
	hardLink             bool
}

func newMovieQueueFixture(t *testing.T) *movieQueueFixture {
	t.Helper()
	f := &movieQueueFixture{s: testService(t), root: t.TempDir()}
	f.nfo = "<movie><title>Example title</title></movie>"
	var rootErr error
	f.root, rootErr = filepath.EvalSymlinks(f.root)
	if rootErr != nil {
		t.Fatal(rootErr)
	}
	f.source = filepath.Join(f.root, "pending", "TEST-001.mp4")
	f.target = filepath.Join(f.root, "organized", "TEST-001", "TEST-001.mp4")
	writeIntakeFixture(t, filepath.Dir(f.source), map[string]string{"TEST-001.mp4": "video"})
	cfg := defaultJavspConfig()
	cfg.InputDirectory = f.root
	cfg.OutputFolderPattern = "organized/{num}"
	cfg.TimeoutSeconds = 5
	if _, err := f.s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	bundle, data := javspTestRuntime(t)
	f.s.javsp = newJavspRunnerWithEngine(bundle, data, selectedJavspFixture(func(ctx context.Context, options embeddedJavspOptions) (embeddedJavspResult, error) {
		f.runs++
		if len(options.InputFiles) != 1 || options.InputFiles[0] != f.source {
			t.Errorf("wrong queued files: %+v", options.InputFiles)
		}
		plan := JavspMoviePlan{MediaID: "TEST-001", Files: []JavspFileMove{{Source: f.source, Destination: f.target}}, NFOPath: filepath.Join(filepath.Dir(f.target), "movie.nfo"), MoveFiles: true, HardLink: f.hardLink}
		if err := options.Hooks.BeforeOrganize(ctx, plan); err != nil {
			return embeddedJavspResult{}, err
		}
		if f.afterPlanError != nil {
			return embeddedJavspResult{}, f.afterPlanError
		}
		writeIntakeFixture(t, filepath.Dir(f.target), map[string]string{"movie.nfo": f.nfo})
		if f.afterArtifactError != nil {
			return embeddedJavspResult{}, f.afterArtifactError
		}
		moved, err := options.Hooks.MoveFiles(ctx, plan)
		if err == nil {
			err = f.afterMoveError
		}
		if err != nil {
			return embeddedJavspResult{Failed: 1}, err
		}
		return embeddedJavspResult{Total: 1, Processed: 1, Movies: []JavspMovieOutcome{{MediaID: plan.MediaID, SourcePaths: []string{f.source}, OutputPaths: moved.OutputPaths, NFOPath: plan.NFOPath, Status: "success"}}}, nil
	}))
	var err error
	f.job, err = f.s.db.EnqueueMediaJob(MediaJob{Key: "movie:test", Kind: "movie", Phase: "organize", SourcePath: f.source, Title: "TEST-001", MediaID: "TEST-001"})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestMovieQueueDrainsOnlyExplicitInputAndFinalizesOwnNFO(t *testing.T) {
	f := newMovieQueueFixture(t)
	unrelated := filepath.Join(f.root, "organized", "OTHER-002")
	writeIntakeFixture(t, unrelated, map[string]string{"OTHER-002.mp4": "other", "movie.nfo": "<movie><title>Other</title></movie>"})
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := f.s.db.MediaJob(f.job.ID)
	if err != nil || job.Status != "success" || job.Phase != "done" || len(job.OutputPaths) != 1 || job.OutputPaths[0] != f.target {
		t.Fatalf("movie did not complete: %+v %v", job, err)
	}
	if f.runs != 1 {
		t.Fatalf("engine runs=%d", f.runs)
	}
	if _, err := os.Stat(f.source); !os.IsNotExist(err) {
		t.Fatal("source not moved")
	}
	if data, err := os.ReadFile(filepath.Join(filepath.Dir(f.target), "TEST-001.nfo")); err != nil || string(data) != "<movie><title>Example title</title></movie>" {
		t.Fatalf("missing own NFO: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(unrelated, "OTHER-002.nfo")); !os.IsNotExist(err) {
		t.Fatal("unrelated media was finalized")
	}
}

func TestMovieQueueCommittedMoveResumesWithoutRunningEngineAgain(t *testing.T) {
	f := newMovieQueueFixture(t)
	f.afterMoveError = context.DeadlineExceeded
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := f.s.db.MediaJob(f.job.ID)
	if err != nil || job.Status != "timed_out" {
		t.Fatalf("wrong timeout status: %+v %v", job, err)
	}
	if _, err := os.Stat(f.source); !os.IsNotExist(err) {
		t.Fatal("fixture did not move")
	}
	job, err = f.s.db.RetryMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.db.ClaimMediaJob(job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	var seq int
	var schema, path string
	if err := f.s.db.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &schema, &path); err != nil {
		t.Fatal(err)
	}
	if err := f.s.db.Close(); err != nil {
		t.Fatal(err)
	}
	f.s.db, err = OpenDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.s.db.Close() })
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.MediaJob(job.ID)
	if err != nil || job.Status != "success" || f.runs != 1 {
		t.Fatalf("restart recrawled completed work: %+v runs=%d %v", job, f.runs, err)
	}
}

func TestMovieQueueBusyWaitsAndCollisionDoesNotOverwrite(t *testing.T) {
	f := newMovieQueueFixture(t)
	f.s.mediaFiles.Lock()
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.s.mediaFiles.Unlock()
	if f.runs != 0 {
		t.Fatal("organizer ignored file writer lock")
	}
	writeIntakeFixture(t, filepath.Dir(f.target), map[string]string{"TEST-001.mp4": "different", "movie.nfo": "original metadata"})
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := f.s.db.MediaJob(f.job.ID)
	if err != nil || job.Status != "needs_attention" {
		t.Fatalf("collision status: %+v %v", job, err)
	}
	assertIntakeFixture(t, filepath.Dir(f.target), map[string]string{"TEST-001.mp4": "different", "movie.nfo": "original metadata"})
	assertIntakeFixture(t, filepath.Dir(f.source), map[string]string{"TEST-001.mp4": "video"})
}

func TestMoviePlanCheckpointHonorsCancellationWithoutChangingClaimVersion(t *testing.T) {
	f := newMovieQueueFixture(t)
	job, err := f.s.db.ClaimMediaJob(f.job.ID, f.job.Version)
	if err != nil {
		t.Fatal(err)
	}
	plan := queuedMoviePlan{Root: f.root, Plan: JavspMoviePlan{MediaID: "TEST-001"}}
	if err := f.s.db.SaveMoviePlan(job.ID, job.Version, plan); err != nil {
		t.Fatal(err)
	}
	current, err := f.s.db.MediaJob(job.ID)
	if err != nil || current.Version != job.Version {
		t.Fatalf("checkpoint invalidated cancel token: %+v %v", current, err)
	}
	if _, err := f.s.db.CancelMediaJob(job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	if err := f.s.db.SaveMoviePlan(job.ID, job.Version, plan); !errors.Is(err, errMediaJobConflict) {
		t.Fatalf("canceled worker saved plan: %v", err)
	}
}

func TestMovieQueueRetryCannotOverwriteAnotherJobsArtifacts(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed_B", true: "partial_B"}[partial], func(t *testing.T) {
			f := newMovieQueueFixture(t)
			sourceA := f.source
			f.afterPlanError = errors.New("failed before artifact creation")
			if err := f.s.drainPendingMovies(context.Background()); err != nil {
				t.Fatal(err)
			}
			a, err := f.s.db.MediaJob(f.job.ID)
			if err != nil || a.MoviePlan == nil || a.MoviePlan.MetadataReady || a.Status != "failed" {
				t.Fatalf("A did not persist its unfinished plan: %+v %v", a, err)
			}
			f.source = filepath.Join(f.root, "other", "TEST-001.mp4")
			writeIntakeFixture(t, filepath.Dir(f.source), map[string]string{"TEST-001.mp4": "B video"})
			f.afterPlanError = nil
			f.nfo = "<movie><title>Metadata belonging to B</title></movie>"
			if partial {
				f.afterArtifactError = errors.New("B artifact written but video not moved")
			}
			b, err := f.s.db.EnqueueMediaJob(MediaJob{Key: "movie:other", Kind: "movie", Phase: "organize", SourcePath: f.source})
			if err != nil {
				t.Fatal(err)
			}
			if err := f.s.drainPendingMovies(context.Background()); err != nil {
				t.Fatal(err)
			}
			b, err = f.s.db.MediaJob(b.ID)
			if err != nil || (!partial && b.Status != "success") || (partial && b.Status != "failed") {
				t.Fatalf("B fixture failed: %+v %v", b, err)
			}
			f.source, f.afterArtifactError = sourceA, nil
			f.nfo = "<movie><title>Replacement from A</title></movie>"
			if _, err := f.s.db.RetryMediaJob(a.ID, a.Version); err != nil {
				t.Fatal(err)
			}
			if err := f.s.drainPendingMovies(context.Background()); err != nil {
				t.Fatal(err)
			}
			a, err = f.s.db.MediaJob(a.ID)
			if err != nil || a.Status != "needs_attention" {
				t.Errorf("A must require conflict resolution: %+v %v", a, err)
			}
			assertIntakeFixture(t, filepath.Dir(f.target), map[string]string{"movie.nfo": "<movie><title>Metadata belonging to B</title></movie>"})
			assertIntakeFixture(t, filepath.Dir(sourceA), map[string]string{"TEST-001.mp4": "video"})
		})
	}
}

func TestMovieQueueRejectsOriginalSymlinkBeforeCallingEngine(t *testing.T) {
	f := newMovieQueueFixture(t)
	real := filepath.Join(filepath.Dir(f.source), "OTHER-001.mp4")
	if err := os.Rename(f.source, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, f.source); err != nil {
		t.Fatal(err)
	}
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := f.s.db.MediaJob(f.job.ID)
	if err != nil || job.Status != "needs_attention" || f.runs != 0 {
		t.Fatalf("unsafe input reached engine: %+v runs=%d %v", job, f.runs, err)
	}
	assertIntakeFixture(t, filepath.Dir(real), map[string]string{"OTHER-001.mp4": "video"})
}

func TestMovieQueueHistoryCreationFailureDoesNotStrandClaim(t *testing.T) {
	f := newMovieQueueFixture(t)
	if _, err := f.s.db.db.Exec(`CREATE TRIGGER reject_queue_history BEFORE INSERT ON javsp_runs BEGIN SELECT RAISE(FAIL,'injected history failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := f.s.drainPendingMovies(context.Background()); err == nil {
		t.Fatal("history failure not reported")
	}
	job, err := f.s.db.MediaJob(f.job.ID)
	if err != nil || job.Status != "pending" || f.runs != 0 {
		t.Fatalf("history failure stranded claim: %+v runs=%d %v", job, f.runs, err)
	}
	if _, err := f.s.db.db.Exec(`DROP TRIGGER reject_queue_history`); err != nil {
		t.Fatal(err)
	}
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.MediaJob(f.job.ID)
	if err != nil || job.Status != "success" {
		t.Fatalf("queue did not recover after DB recovery: %+v %v", job, err)
	}
}

func TestMovieQueueResumeHonorsTimeoutWithoutRecrawling(t *testing.T) {
	f := newMovieQueueFixture(t)
	f.afterMoveError = context.DeadlineExceeded
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := f.s.db.MediaJob(f.job.ID)
	if err != nil || job.MoviePlan == nil || !job.MoviePlan.MetadataReady {
		t.Fatalf("missing resume checkpoint: %+v %v", job, err)
	}
	if _, err := f.s.db.RetryMediaJob(job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	config, err := f.s.db.JavspConfigPrivate()
	if err != nil {
		t.Fatal(err)
	}
	// Inject an already exhausted budget directly; public config validation
	// enforces positive values, but all execution branches must apply a budget.
	config.TimeoutSeconds = 0
	if _, err := f.s.db.UpdateJavspConfig(config); err != nil {
		t.Fatal(err)
	}
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.MediaJob(job.ID)
	if err != nil || job.Status != "timed_out" || f.runs != 1 {
		t.Fatalf("resume ignored time budget: %+v runs=%d %v", job, f.runs, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.target), "TEST-001.nfo")); !os.IsNotExist(err) {
		t.Fatal("expired resume wrote final NFO")
	}
}

func TestMovieQueueLinksItsPersistedRunHistory(t *testing.T) {
	f := newMovieQueueFixture(t)
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := f.s.db.MediaJob(f.job.ID)
	if err != nil || job.LastJavspRunID == 0 {
		t.Fatalf("no run linkage: %+v %v", job, err)
	}
	var status string
	if err := f.s.db.db.QueryRow(`SELECT status FROM javsp_runs WHERE id=?`, job.LastJavspRunID).Scan(&status); err != nil || status != "success" {
		t.Fatalf("incorrect linked history: %s %v", status, err)
	}
}

func TestMovieQueueResumeRejectsChangedMetadata(t *testing.T) {
	f := newMovieQueueFixture(t)
	f.afterMoveError = context.DeadlineExceeded
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := f.s.db.MediaJob(f.job.ID)
	if err != nil || job.MoviePlan == nil || !job.MoviePlan.MetadataReady {
		t.Fatalf("missing checkpoint: %+v %v", job, err)
	}
	path := filepath.Join(filepath.Dir(f.target), "movie.nfo")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := "<movie><title>Another title</title></movie>"
	if len(changed) != len(f.nfo) {
		t.Fatal("fixture must defeat size-only metadata validation")
	}
	if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.RetryMediaJob(job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.MediaJob(job.ID)
	if err != nil || job.Status != "needs_attention" || f.runs != 1 {
		t.Fatalf("resume accepted replaced metadata: %+v runs=%d %v", job, f.runs, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.target), "TEST-001.nfo")); !os.IsNotExist(err) {
		t.Fatal("resume published metadata from another writer")
	}
	assertIntakeFixture(t, filepath.Dir(f.target), map[string]string{"TEST-001.mp4": "video", "movie.nfo": changed})
}

func TestMovieQueueCancellationAndShutdownKeepHistoryAccurate(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "shutdown"}[shutdown], func(t *testing.T) {
			f := newMovieQueueFixture(t)
			originalRunner := f.s.javsp
			bundle, data := javspTestRuntime(t)
			entered := make(chan struct{})
			f.s.javsp = newJavspRunnerWithEngine(bundle, data, selectedJavspFixture(func(ctx context.Context, _ embeddedJavspOptions) (embeddedJavspResult, error) {
				close(entered)
				<-ctx.Done()
				return embeddedJavspResult{}, ctx.Err()
			}))
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			if shutdown {
				f.s.runCtx = ctx
			}
			done := make(chan error, 1)
			go func() { done <- f.s.drainPendingMovies(ctx) }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("movie not started")
			}
			job, err := f.s.db.MediaJob(f.job.ID)
			if err != nil || job.Status != "running" {
				t.Fatalf("job not claimed: %+v %v", job, err)
			}
			if shutdown {
				stop()
			} else {
				if _, err := f.s.db.CancelMediaJob(job.ID, job.Version); err != nil {
					t.Fatal(err)
				}
				f.s.cancelMediaJobAttempt(job.ID, job.Version)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled worker did not stop")
			}
			job, err = f.s.db.MediaJob(job.ID)
			want := "canceled"
			if shutdown {
				want = "pending"
			}
			if err != nil || job.Status != want {
				t.Fatalf("wrong queue settlement: %+v %v", job, err)
			}
			var historyStatus string
			if err := f.s.db.db.QueryRow(`SELECT status FROM javsp_runs WHERE id=?`, job.LastJavspRunID).Scan(&historyStatus); err != nil || historyStatus != "canceled" {
				t.Errorf("ended attempt must not be pending in history: %s %v", historyStatus, err)
			}
			assertIntakeFixture(t, filepath.Dir(f.source), map[string]string{"TEST-001.mp4": "video"})
			f.s.runCtx = nil
			f.s.javsp = originalRunner
			if !shutdown {
				if _, err := f.s.db.RetryMediaJob(job.ID, job.Version); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.s.drainPendingMovies(context.Background()); err != nil {
				t.Fatal(err)
			}
			job, err = f.s.db.MediaJob(job.ID)
			if err != nil || job.Status != "success" {
				t.Fatalf("could not resume after canceled attempt: %+v %v", job, err)
			}
		})
	}
}
