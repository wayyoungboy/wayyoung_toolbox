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
			var present, cancelDuringLookup atomic.Bool
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
				if cancelDuringLookup.Load() {
					current, _ := f.s.db.MediaJob(job.ID)
					_, _ = f.s.db.CancelMediaJob(current.ID, current.Version)
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
