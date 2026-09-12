package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	javspengine "github.com/yuukiy/javsp-go/engine"
)

func configureIntakeJavsp(t *testing.T, f *intakeQueueFixture) JavspConfig {
	t.Helper()
	config, err := f.s.db.IntakeConfig()
	if err != nil {
		t.Fatal(err)
	}
	config.RunJavsp = true
	if _, err := f.s.db.UpdateIntakeConfig(config); err != nil {
		t.Fatal(err)
	}
	cfg := defaultJavspConfig()
	cfg.InputDirectory, cfg.MinimumSize = filepath.Dir(f.target), "0B"
	if _, err := f.s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	bundle, data := javspTestRuntime(t)
	f.s.javsp = NewJavspRunner(bundle, data)
	return cfg
}

func TestMovieClaimsWaitForUnpublishedIntakeWithoutBlockingOtherJobs(t *testing.T) {
	db, _ := newQueueTestDB(t)
	first, err := db.EnqueueMediaJob(MediaJob{Key: "movie:waiting", Kind: "movie", Phase: "organize", SourcePath: "/pending/pack/TEST-001.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.EnqueueMediaJob(MediaJob{Key: "movie:independent", Kind: "movie", Phase: "organize", SourcePath: "/pending/pack2/TEST-002.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := db.PendingMediaJobs("movie", 1)
	if err != nil || len(listed) != 1 || listed[0].ID != first.ID {
		t.Fatalf("fixture: %+v %v", listed, err)
	}
	// Intake was registered after the worker's selection, before its claim.
	parent, err := db.EnqueueMediaJob(MediaJob{Key: "torrent:parent", Kind: "torrent", Phase: "qb_cleanup", TargetPath: "/pending/pack", Organize: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.claimMovieWithHistory(first.ID, first.Version); !errors.Is(err, errMediaJobConflict) {
		t.Fatalf("unpublished intake files were claimed: %v", err)
	}
	listed, err = db.PendingMediaJobs("movie", 1)
	if err != nil || len(listed) != 1 || listed[0].ID != second.ID {
		t.Fatalf("blocked head starved independent film: %+v %v", listed, err)
	}
	history, err := db.ListJavspRuns(10)
	if err != nil || len(history) != 0 {
		t.Fatalf("blocked claim left running history: %+v %v", history, err)
	}
	parent, err = db.ClaimMediaJob(parent.ID, parent.Version)
	if err != nil {
		t.Fatal(err)
	}
	first.ParentID = parent.ID
	ready, created, err := db.saveIntakeMovies(context.Background(), parent.ID, parent.Version, []MediaJob{first})
	if err != nil || created != 0 || len(ready.ChildJobIDs) != 1 || ready.ChildJobIDs[0] != first.ID {
		t.Fatalf("manual job was not adopted: %+v created=%d %v", ready, created, err)
	}
	listed, err = db.PendingMediaJobs("movie", 1)
	if err != nil || len(listed) != 1 || listed[0].ID != first.ID {
		t.Fatalf("published film remained blocked: %+v %v", listed, err)
	}
	claimed, _, err := db.claimMovieWithHistory(first.ID, first.Version)
	if err != nil || claimed.ParentID != parent.ID {
		t.Fatalf("handoff lost original movie identity: %+v %v", claimed, err)
	}
}

func TestMovieIntakeWaitAfterCancellationDependsOnFileOwnership(t *testing.T) {
	for _, tc := range []struct {
		name    string
		phase   string
		claim   bool
		blocked bool
	}{
		{"transfer canceled before claim", "transfer", false, false},
		{"transfer canceled after claim", "transfer", true, true},
		{"moved but unpublished", "qb_cleanup", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, dbPath := newQueueTestDB(t)
			movie, err := db.EnqueueMediaJob(MediaJob{Key: "movie", Kind: "movie", Phase: "organize", SourcePath: "/pending/pack/TEST-001.mp4"})
			if err != nil {
				t.Fatal(err)
			}
			parent, err := db.EnqueueMediaJob(MediaJob{Key: "intake", Kind: "torrent", Phase: tc.phase, TargetPath: "/pending/pack", Organize: true})
			if err != nil {
				t.Fatal(err)
			}
			if tc.claim {
				parent, err = db.ClaimMediaJob(parent.ID, parent.Version)
				if err != nil {
					t.Fatal(err)
				}
			}
			parent, err = db.CancelMediaJob(parent.ID, parent.Version)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = OpenDatabase(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			pending, err := db.PendingMediaJobs("movie", 1)
			if err != nil || (len(pending) == 0) != tc.blocked {
				t.Fatalf("canceled intake reservation: blocked=%v pending=%+v error=%v", tc.blocked, pending, err)
			}
			views := []MediaQueueJob{{MediaJob: movie}}
			if err := db.annotateQueueWaits(context.Background(), views); err != nil || (len(views[0].WaitingForIntake) > 0) != tc.blocked {
				t.Fatalf("view disagrees with executable wait: %+v %v", views, err)
			}
			if tc.blocked {
				if _, _, err := db.claimMovieWithHistory(movie.ID, movie.Version); !errors.Is(err, errMediaJobConflict) {
					t.Fatalf("canceled owner no longer protects its files: %v", err)
				}
				return
			}
			// A retried intake reserves the destination again. The worker must
			// recheck its previously selected movie at claim time.
			parent, err = db.RetryMediaJob(parent.ID, parent.Version)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := db.claimMovieWithHistory(movie.ID, movie.Version); !errors.Is(err, errMediaJobConflict) {
				t.Fatalf("retried intake did not reacquire reservation: %v", err)
			}
			if _, err := db.CancelMediaJob(parent.ID, parent.Version); err != nil {
				t.Fatal(err)
			}
			if _, _, err := db.claimMovieWithHistory(movie.ID, movie.Version); err != nil {
				t.Fatalf("unclaimed cancellation permanently blocks movie: %v", err)
			}
		})
	}
}

func restartIntakeQueueFixture(t *testing.T, f *intakeQueueFixture) {
	t.Helper()
	var dbPath, schema string
	var seq int
	if err := f.s.db.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &schema, &dbPath); err != nil {
		t.Fatal(err)
	}
	if err := f.s.db.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.s.db, err = OpenDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.s.db.Close() })
}

func TestMovieWaitSurvivesCanceledTransferWithDoneReceiptAndStalePhase(t *testing.T) {
	f := newIntakeQueueFixture(t)
	cfg := configureIntakeJavsp(t, f)
	f.s.mediaFiles.Lock()
	_, err := f.s.runIntake(context.Background(), "manual")
	f.s.mediaFiles.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := f.s.db.PendingMediaJobs("torrent", 1)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("missing intake fixture: %+v %v", jobs, err)
	}
	parent, err := f.s.db.ClaimMediaJob(jobs[0].ID, jobs[0].Version)
	if err != nil {
		t.Fatal(err)
	}
	item := IntakeCandidate{JobID: parent.ID, Hash: parent.TorrentHash, Source: parent.SourcePath, Destination: parent.TargetPath}
	if outcome, err := moveIntakeCandidateContext(context.Background(), item, parent.IntakeSourceRoot, parent.IntakeTargetRoot); err != nil || outcome != "moved" {
		t.Fatalf("receipt fixture did not finish moving: %q %v", outcome, err)
	}
	// Simulate exit after the durable done receipt, before the DB phase update.
	restartIntakeQueueFixture(t, f)
	parent, err = f.s.db.MediaJob(parent.ID)
	if err != nil || parent.Phase != "transfer" || parent.Status != "pending" {
		t.Fatalf("bad crash fixture: %+v %v", parent, err)
	}
	parent, err = f.s.db.ClaimMediaJob(parent.ID, parent.Version)
	if err != nil {
		t.Fatal(err)
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
	f.failInfo.Store(true)
	parent, err = f.s.transferQueuedIntake(context.Background(), session, nil, parent, &IntakeRunResult{})
	if err != nil || parent.Phase != "transfer" || parent.Status != "failed" || parent.HoldFiles {
		t.Fatalf("failed recovery fixture: %+v %v", parent, err)
	}
	parent, err = f.s.db.CancelMediaJob(parent.ID, parent.Version)
	if err != nil {
		t.Fatal(err)
	}
	movie, err := movieDiscoveryJob(context.Background(), cfg.InputDirectory, "TEST-001", []string{parent.TargetPath}, 0)
	if err != nil {
		t.Fatal(err)
	}
	movie, err = f.s.db.EnqueueMediaJob(movie)
	if err != nil {
		t.Fatal(err)
	}
	restartIntakeQueueFixture(t, f)
	jobs, err = f.s.db.PendingMediaJobs("movie", 1)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("done receipt's unpublished files were released after cancel: %+v %v", jobs, err)
	}
	if _, _, err := f.s.db.claimMovieWithHistory(movie.ID, movie.Version); !errors.Is(err, errMediaJobConflict) {
		t.Fatalf("stale transfer allowed movie claim: %v", err)
	}
	views := []MediaQueueJob{{MediaJob: movie}}
	if err := f.s.db.annotateQueueWaits(context.Background(), views); err != nil || len(views[0].WaitingForIntake) != 1 || views[0].WaitingForIntake[0] != parent.ID {
		t.Fatalf("stale transfer wait missing from API: %+v %v", views, err)
	}
	if _, err := f.s.db.RetryMediaJob(parent.ID, parent.Version); err != nil {
		t.Fatal(err)
	}
	f.failInfo.Store(false)
	result, err := f.s.runIntake(context.Background(), "manual")
	if err != nil || result.Failed != 0 || result.TasksDeleted != 1 || result.JavspQueued != 0 {
		t.Fatalf("retry did not resume/adopt existing movie: %+v %v", result, err)
	}
	parent, err = f.s.db.MediaJob(parent.ID)
	if err != nil || parent.Status != "success" || !parent.IntakeMoviesReady || len(parent.ChildJobIDs) != 1 || parent.ChildJobIDs[0] != movie.ID {
		t.Fatalf("recovery lost handoff: %+v %v", parent, err)
	}
	if _, _, err := f.s.db.claimMovieWithHistory(movie.ID, movie.Version); err != nil {
		t.Fatalf("recovered movie remains blocked: %v", err)
	}
}

