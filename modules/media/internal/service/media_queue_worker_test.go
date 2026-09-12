package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMediaQueueWorkerDrainsWithDiscoveryScheduleDisabled(t *testing.T) {
	f := newIntakeQueueFixture(t)
	config, err := f.s.db.IntakeConfig()
	if err != nil || config.Enabled {
		t.Fatalf("test needs disabled discovery: %+v %v", config, err)
	}
	f.s.mediaFiles.Lock()
	_, err = f.s.runIntake(context.Background(), "manual")
	f.s.mediaFiles.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); f.s.queueWorker(ctx, 10*time.Millisecond) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("queue worker did not stop")
		}
	}()
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("pending intake never resumed without another manual run")
		case <-tick.C:
			jobs, err := f.s.db.ListMediaJobs("success", 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(jobs) == 1 && jobs[0].Phase == "done" {
				return
			}
		}
	}
}

func TestMediaQueueWorkerDrainsMoviesDespiteQBFailure(t *testing.T) {
	f := newMovieQueueFixture(t)
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "qB unavailable", http.StatusServiceUnavailable)
	}))
	defer qb.Close()
	if _, err := f.s.db.UpdateSettings(map[string]any{"qb_host": qb.URL, "qb_username": "test", "qb_password": "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.EnqueueMediaJob(MediaJob{Key: "torrent:offline", Kind: "torrent", Phase: "qb_cleanup", TorrentHash: "offline"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); f.s.queueWorker(ctx, 10*time.Millisecond) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("queue did not stop")
		}
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("queued movie was not consumed independently of qB")
		case <-ticker.C:
			job, err := f.s.db.MediaJob(f.job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if job.Status == "success" {
				return
			}
		}
	}
}

func TestMediaQueueWorkerIdleDoesNotDiscoverOrScan(t *testing.T) {
	f := newIntakeQueueFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	f.s.queueWorker(ctx, 10*time.Millisecond)
	jobs, err := f.s.db.ListMediaJobs("", 10)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("startup discovered unsolicited jobs: %+v %v", jobs, err)
	}
	if f.stops.Load() != 0 || f.deletes.Load() != 0 {
		t.Fatal("startup changed download tasks")
	}
}
