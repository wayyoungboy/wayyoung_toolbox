package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	javspengine "github.com/yuukiy/javsp-go/engine"
)

func newResourceExecutionFixture(t *testing.T) *rssExecutionFixture {
	t.Helper()
	f := newRSSExecutionFixture(t)
	f.addedOn.Store(time.Now().Unix())
	if _, err := f.s.db.CancelMediaJob(f.job.ID, f.job.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.db.Exec("DELETE FROM rss_tasks"); err != nil {
		t.Fatal(err)
	}
	resourceDownloadSetting(t, f.s.db, "intake_source_directory", "/downloads")
	view, err := f.s.db.saveResources(context.Background(), javspengine.ResourceSearchResult{Code: "TEST-001", Provider: "javdb", State: "ready", Items: []javspengine.ResourceCandidate{{InfoHash: resourceTestHash, SourceURL: "https://source.example.test/v/test"}}})
	if err != nil {
		t.Fatal(err)
	}
	f.job, _, err = f.s.db.EnqueueResourceDownload(context.Background(), ResourceDownloadSelection{Code: "TEST-001", Provider: "javdb", InfoHash: resourceTestHash, SearchedAt: view.SearchedAt})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func resourceDrain(t *testing.T, f *rssExecutionFixture) MediaJob {
	t.Helper()
	if err := f.s.drainPendingRSS(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := f.s.db.MediaJob(f.job.ID)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestResourceExecutionUsesIndependentProducerAndDirectQB(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[present], func(t *testing.T) {
			f := newResourceExecutionFixture(t)
			f.present.Store(present)
			job := resourceDrain(t, f)
			wantAdds := int32(1)
			if present {
				wantAdds = 0
			}
			if job.Status != "pending" || job.Phase != "download" || job.TorrentAddedOn != f.addedOn.Load() || job.RSSID != 0 || job.Discovery != nil || job.Resource == nil || f.adds.Load() != wantAdds || f.feeds.Load() != 0 {
				t.Fatalf("independent execution: %+v posts=%d", job, f.adds.Load())
			}
			if !present {
				select {
				case path := <-f.savePaths:
					if path != "/downloads" {
						t.Fatal(path)
					}
				default:
					t.Fatal("missing destination")
				}
			}
			if again := resourceDrain(t, f); again.ID != job.ID || f.adds.Load() != wantAdds {
				t.Fatal("repeat dispatch")
			}
		})
	}
}

func TestResourceExecutionUnknownOutcomeSurvivesRestartWithoutRepost(t *testing.T) {
	f := newResourceExecutionFixture(t)
	f.failLookupAfterAdd.Store(true)
	job := resourceDrain(t, f)
	if job.Status != "needs_attention" || job.Resource.SubmissionState != "dispatching" || f.adds.Load() != 1 {
		t.Fatalf("uncertainty lost: %+v", job)
	}
	f.reopen(t)
	f.lookupFails.Store(false)
	f.present.Store(false)
	if _, err := f.s.db.RetryMediaJob(job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	job = resourceDrain(t, f)
	if f.adds.Load() != 1 || job.Status != "needs_attention" {
		t.Fatalf("unknown POST repeated: %+v", job)
	}
	f.present.Store(true)
	if err := f.s.refreshQueuedDownloads(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := f.s.db.MediaJob(job.ID)
	if err != nil || job.Phase != "download" || job.Status != "pending" || f.adds.Load() != 1 {
		t.Fatalf("late observation failed: %+v %v", job, err)
	}
}

func TestResourceExecutionRetryRejectsLaterReaddedGeneration(t *testing.T) {
	f := newResourceExecutionFixture(t)
	f.failLookupAfterAdd.Store(true)
	job := resourceDrain(t, f)
	if job.Status != "needs_attention" {
		t.Fatal(job.Status)
	}
	f.lookupFails.Store(false)
	f.addedOn.Add(int64(rssJobTimeout/time.Second) + 60)
	if _, err := f.s.db.RetryMediaJob(job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	job = resourceDrain(t, f)
	if job.Status != "needs_attention" || job.Phase != "discovered" || job.TorrentAddedOn != 0 || f.adds.Load() != 1 {
		t.Fatalf("adopted readded task: %+v", job)
	}
}

func TestResourceExecutionExplicitRejectionCanRetryButEndpointEditCannotDispatch(t *testing.T) {
	f := newResourceExecutionFixture(t)
	f.reject.Store(true)
	job := resourceDrain(t, f)
	if job.Status != "failed" || job.Resource.SubmissionState != "rejected" || f.adds.Load() != 1 {
		t.Fatalf("rejection not recorded: %+v", job)
	}
	f.reject.Store(false)
	if _, err := f.s.db.RetryMediaJob(job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	job = resourceDrain(t, f)
	if job.Phase != "download" || f.adds.Load() != 2 {
		t.Fatalf("explicit retry failed: %+v", job)
	}

	other := newResourceExecutionFixture(t)
	if _, err := other.s.db.UpdateSettings(map[string]any{"qb_host": "http://different.example.test"}); err != nil {
		t.Fatal(err)
	}
	job = resourceDrain(t, other)
	if job.Status != "needs_attention" || other.adds.Load() != 0 || other.queries.Load() != 0 {
		t.Fatalf("different downloader accessed: %+v", job)
	}
}

func TestResourceExecutionCancelDuringPostNeverRestartsDownload(t *testing.T) {
	f := newResourceExecutionFixture(t)
	f.pauseAdd.Store(true)
	done := make(chan error, 1)
	go func() { done <- f.s.drainPendingRSS(context.Background()) }()
	select {
	case <-f.posted:
	case <-time.After(3 * time.Second):
		t.Fatal("POST not reached")
	}
	job, err := f.s.db.MediaJob(f.job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.CancelMediaJob(job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	f.s.cancelMediaJobAttempt(job.ID, job.Version)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation stalled")
	}
	job, err = f.s.db.MediaJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "canceled" || job.Resource.SubmissionState != "dispatching" {
		t.Fatalf("cancel lost intent: %+v", job)
	}
	f.pauseAdd.Store(false)
	f.present.Store(false)
	if _, err := f.s.db.RetryMediaJob(job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	job = resourceDrain(t, f)
	if job.Status != "needs_attention" || f.adds.Load() != 1 {
		t.Fatalf("canceled POST replayed: %+v", job)
	}
}

func TestResourceProducerSurvivesDatabaseReopen(t *testing.T) {
	s, input := resourceDownloadFixture(t)
	job, _, err := s.db.EnqueueResourceDownload(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	s.db, err = OpenDatabase(filepath.Join(s.dataDir, "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	again, created, err := s.db.EnqueueResourceDownload(context.Background(), input)
	if err != nil || created || again.ID != job.ID || again.Resource == nil || again.Resource.DownloadPath != "/downloads" {
		t.Fatalf("restart lost producer: %+v %v", again, err)
	}
}
