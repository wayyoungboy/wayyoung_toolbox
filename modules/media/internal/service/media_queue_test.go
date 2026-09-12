package service

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func newQueueTestDB(t *testing.T) (*Database, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tasks.db")
	db, err := OpenDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func enqueueQueueTestJob(t *testing.T, db *Database, key string) MediaJob {
	t.Helper()
	job, err := db.EnqueueMediaJob(MediaJob{Key: key, Kind: "torrent", MediaID: "TEST-001", Title: "example", TorrentHash: "abcdef", RSSItemKey: "rss-item", SourceURL: "https://example.invalid/entry", SourcePath: "/downloads/movie.mp4", TargetPath: "/media/pending/movie.mp4", Phase: "transfer"})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestMediaQueueEnqueueIsIdempotentAndKeepsProvenance(t *testing.T) {
	db, _ := newQueueTestDB(t)
	first := enqueueQueueTestJob(t, db, "torrent:abc")
	repeat, err := db.EnqueueMediaJob(MediaJob{Key: first.Key, Kind: "torrent", Phase: "transfer", Title: "should not overwrite"})
	if err != nil || repeat.ID != first.ID || repeat.SourcePath != first.SourcePath || repeat.Title != first.Title || repeat.RSSItemKey != first.RSSItemKey {
		t.Fatalf("lost provenance: %+v %v", repeat, err)
	}
	events, err := db.MediaJobEvents(first.ID)
	if err != nil || len(events) != 1 {
		t.Fatalf("duplicate created events: %+v %v", events, err)
	}
}

func TestMediaQueueRetriesOnlyUnfinishedPhaseAndRecoversRestart(t *testing.T) {
	db, path := newQueueTestDB(t)
	job := enqueueQueueTestJob(t, db, "torrent:restart")
	claimed, err := db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	job, err = db.TransitionMediaJob(claimed.ID, claimed.Version, MediaJobTransition{Phase: "qb_cleanup", Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	job, err = db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	job, err = db.TransitionMediaJob(job.ID, job.Version, MediaJobTransition{Phase: "qb_cleanup", Status: "failed", Error: "qB unavailable"})
	if err != nil {
		t.Fatal(err)
	}
	job, err = db.RetryMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	if job.Phase != "qb_cleanup" || job.Status != "pending" {
		t.Fatalf("repeated file move: %+v", job)
	}
	job, err = db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := reopened.MediaJob(job.ID)
	if err != nil || recovered.Phase != "qb_cleanup" || recovered.Status != "pending" || recovered.Version <= job.Version || recovered.Attempts != 3 {
		t.Fatalf("bad recovery: %+v %v", recovered, err)
	}
	events, err := reopened.MediaJobEvents(job.ID)
	if err != nil || events[len(events)-1].Reason != "restart_recovery" {
		t.Fatalf("no recovery event: %+v %v", events, err)
	}
}

func TestMediaQueueOneClaimAndStaleWorkerCannotUndoCancel(t *testing.T) {
	db, _ := newQueueTestDB(t)
	job := enqueueQueueTestJob(t, db, "torrent:claim")
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := db.ClaimMediaJob(job.ID, job.Version); err == nil {
				wins.Add(1)
			} else if !errors.Is(err, errMediaJobConflict) {
				t.Errorf("claim: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("claims=%d", wins.Load())
	}
	claimed, err := db.MediaJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := db.CancelMediaJob(claimed.ID, claimed.Version)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != "canceled" {
		t.Fatal(canceled)
	}
	if _, err := db.TransitionMediaJob(claimed.ID, claimed.Version, MediaJobTransition{Phase: "qb_cleanup", Status: "pending"}); !errors.Is(err, errMediaJobConflict) {
		t.Fatalf("stale worker accepted: %v", err)
	}
	latest, err := db.MediaJob(job.ID)
	if err != nil || latest.Status != "canceled" || latest.Phase != "transfer" {
		t.Fatalf("cancel lost: %+v %v", latest, err)
	}
}

func TestMediaQueueTerminalStatusesAndTransitionValidation(t *testing.T) {
	db, _ := newQueueTestDB(t)
	for _, status := range []string{"failed", "partial", "timed_out", "needs_attention"} {
		t.Run(status, func(t *testing.T) {
			job := enqueueQueueTestJob(t, db, "torrent:"+status)
			job, err := db.ClaimMediaJob(job.ID, job.Version)
			if err != nil {
				t.Fatal(err)
			}
			job, err = db.TransitionMediaJob(job.ID, job.Version, MediaJobTransition{Phase: job.Phase, Status: status, Error: "reason"})
			if err != nil {
				t.Fatal(err)
			}
			if job.Status != status || job.Error != "reason" {
				t.Fatal(job)
			}
			job, err = db.RetryMediaJob(job.ID, job.Version)
			if err != nil || job.Phase != "transfer" || job.Error != "" {
				t.Fatalf("retry: %+v %v", job, err)
			}
		})
	}
	job := enqueueQueueTestJob(t, db, "torrent:validation")
	job, err := db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []MediaJobTransition{{Phase: "download", Status: "pending"}, {Phase: "done", Status: "success"}, {Phase: "transfer", Status: "success"}, {Phase: "transfer", Status: "invented"}} {
		if _, err := db.TransitionMediaJob(job.ID, job.Version, change); err == nil {
			t.Fatalf("invalid transition accepted: %+v", change)
		}
	}
	job, err = db.TransitionMediaJob(job.ID, job.Version, MediaJobTransition{Phase: "qb_cleanup", Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	job, err = db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	job, err = db.TransitionMediaJob(job.ID, job.Version, MediaJobTransition{Phase: "done", Status: "success"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RetryMediaJob(job.ID, job.Version); err == nil {
		t.Fatal("successful job restarted")
	}
}

func TestMediaQueueMigrationPreservesSettingsAndHistory(t *testing.T) {
	db, path := newQueueTestDB(t)
	if _, err := db.db.Exec(`INSERT INTO settings(key,value,updated_at) VALUES('private-custom','"keep"','old')`); err != nil {
		t.Fatal(err)
	}
	run, err := db.StartMaintenanceRun("intake", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishMaintenanceRun(run, "success", 1, 1, 1, []string{"original"}, ""); err != nil {
		t.Fatal(err)
	}
	job := enqueueQueueTestJob(t, db, "torrent:keep")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var value string
	if err := reopened.db.QueryRow(`SELECT value FROM settings WHERE key='private-custom'`).Scan(&value); err != nil || value != `"keep"` {
		t.Fatalf("setting lost: %q %v", value, err)
	}
	runs, err := reopened.ListMaintenanceRuns(10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("history lost: %+v %v", runs, err)
	}
	if got, err := reopened.MediaJob(job.ID); err != nil || got.Status != "pending" || got.Version != job.Version {
		t.Fatalf("pending job changed: %+v %v", got, err)
	}
}

func TestMediaQueueSecondInstanceCannotStealClaims(t *testing.T) {
	db, path := newQueueTestDB(t)
	job := enqueueQueueTestJob(t, db, "torrent:owner")
	job, err := db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenDatabase(path)
	if err == nil {
		second.Close()
		t.Fatal("second process revoked active claims")
	}
	current, err := db.MediaJob(job.ID)
	if err != nil || current.Version != job.Version || current.Status != "running" {
		t.Fatalf("claim stolen: %+v %v", current, err)
	}
}

func TestMediaQueueCapturedFilesBlockOtherWritersUntilRecovered(t *testing.T) {
	db, _ := newQueueTestDB(t)
	job := enqueueQueueTestJob(t, db, "torrent:files")
	job, err := db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	if blocked, err := db.MediaFilesBlocked(); err != nil || !blocked {
		t.Fatalf("claimed transfer not guarded: %v %v", blocked, err)
	}
	job, err = db.TransitionMediaJob(job.ID, job.Version, MediaJobTransition{Phase: "transfer", Status: "needs_attention", HoldFiles: true})
	if err != nil {
		t.Fatal(err)
	}
	job, err = db.CancelMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	if blocked, err := db.MediaFilesBlocked(); err != nil || !blocked {
		t.Fatalf("cancel released captured files: %v %v", blocked, err)
	}
	job, err = db.RetryMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	job, err = db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.TransitionMediaJob(job.ID, job.Version, MediaJobTransition{Phase: "qb_cleanup", Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	if blocked, err := db.MediaFilesBlocked(); err != nil || blocked {
		t.Fatalf("finished file transaction held forever: %v %v", blocked, err)
	}
}

func TestMediaQueueIntakePublicationAndCompletionUseSeparateCheckpoints(t *testing.T) {
	db, _ := newQueueTestDB(t)
	job, err := db.EnqueueMediaJob(MediaJob{Key: "parent", Kind: "torrent", Phase: "transfer", Organize: true})
	if err != nil {
		t.Fatal(err)
	}
	job, err = db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	job, err = db.TransitionMediaJob(job.ID, job.Version, MediaJobTransition{Phase: "qb_cleanup", Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	job, err = db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	child := MediaJob{Key: "child", Kind: "movie", ParentID: job.ID, Phase: "organize", SourcePath: "/pending/movie.mp4"}
	if _, err := db.CompleteIntakeJob(job.ID, job.Version); err == nil {
		t.Fatal("completion allowed without persistent movie handoff")
	}
	if _, _, err := db.saveIntakeMovies(context.Background(), job.ID, job.Version, []MediaJob{child, {}}); err == nil {
		t.Fatal("bad child accepted")
	}
	jobs, err := db.ListMediaJobs("", 10)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "running" {
		t.Fatalf("partial transaction committed: %+v %v", jobs, err)
	}
	oldVersion := job.Version
	job, err = db.CancelMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.saveIntakeMovies(context.Background(), job.ID, oldVersion, []MediaJob{child}); !errors.Is(err, errMediaJobConflict) {
		t.Fatalf("canceled parent spawned child: %v", err)
	}
	job, err = db.RetryMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	job, err = db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	claimedVersion := job.Version
	job, created, err := db.saveIntakeMovies(context.Background(), job.ID, claimedVersion, []MediaJob{child})
	if err != nil || created != 1 || job.Status != "running" || !job.IntakeMoviesReady || job.Version != claimedVersion || len(job.ChildJobIDs) != 1 {
		t.Fatalf("publication finished intake or lost claim/checkpoint: %+v %d %v", job, created, err)
	}
	// Cancellation after publication must invalidate completion but must not
	// discard the children or repeat their publication on a cleanup-only retry.
	job, err = db.CancelMediaJob(job.ID, claimedVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CompleteIntakeJob(job.ID, claimedVersion); !errors.Is(err, errMediaJobConflict) {
		t.Fatalf("stale worker completed canceled intake: %v", err)
	}
	job, err = db.RetryMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	job, err = db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	job, err = db.CompleteIntakeJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err = db.ListMediaJobs("", 10)
	if err != nil || len(jobs) != 2 || job.Status != "success" {
		t.Fatalf("completion failed: %+v %v", jobs, err)
	}
}
