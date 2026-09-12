package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestRSSAcquisitionPersistsOriginalEntryAndReusesItsQueueID(t *testing.T) {
	s := testService(t)
	task := Task{ID: 41, Name: "Example subscription"}
	entry := FeedEntry{EntryID: "episode-1", Title: "HD TEST-001", SourceURL: "https://example.test/one", Summary: "original resolver markup"}
	job, err := s.db.EnqueueRSSAcquisition(task, entry)
	if err != nil {
		t.Fatal(err)
	}
	if job.Kind != "torrent" || job.Phase != "discovered" || job.Status != "pending" || job.RSSID != task.ID || job.MediaID != "TEST-001" || job.Discovery == nil || job.Discovery.Entry.Summary != entry.Summary {
		t.Fatalf("incomplete RSS origin: %+v", job)
	}
	entry.Title = "updated display title"
	again, err := s.db.EnqueueRSSAcquisition(task, entry)
	if err != nil || again.ID != job.ID || again.Title != job.Title {
		t.Fatalf("stable GUID replaced original provenance: %+v %v", again, err)
	}
	other, err := s.db.EnqueueRSSAcquisition(Task{ID: 42, Name: "Other subscription"}, entry)
	if err != nil || other.ID == job.ID {
		t.Fatalf("separate source was collapsed before deduplication: %+v %v", other, err)
	}
}

