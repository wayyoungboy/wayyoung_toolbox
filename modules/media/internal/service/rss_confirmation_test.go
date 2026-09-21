package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRSSConfirmationRecoversDelayedVisibilityWithoutReposting(t *testing.T) {
	for _, state := range []string{"acknowledged", "dispatching"} {
		t.Run(state, func(t *testing.T) {
			f := newRSSExecutionFixture(t)
			claimed, err := f.s.db.ClaimMediaJob(f.job.ID, f.job.Version)
			if err != nil {
				t.Fatal(err)
			}
			reserved, err := f.s.db.ReserveRSSDownload(claimed.ID, claimed.Version, f.job.Discovery.Entry.Summary)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.s.db.SaveRSSSubmission(reserved.ID, reserved.Version, "dispatching"); err != nil {
				t.Fatal(err)
			}
			if state == "acknowledged" {
				if err := f.s.db.SaveRSSSubmission(reserved.ID, reserved.Version, state); err != nil {
					t.Fatal(err)
				}
			}
			job, err := f.s.db.TransitionMediaJob(reserved.ID, reserved.Version, MediaJobTransition{Phase: "discovered", Status: "needs_attention", Error: "目标已存在但未通过完整内容核验：投递结果尚未确认"})
			if err != nil {
				t.Fatal(err)
			}
			var present atomic.Bool
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v2/auth/login" {
					fmt.Fprint(w, "Ok.")
					return
				}
				if r.Method != "GET" || r.URL.Path != "/api/v2/torrents/info" || r.URL.Query().Get("hashes") != job.TorrentHash {
					t.Errorf("unsafe confirmation request %s %s", r.Method, r.URL)
					http.Error(w, "unsafe", 400)
					return
				}
				if present.Load() {
					_ = json.NewEncoder(w).Encode([]QBTorrent{{Hash: job.TorrentHash, AddedOn: time.Now().Unix(), ContentPath: "/downloads/TEST-001.mp4", Progress: 1, State: "stalledUP"}})
				} else {
					fmt.Fprint(w, "[]")
				}
			}))
			defer remote.Close()
			if _, err := f.s.db.UpdateSettings(map[string]any{"qb_host": remote.URL}); err != nil {
				t.Fatal(err)
			}
			if err := f.s.reconcileRSSSubmissions(context.Background()); err != nil {
				t.Fatal(err)
			}
			missing, _ := f.s.db.MediaJob(job.ID)
			if missing.Phase != "discovered" || missing.Status != "needs_attention" || strings.Contains(missing.Error, "目标已存在") {
				t.Fatalf("missing should wait with clear message: %+v", missing)
			}
			present.Store(true)
			f.reopen(t)
			if err := f.s.reconcileRSSSubmissions(context.Background()); err != nil {
				t.Fatal(err)
			}
			confirmed, _ := f.s.db.MediaJob(job.ID)
			if confirmed.Phase != "download" || confirmed.Status != "pending" || confirmed.Error != "" || confirmed.TorrentAddedOn <= 0 {
				t.Fatalf("not recovered: %+v", confirmed)
			}
			if confirmed.Attempts != job.Attempts {
				t.Fatal("read-only confirmation incremented submission attempts")
			}
		})
	}
}

func TestRSSConfirmationRejectsUnsafeOrStaleObservations(t *testing.T) {
	for _, scenario := range []string{"cancel_race", "old_torrent", "readded_torrent", "wrong_hash", "invalid_path", "empty_path", "missing_dispatch", "network_error", "missing", "canceled_context"} {
		t.Run(scenario, func(t *testing.T) {
			f := newRSSExecutionFixture(t)
			f.failLookupAfterAdd.Store(true)
			job, _ := f.run(t)
			if job.Status != "needs_attention" {
				t.Fatalf("fixture=%+v", job)
			}
			torrent := &QBTorrent{Hash: job.TorrentHash, AddedOn: time.Now().Unix(), ContentPath: "/downloads/TEST-001.mp4"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var pollErr error
			switch scenario {
			case "cancel_race":
				if _, err := f.s.db.CancelMediaJob(job.ID, job.Version); err != nil {
					t.Fatal(err)
				}
			case "old_torrent":
				torrent.AddedOn -= 3600
			case "readded_torrent":
				torrent.AddedOn += 3600
			case "wrong_hash":
				torrent.Hash = strings.Repeat("a", 40)
			case "invalid_path":
				torrent.ContentPath = "relative/video.mp4"
			case "empty_path":
				torrent.ContentPath = ""
			case "missing_dispatch":
				if _, err := f.s.db.db.Exec(`DELETE FROM media_job_events WHERE job_id=? AND reason='submission_dispatching'`, job.ID); err != nil {
					t.Fatal(err)
				}
			case "network_error":
				pollErr = fmt.Errorf("temporary failure")
			case "missing":
				torrent = nil
			case "canceled_context":
				cancel()
			}
			before, _ := f.s.db.MediaJobEvents(job.ID)
			for range 2 {
				err := f.s.db.recordRSSConfirmation(ctx, job, torrent, pollErr)
				if scenario != "cancel_race" && scenario != "canceled_context" && err != nil {
					t.Fatal(err)
				}
				if (scenario == "cancel_race" || scenario == "canceled_context") && err == nil {
					t.Fatal("stale/canceled write accepted")
				}
			}
			current, _ := f.s.db.MediaJob(job.ID)
			if current.Phase != "discovered" || current.TorrentAddedOn != 0 || current.Attempts != job.Attempts {
				t.Fatalf("unsafe association: %+v", current)
			}
			if scenario == "cancel_race" && current.Status != "canceled" {
				t.Fatal("canceled job revived")
			}
			after, _ := f.s.db.MediaJobEvents(job.ID)
			if len(before) != len(after) {
				t.Fatal("observation inflated event history")
			}
		})
	}
}

func TestRSSConfirmationNeverRevivesCanceledOrUnsubmittedJobs(t *testing.T) {
	f := newRSSExecutionFixture(t)
	if err := f.s.reconcileRSSSubmissions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.queries.Load() != 0 || f.adds.Load() != 0 {
		t.Fatal("confirmation touched an unsubmitted job")
	}
	f.failLookupAfterAdd.Store(true)
	job, _ := f.run(t)
	if _, err := f.s.db.CancelMediaJob(job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	f.lookupFails.Store(false)
	before := f.queries.Load()
	if err := f.s.reconcileRSSSubmissions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.queries.Load() != before {
		t.Fatal("confirmation queried canceled job")
	}
}
