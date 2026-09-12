package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type downloadFixture struct {
	s        *Service
	job      MediaJob
	mu       sync.Mutex
	torrents []QBTorrent
	fail     bool
	requests atomic.Int32
	gate     <-chan struct{}
	entered  chan struct{}
}

func newDownloadFixture(t *testing.T) *downloadFixture {
	t.Helper()
	f := &downloadFixture{s: testService(t), entered: make(chan struct{}, 8)}
	hash := "0123456789abcdef0123456789abcdef01234567"
	f.torrents = []QBTorrent{{Hash: hash, AddedOn: 42, Name: "TEST-001", ContentPath: "/media/downloads/TEST-001.mp4", Progress: .25, AmountLeft: 750, State: "downloading"}}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		if r.URL.Path == "/api/v2/auth/login" {
			fmt.Fprint(w, "Ok.")
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/torrents/info" {
			t.Errorf("download observer performed unexpected action: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", 400)
			return
		}
		f.mu.Lock()
		rows := append([]QBTorrent(nil), f.torrents...)
		fail, gate := f.fail, f.gate
		f.mu.Unlock()
		if gate != nil {
			select {
			case f.entered <- struct{}{}:
			default:
			}
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		if fail {
			http.Error(w, "temporarily unavailable", 503)
			return
		}
		if rows == nil {
			rows = []QBTorrent{}
		}
		if err := json.NewEncoder(w).Encode(rows); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(remote.Close)
	if _, err := f.s.db.UpdateSettings(map[string]any{"qb_host": remote.URL, "qb_username": "fixture", "qb_password": "fixture"}); err != nil {
		t.Fatal(err)
	}
	var err error
	f.job, err = f.s.db.EnqueueMediaJob(MediaJob{Key: "download:test", Kind: "torrent", Phase: "download", Title: "TEST-001", TorrentHash: hash, TorrentAddedOn: 42, SourcePath: f.torrents[0].ContentPath})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestDownloadObserverPersistsProgressWithoutActionsAndSurvivesRestart(t *testing.T) {
	f := newDownloadFixture(t)
	before, _ := f.s.db.MediaJobEvents(f.job.ID)
	for _, progress := range []float64{.25, 1} {
		f.mu.Lock()
		f.torrents[0].Progress = progress
		if progress == 1 {
			f.torrents[0].AmountLeft, f.torrents[0].State = 0, "stalledUP"
		}
		f.mu.Unlock()
		if err := f.s.refreshQueuedDownloads(context.Background()); err != nil {
			t.Fatal(err)
		}
		job, err := f.s.db.MediaJob(f.job.ID)
		if err != nil || job.Download == nil || job.Download.Progress != progress || job.Download.Complete != (progress == 1) || job.Download.ObservedAt == "" || job.Download.Error != "" {
			t.Fatalf("progress not recorded: %+v %v", job, err)
		}
		if job.Status != "pending" || job.Phase != "download" || job.Version != f.job.Version || job.Attempts != 0 {
			t.Fatalf("read-only observer changed execution: %+v", job)
		}
	}
	after, _ := f.s.db.MediaJobEvents(f.job.ID)
	if len(after) != len(before) {
		t.Fatal("progress polls created workflow events")
	}
	var seq int
	var name, path string
	if err := f.s.db.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := f.s.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := OpenDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	job, err := db.MediaJob(f.job.ID)
	if err != nil || job.Download == nil || !job.Download.Complete {
		t.Fatal("restart lost download observation", err)
	}
}

func TestDownloadObserverFailureRetainsLastReadingAndRecovers(t *testing.T) {
	f := newDownloadFixture(t)
	if err := f.s.refreshQueuedDownloads(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.fail = true
	f.mu.Unlock()
	if err := f.s.refreshQueuedDownloads(context.Background()); err == nil {
		t.Fatal("network failure hidden")
	}
	job, err := f.s.db.MediaJob(f.job.ID)
	if err != nil || job.Status != "pending" || job.Download == nil || job.Download.Error == "" || job.Download.Progress != .25 || job.Download.ObservedAt == "" {
		t.Fatalf("failure erased last reading: %+v %v", job, err)
	}
	f.mu.Lock()
	f.fail = false
	f.torrents[0].Progress = .5
	f.mu.Unlock()
	if err := f.s.refreshQueuedDownloads(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, _ = f.s.db.MediaJob(f.job.ID)
	if job.Download.Error != "" || job.Download.Progress != .5 {
		t.Fatal("observation did not recover")
	}
}

func TestDownloadObserverWaitsForMetadataPathAndThenContinues(t *testing.T) {
	f := newDownloadFixture(t)
	for _, path := range []string{"", "/media/downloads/TEST-001.mp4"} {
		f.mu.Lock()
		f.torrents[0].ContentPath = path
		f.torrents[0].Progress, f.torrents[0].AmountLeft = 1, 0
		f.torrents[0].State = "stalledUP"
		f.mu.Unlock()
		if err := f.s.refreshQueuedDownloads(context.Background()); err != nil {
			t.Fatal(err)
		}
		job, err := f.s.db.MediaJob(f.job.ID)
		if err != nil || job.Status != "pending" || job.Version != f.job.Version || job.Download == nil || job.Download.Error != "" || job.Download.Complete != (path != "") {
			t.Fatalf("unknown path blocked eventual completion: %+v %v", job, err)
		}
	}
	for _, path := range []string{"relative.mp4", "/media/../escape.mp4", "/media/\x00bad.mp4"} {
		f := newDownloadFixture(t)
		f.mu.Lock()
		f.torrents[0].ContentPath = path
		f.mu.Unlock()
		if err := f.s.refreshQueuedDownloads(context.Background()); err != nil {
			t.Fatal(err)
		}
		job, err := f.s.db.MediaJob(f.job.ID)
		if err != nil || job.Status != "needs_attention" {
			t.Fatalf("invalid nonempty path accepted: %+v %v", job, err)
		}
	}
}

func TestDownloadObserverMissingOrReplacedTaskNeedsAttention(t *testing.T) {
	for _, kind := range []string{"missing", "replaced", "invalid progress", "remote error"} {
		t.Run(kind, func(t *testing.T) {
			f := newDownloadFixture(t)
			f.mu.Lock()
			switch kind {
			case "missing":
				f.torrents = nil
			case "replaced":
				f.torrents[0].AddedOn++
			case "invalid progress":
				f.torrents[0].Progress = 2
			case "remote error":
				f.torrents[0].State = "missingFiles"
			}
			f.mu.Unlock()
			if err := f.s.refreshQueuedDownloads(context.Background()); err != nil {
				t.Fatal(err)
			}
			job, err := f.s.db.MediaJob(f.job.ID)
			if err != nil || job.Status != "needs_attention" || job.Error == "" || job.TorrentAddedOn != f.job.TorrentAddedOn {
				t.Fatalf("unsafe remote result adopted: %+v %v", job, err)
			}
		})
	}
}

func TestDownloadObserverNoJobsHasNoNetworkOrFileSideEffects(t *testing.T) {
	f := newDownloadFixture(t)
	if _, err := f.s.db.CancelMediaJob(f.job.ID, f.job.Version); err != nil {
		t.Fatal(err)
	}
	if err := f.s.refreshQueuedDownloads(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.requests.Load() != 0 {
		t.Fatal("observer queried qB without active downloads")
	}
	jobs, err := f.s.db.ListMediaJobs("", 10)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "canceled" {
		t.Fatal("observer discovered unsolicited jobs")
	}
}

func TestServiceRunRefreshesExistingDownloadAndStopsCleanly(t *testing.T) {
	f := newDownloadFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.s.Run(ctx, listener) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("download worker prevented shutdown")
		}
	}()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-timer.C:
			t.Fatal("service never refreshed queued download")
		case <-ticker.C:
			job, err := f.s.db.MediaJob(f.job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if job.Download != nil && job.Download.ObservedAt != "" {
				return
			}
		}
	}
}

func TestDownloadObserverCanceledOrAdvancedJobRejectsLateResult(t *testing.T) {
	for _, kind := range []string{"request-canceled", "job-canceled", "advanced"} {
		t.Run(kind, func(t *testing.T) {
			f := newDownloadFixture(t)
			gate := make(chan struct{})
			f.mu.Lock()
			f.gate = gate
			f.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- f.s.refreshQueuedDownloads(ctx) }()
			select {
			case <-f.entered:
			case <-ctx.Done():
				t.Fatal("poll never reached remote")
			}
			switch kind {
			case "request-canceled":
				cancel()
			case "job-canceled":
				if _, err := f.s.db.CancelMediaJob(f.job.ID, f.job.Version); err != nil {
					t.Fatal(err)
				}
			case "advanced":
				job, err := f.s.db.ClaimMediaJob(f.job.ID, f.job.Version)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.s.db.TransitionMediaJob(job.ID, job.Version, MediaJobTransition{Phase: "transfer", Status: "pending"}); err != nil {
					t.Fatal(err)
				}
			}
			close(gate)
			select {
			case err := <-done:
				if kind != "request-canceled" && err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("poll did not return")
			}
			job, err := f.s.db.MediaJob(f.job.ID)
			if err != nil || job.Download != nil {
				t.Fatalf("late poll changed job: %+v %v", job, err)
			}
		})
	}
}