func TestRSSObservationValidatesContentPathBeforeSavingDownload(t *testing.T) {
	for _, path := range []string{"relative/movie.mp4", "/downloads/../outside.mp4", "/downloads/nul\x00.mp4", "", "/downloads/movie.mp4"} {
		t.Run(path, func(t *testing.T) {
			db, _ := newQueueTestDB(t)
			const hash = "0123456789abcdef0123456789abcdef01234567"
			job, err := db.EnqueueRSSAcquisition(Task{ID: 1}, FeedEntry{EntryID: "item", Title: "TEST-001"})
			if err != nil {
				t.Fatal(err)
			}
			job, err = db.ClaimMediaJob(job.ID, job.Version)
			if err != nil {
				t.Fatal(err)
			}
			job, err = db.ReserveRSSDownload(job.ID, job.Version, "magnet:?xt=urn:btih:"+hash)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.ObserveRSSDownload(job.ID, job.Version, QBTorrent{Hash: hash, AddedOn: 1700000000, ContentPath: path})
			valid := path == "" || path == "/downloads/movie.mp4"
			if valid && err != nil || !valid && !errors.Is(err, errIntakeConflict) {
				t.Fatalf("unexpected content path validation: %v", err)
			}
			stored, readErr := db.MediaJob(job.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !valid && (stored.Phase != "discovered" || stored.SourcePath != "") {
				t.Fatalf("invalid response partially saved: %+v", stored)
			}
		})
	}
}

func TestCompletedIntakeAdoptsRSSQueueIdentityWithoutRewinding(t *testing.T) {
	f := newIntakeQueueFixture(t)
	config, err := f.s.db.IntakeConfig()
	if err != nil {
		t.Fatal(err)
	}
	const hash = "0123456789abcdef0123456789abcdef01234567"
	job, err := f.s.db.EnqueueRSSAcquisition(Task{ID: 41, Name: "subscription"}, FeedEntry{EntryID: "entry", Title: "HD TEST-001", SourceURL: "https://example.test/entry"})
	if err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.ReserveRSSDownload(job.ID, job.Version, "magnet:?xt=urn:btih:"+hash)
	if err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.ObserveRSSDownload(job.ID, job.Version, QBTorrent{Hash: hash, AddedOn: 1700000000, ContentPath: f.source})
	if err != nil {
		t.Fatal(err)
	}
	candidate := IntakeCandidate{Hash: hash, AddedOn: 1700000000, Name: "TEST-001", Source: f.source, Destination: f.target}
	adopted, err := f.s.enqueueIntakeCandidate(context.Background(), candidate, config)
	if err != nil || adopted.ID != job.ID || adopted.Phase != "transfer" || adopted.RSSID != 41 || adopted.SourceURL != "https://example.test/entry" || adopted.IntakeSourceRoot != config.SourceDirectory {
		t.Fatalf("intake lost origin or did not advance: %+v %v", adopted, err)
	}
	adopted, err = f.s.db.ClaimMediaJob(adopted.ID, adopted.Version)
	if err != nil {
		t.Fatal(err)
	}
	adopted, err = f.s.db.TransitionMediaJob(adopted.ID, adopted.Version, MediaJobTransition{Phase: "qb_cleanup", Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.s.enqueueIntakeCandidate(context.Background(), candidate, config)
	if err != nil || again.ID != job.ID || again.Phase != "qb_cleanup" {
		t.Fatalf("intake discovery rewound cleanup: %+v %v", again, err)
	}
	candidate.AddedOn++
	newGeneration, err := f.s.enqueueIntakeCandidate(context.Background(), candidate, config)
	if err != nil || newGeneration.ID == job.ID || newGeneration.RSSID != 0 {
		t.Fatalf("re-added task inherited old ownership: %+v %v", newGeneration, err)
	}
}

func TestCompletedIntakeDoesNotBypassCanceledOrUnconfirmedRSS(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfirmed", true: "canceled"}[cancel], func(t *testing.T) {
			f := newIntakeQueueFixture(t)
			config, err := f.s.db.IntakeConfig()
			if err != nil {
				t.Fatal(err)
			}
			job, err := f.s.db.EnqueueRSSAcquisition(Task{ID: 1}, FeedEntry{EntryID: "entry", Title: "TEST-001"})
			if err != nil {
				t.Fatal(err)
			}
			job, err = f.s.db.ClaimMediaJob(job.ID, job.Version)
			if err != nil {
				t.Fatal(err)
			}
			job, err = f.s.db.ReserveRSSDownload(job.ID, job.Version, "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
			if err != nil {
				t.Fatal(err)
			}
			if cancel {
				job, err = f.s.db.CancelMediaJob(job.ID, job.Version)
				if err != nil {
					t.Fatal(err)
				}
			}
			got, err := f.s.enqueueIntakeCandidate(context.Background(), IntakeCandidate{Hash: job.TorrentHash, AddedOn: 1700000000, Name: "TEST-001", Source: f.source, Destination: f.target}, config)
			if err != nil || got.ID != job.ID || got.Status != job.Status || got.Phase != "discovered" {
				t.Fatalf("intake bypassed submission lifecycle: %+v %v", got, err)
			}
			assertIntakeFixture(t, filepath.Dir(f.source), map[string]string{"TEST-001.mp4": "video"})
		})
	}
}

func TestRSSAcquisitionDownloadClaimDeduplicatesAcrossSubscriptions(t *testing.T) {
	s := testService(t)
	const hash = "0123456789abcdef0123456789abcdef01234567"
	first, err := s.db.EnqueueRSSAcquisition(Task{ID: 1}, FeedEntry{EntryID: "one", Title: "HD TEST-001"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.db.EnqueueRSSAcquisition(Task{ID: 2}, FeedEntry{EntryID: "two", Title: "HD TEST-001"})
	if err != nil {
		t.Fatal(err)
	}
	first, err = s.db.ClaimMediaJob(first.ID, first.Version)
	if err != nil {
		t.Fatal(err)
	}
	first, err = s.db.ReserveRSSDownload(first.ID, first.Version, "magnet:?xt=urn:btih:"+hash)
	if err != nil || first.TorrentHash != hash || first.Discovery.Magnet == "" {
		t.Fatalf("reservation missing: %+v %v", first, err)
	}
	second, err = s.db.ClaimMediaJob(second.ID, second.Version)
	if err != nil {
		t.Fatal(err)
	}
	second, err = s.db.ReserveRSSDownload(second.ID, second.Version, "magnet:?xt=urn:btih:1123456789abcdef0123456789abcdef01234567")
	if err != nil || second.Status != "skipped" || second.RelatedJobID != first.ID {
		t.Fatalf("same film was not linked to the reserved download: %+v %v", second, err)
	}
	if _, err := s.db.RetryMediaJob(second.ID, second.Version); !errors.Is(err, errMediaJobConflict) {
		t.Fatalf("duplicate was automatically retryable: %v", err)
	}
}

func TestRSSSubmissionIntentAndObservationHonorCancellation(t *testing.T) {
	s := testService(t)
	job, err := s.db.EnqueueRSSAcquisition(Task{ID: 1}, FeedEntry{EntryID: "one", Title: "HD TEST-001"})
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.db.ReserveRSSDownload(job.ID, job.Version, "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	version := job.Version
	if err := s.db.SaveRSSSubmission(job.ID, version, "dispatching"); err != nil {
		t.Fatal(err)
	}
	job, err = s.db.MediaJob(job.ID)
	if err != nil || job.Version != version || job.Discovery.SubmissionState != "dispatching" {
		t.Fatalf("submission checkpoint invalidated cancellation: %+v %v", job, err)
	}
	if _, err := s.db.CancelMediaJob(job.ID, version); err != nil {
		t.Fatal(err)
	}
	if err := s.db.SaveRSSSubmission(job.ID, version, "acknowledged"); !errors.Is(err, errMediaJobConflict) {
		t.Fatalf("stale worker overwrote cancellation: %v", err)
	}
	if _, err := s.db.ObserveRSSDownload(job.ID, version, QBTorrent{Hash: job.TorrentHash, AddedOn: 1700000000}); !errors.Is(err, errMediaJobConflict) {
		t.Fatalf("stale observation overwrote cancellation: %v", err)
	}
}

func TestSkippedRSSJobIsTerminalAndCannotOwnAnotherDownload(t *testing.T) {
	s := testService(t)
	create := func(id int64) MediaJob {
		job, err := s.db.EnqueueRSSAcquisition(Task{ID: id}, FeedEntry{EntryID: "entry", Title: "TEST-001"})
		if err != nil {
			t.Fatal(err)
		}
		job, err = s.db.ClaimMediaJob(job.ID, job.Version)
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	b, a := create(1), create(2)
	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	var err error
	a, err = s.db.ReserveRSSDownload(a.ID, a.Version, magnet)
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.db.ReserveRSSDownload(b.ID, b.Version, magnet)
	if err != nil || b.Status != "skipped" {
		t.Fatalf("missing duplicate: %+v %v", b, err)
	}
	if _, err := s.db.CancelMediaJob(b.ID, b.Version); !errors.Is(err, errMediaJobConflict) {
		t.Errorf("canceled terminal duplicate: %v", err)
	}
	c := create(3)
	c, err = s.db.ReserveRSSDownload(c.ID, c.Version, magnet)
	if err != nil || c.RelatedJobID != a.ID {
		t.Fatalf("duplicate became owner: %+v %v", c, err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/queue?status=skipped", nil)
	req.Header.Set("X-Trim-Isadmin", "true")
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("skipped queue cannot be inspected: %s", response.Body.String())
	}
}