func TestIntakePublishesGroupedMoviesBeforeQBCleanupAndDoesNotDiscoverAgainOnRetry(t *testing.T) {
	f := newIntakeQueueFixture(t)
	f.source = filepath.Join(filepath.Dir(f.source), "pack")
	f.target = filepath.Join(filepath.Dir(f.target), "pack")
	writeIntakeFixture(t, f.source, map[string]string{"TEST-001.1.mp4": "first", "TEST-001.2.mp4": "second", "[未识别].mp4": "unknown"})
	cfg := configureIntakeJavsp(t, f)
	var discoveries atomic.Int32
	f.s.discoverMovies = func(ctx context.Context, options javspengine.Options) (javspengine.DiscoveryResult, error) {
		discoveries.Add(1)
		return javspengine.Discover(ctx, options)
	}
	f.failDelete.Store(true)
	result, err := f.s.runIntake(context.Background(), "manual")
	if err != nil || result.Failed != 1 || result.JavspQueued != 2 {
		t.Fatalf("qB failure lost grouped queue: %+v %v", result, err)
	}
	jobs, err := f.s.db.ListMediaJobs("", 10)
	if err != nil || len(jobs) != 3 {
		t.Fatalf("missing parent or grouped films: %+v %v", jobs, err)
	}
	var parent, movie MediaJob
	for _, job := range jobs {
		if job.Kind == "torrent" {
			parent = job
			continue
		}
		if job.MediaID == "TEST-001" {
			movie = job
			if len(job.SourcePaths) != 2 || len(job.SourceIdentities) != 2 || job.Status != "pending" {
				t.Fatalf("multipart selection was split: %+v", job)
			}
		} else if job.MediaID != "" || job.Status != "needs_attention" {
			t.Fatalf("unknown input disappeared: %+v", job)
		}
	}
	if movie.ParentID != parent.ID || parent.Phase != "qb_cleanup" || parent.Status != "failed" {
		t.Fatalf("lost intake lineage/state: parent=%+v movie=%+v", parent, movie)
	}
	if discoveries.Load() != 1 {
		t.Fatal("intake did not use bounded discovery")
	}
	if _, err := f.s.startJavsp(context.Background(), "manual"); err != nil {
		t.Fatal(err)
	}
	f.s.wait.Wait()
	jobs, err = f.s.db.ListMediaJobs("", 10)
	if err != nil || len(jobs) != 3 {
		t.Fatalf("manual discovery duplicated intake films: %+v %v", jobs, err)
	}
	// Execute the published multipart film while qB cleanup remains failed.
	bundle, data := javspTestRuntime(t)
	f.s.javsp = newJavspRunnerWithEngine(bundle, data, selectedJavspFixture(func(ctx context.Context, options embeddedJavspOptions) (embeddedJavspResult, error) {
		root, err := filepath.EvalSymlinks(cfg.InputDirectory)
		if err != nil {
			return embeddedJavspResult{}, err
		}
		out := filepath.Join(root, "#整理完成", "TEST-001")
		plan := JavspMoviePlan{MediaID: "TEST-001", MoveFiles: true, NFOPath: filepath.Join(out, "movie.nfo")}
		for _, source := range options.InputFiles {
			plan.Files = append(plan.Files, JavspFileMove{Source: source, Destination: filepath.Join(out, filepath.Base(source))})
		}
		if err := options.Hooks.BeforeOrganize(ctx, plan); err != nil {
			return embeddedJavspResult{}, err
		}
		writeIntakeFixture(t, out, map[string]string{"movie.nfo": "<movie><title>Example</title></movie>"})
		if _, err := options.Hooks.MoveFiles(ctx, plan); err != nil {
			return embeddedJavspResult{}, err
		}
		return embeddedJavspResult{Total: 1, Processed: 1}, nil
	}))
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	movie, err = f.s.db.MediaJob(movie.ID)
	if err != nil || movie.Status != "success" {
		t.Fatalf("qB failure blocked movie execution: %+v %v", movie, err)
	}
	for _, source := range movie.SourcePaths {
		if _, err := os.Stat(source); !os.IsNotExist(err) {
			t.Fatal("movie was not organized")
		}
	}
	parent, err = f.s.db.RetryMediaJob(parent.ID, parent.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.ClaimMediaJob(parent.ID, parent.Version); err != nil {
		t.Fatal(err)
	}
	var dbPath, schema string
	var seq int
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
	t.Cleanup(func() { _ = f.s.db.Close() })
	f.failDelete.Store(false)
	before := discoveries.Load()
	result, err = f.s.runIntake(context.Background(), "manual")
	if err != nil || result.TasksDeleted != 1 || result.Moved != 0 || result.JavspQueued != 0 || discoveries.Load() != before || f.stops.Load() != 1 {
		t.Fatalf("restart repeated a completed phase: %+v %v discoveries=%d", result, err, discoveries.Load())
	}
	parent, err = f.s.db.MediaJob(parent.ID)
	if err != nil || parent.Status != "success" || parent.Phase != "done" {
		t.Fatalf("cleanup did not finish: %+v %v", parent, err)
	}
}

func TestIntakePublicationStorageFailureRollsBackChildrenAndNeverDeletesQB(t *testing.T) {
	f := newIntakeQueueFixture(t)
	configureIntakeJavsp(t, f)
	if _, err := f.s.db.db.Exec(`CREATE TRIGGER reject_intake_handoff BEFORE UPDATE ON media_jobs
 WHEN NEW.kind='torrent' AND json_extract(NEW.payload,'$.intake_movies_ready')=1
 BEGIN SELECT RAISE(FAIL,'injected checkpoint failure'); END`); err != nil {
		t.Fatal(err)
	}
	result, err := f.s.runIntake(context.Background(), "manual")
	if err != nil || result.Failed != 1 || result.JavspQueued != 0 || f.deletes.Load() != 0 {
		t.Fatalf("storage failure allowed cleanup: %+v %v", result, err)
	}
	jobs, err := f.s.db.ListMediaJobs("", 10)
	if err != nil || len(jobs) != 1 || jobs[0].IntakeMoviesReady || len(jobs[0].ChildJobIDs) != 0 || jobs[0].Phase != "qb_cleanup" {
		t.Fatalf("partially published handoff: %+v %v", jobs, err)
	}
	if _, err := os.Stat(f.target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.source); !os.IsNotExist(err) {
		t.Fatal("fixture did not finish moving")
	}
	if _, err := f.s.db.db.Exec(`DROP TRIGGER reject_intake_handoff`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.RetryMediaJob(jobs[0].ID, jobs[0].Version); err != nil {
		t.Fatal(err)
	}
	result, err = f.s.runIntake(context.Background(), "manual")
	if err != nil || result.TasksDeleted != 1 || result.JavspQueued != 1 || result.Moved != 0 || f.stops.Load() != 1 {
		t.Fatalf("storage retry repeated transfer: %+v %v", result, err)
	}
}

func TestIntakePublicationCancellationDoesNotSpawnChildrenOrDeleteQB(t *testing.T) {
	f := newIntakeQueueFixture(t)
	configureIntakeJavsp(t, f)
	f.s.discoverMovies = func(ctx context.Context, options javspengine.Options) (javspengine.DiscoveryResult, error) {
		result, err := javspengine.Discover(ctx, options)
		if err != nil {
			return result, err
		}
		jobs, err := f.s.db.ListMediaJobs("running", 10)
		if err != nil || len(jobs) != 1 {
			return result, fmt.Errorf("fixture running job: %v", err)
		}
		_, err = f.s.db.CancelMediaJob(jobs[0].ID, jobs[0].Version)
		return result, err
	}
	if _, err := f.s.runIntake(context.Background(), "manual"); err != nil {
		t.Fatal(err)
	}
	jobs, err := f.s.db.ListMediaJobs("", 10)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "canceled" || jobs[0].IntakeMoviesReady || f.deletes.Load() != 0 {
		t.Fatalf("canceled claim published work: %+v %v", jobs, err)
	}
}
