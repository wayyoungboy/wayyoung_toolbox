package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	javspengine "github.com/yuukiy/javsp-go/engine"
)

func enqueueIntakeResource(t *testing.T, f *intakeQueueFixture) MediaJob {
	t.Helper()
	hash := resourceTestHash
	f.torrentHash.Store(&hash)
	view, err := f.s.db.saveResources(context.Background(), javspengine.ResourceSearchResult{Code: "TEST-001", Provider: "javdb", State: "ready", Items: []javspengine.ResourceCandidate{{InfoHash: hash, SourceURL: "https://source.example.test/v/test"}}})
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := f.s.db.EnqueueResourceDownload(context.Background(), ResourceDownloadSelection{Code: "TEST-001", Provider: "javdb", InfoHash: hash, SearchedAt: view.SearchedAt})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func resourceAsObserved(t *testing.T, f *intakeQueueFixture, job MediaJob) MediaJob {
	t.Helper()
	job, err := f.s.db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.observeAcquisitionDownload(job.ID, job.Version, QBTorrent{Hash: resourceTestHash, AddedOn: f.addedOn.Load(), ContentPath: f.source})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestResourceIntakeKeepsProducerThroughMoveAndChildHandoff(t *testing.T) {
	f := newIntakeQueueFixture(t)
	configureIntakeJavsp(t, f)
	job := resourceAsObserved(t, f, enqueueIntakeResource(t, f))
	result, err := f.s.runIntake(context.Background(), "manual")
	if err != nil || result.Moved != 1 || result.TasksDeleted != 1 || result.JavspQueued != 1 {
		t.Fatalf("intake failed: %+v %v", result, err)
	}
	job, err = f.s.db.MediaJob(job.ID)
	if err != nil || job.Phase != "done" || job.Status != "success" || !job.IntakeMoviesReady || job.Resource == nil || len(job.ChildJobIDs) != 1 {
		t.Fatalf("producer lost: %+v %v", job, err)
	}
	child, err := f.s.db.MediaJob(job.ChildJobIDs[0])
	if err != nil || child.ParentID != job.ID || child.Resource == nil || child.Resource.Provider != "javdb" || child.RSSID != 0 || child.TorrentHash != job.TorrentHash {
		t.Fatalf("handoff origin lost: %+v %v", child, err)
	}
	if _, err := os.Stat(f.target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.source); !os.IsNotExist(err) {
		t.Fatalf("source retained: %v", err)
	}
}

func TestResourceIntakeNeverBypassesCanceledOrUnconfirmedProducer(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfirmed", true: "canceled"}[cancel], func(t *testing.T) {
			f := newIntakeQueueFixture(t)
			job := enqueueIntakeResource(t, f)
			var err error
			if cancel {
				job = resourceAsObserved(t, f, job)
				job, err = f.s.db.CancelMediaJob(job.ID, job.Version)
			} else {
				job, err = f.s.db.ClaimMediaJob(job.ID, job.Version)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.s.db.saveAcquisitionSubmission(job.ID, job.Version, "dispatching"); err != nil {
					t.Fatal(err)
				}
				job, err = f.s.db.TransitionMediaJob(job.ID, job.Version, MediaJobTransition{Phase: "discovered", Status: "needs_attention"})
			}
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := f.s.db.IntakeConfig()
			if err != nil {
				t.Fatal(err)
			}
			got, err := f.s.enqueueIntakeCandidate(context.Background(), IntakeCandidate{Hash: job.TorrentHash, AddedOn: f.addedOn.Load(), Name: "TEST-001", Source: f.source, Destination: f.target}, cfg)
			if err != nil || got.ID != job.ID || got.Status != job.Status || got.Phase != job.Phase {
				t.Fatalf("bypassed original producer: %+v %v", got, err)
			}
			if _, err := os.Stat(f.source); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResourcePollingNeverContactsChangedDownloader(t *testing.T) {
	for _, phase := range []string{"discovered", "download"} {
		t.Run(phase, func(t *testing.T) {
			f := newResourceExecutionFixture(t)
			if phase == "download" {
				f.present.Store(true)
				resourceDrain(t, f)
			} else {
				f.failLookupAfterAdd.Store(true)
				resourceDrain(t, f)
			}
			var calls atomic.Int32
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == "/api/v2/auth/login" {
					w.Write([]byte("Ok."))
				} else {
					w.Write([]byte("[]"))
				}
			}))
			defer other.Close()
			if _, err := f.s.db.UpdateSettings(map[string]any{"qb_host": other.URL}); err != nil {
				t.Fatal(err)
			}
			if err := f.s.refreshQueuedDownloads(context.Background()); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 0 {
				t.Fatalf("contacted unrelated downloader %d times", calls.Load())
			}
			job, err := f.s.db.MediaJob(f.job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if job.TorrentAddedOn != 0 && job.TorrentAddedOn != f.addedOn.Load() {
				t.Fatal("adopted other instance")
			}
		})
	}
}
