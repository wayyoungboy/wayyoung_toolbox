package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
	javspengine "github.com/yuukiy/javsp-go/engine"
)

func resourceDownloadFixture(t *testing.T) (*Service, ResourceDownloadSelection) {
	t.Helper()
	s := testService(t)
	if _, err := s.db.UpdateSettings(map[string]any{"qb_host": "http://qb.example.test", "qb_username": "example", "qb_password": "private-secret"}); err != nil {
		t.Fatal(err)
	}
	resourceDownloadSetting(t, s.db, "intake_source_directory", "/downloads")
	result, err := s.db.saveResources(context.Background(), javspengine.ResourceSearchResult{Code: "TEST-001", Provider: "javdb", State: "ready", Items: []javspengine.ResourceCandidate{{InfoHash: resourceTestHash, Magnet: "magnet:?xt=urn:btih:" + resourceTestHash, Name: "Example release", Source: "javdb", SourceURL: "https://source.example.test/v/example", Verification: "unverified"}}})
	if err != nil {
		t.Fatal(err)
	}
	return s, ResourceDownloadSelection{Code: "TEST-001", Provider: "javdb", InfoHash: resourceTestHash, SearchedAt: result.SearchedAt}
}

func TestResourceDownloadConcurrentRequestsAndAtomicFailure(t *testing.T) {
	s, input := resourceDownloadFixture(t)
	if _, err := s.db.db.Exec(`CREATE TRIGGER resource_event_failure BEFORE INSERT ON media_job_events BEGIN SELECT RAISE(ABORT,'test event failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.db.EnqueueResourceDownload(context.Background(), input); err == nil {
		t.Fatal("injected event failure accepted")
	}
	for _, table := range []string{"media_jobs", "media_job_events"} {
		var count int
		if err := s.db.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial reservation in %s: %d %v", table, count, err)
		}
	}
	if _, err := s.db.db.Exec("DROP TRIGGER resource_event_failure"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var created atomic.Int32
	ids := make(chan int64, 16)
	errors := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, isNew, err := s.db.EnqueueResourceDownload(context.Background(), input)
			if err != nil {
				errors <- err
				return
			}
			if isNew {
				created.Add(1)
			}
			ids <- job.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	var expected int64
	for id := range ids {
		if expected == 0 {
			expected = id
		}
		if id != expected {
			t.Fatal("concurrent requests created distinct jobs")
		}
	}
	if created.Load() != 1 {
		t.Fatalf("created %d jobs", created.Load())
	}
	job, err := s.db.MediaJob(expected)
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.db.CancelMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.db.Exec("DELETE FROM resource_search_cache"); err != nil {
		t.Fatal(err)
	}
	again, isNew, err := s.db.EnqueueResourceDownload(context.Background(), input)
	if err != nil || isNew || again.Status != "canceled" || again.ID != job.ID {
		t.Fatalf("repeat resurrected a stopped request: %+v %v", again, err)
	}
}

func resourceDownloadSetting(t *testing.T, db *Database, key, value string) {
	t.Helper()
	if _, err := db.db.Exec("UPDATE settings SET value=? WHERE key=?", encodeJSON(value), key); err != nil {
		t.Fatal(err)
	}
}

func TestResourceDownloadPersistsIndependentProducerAndFrozenDestination(t *testing.T) {
	s, selection := resourceDownloadFixture(t)
	job, created, err := s.db.EnqueueResourceDownload(context.Background(), selection)
	if err != nil || !created {
		t.Fatalf("enqueue: created=%t %v", created, err)
	}
	if job.RSSID != 0 || job.Discovery != nil || job.Resource == nil || job.Kind != "torrent" || job.Phase != "discovered" || job.Status != "pending" || job.TorrentHash != resourceTestHash || job.IntakeSourceRoot != "/downloads" || job.Resource.DownloadPath != "/downloads" || job.Resource.Provider != "javdb" || job.Resource.SearchedAt != selection.SearchedAt {
		t.Fatalf("independent producer lost: %+v", job)
	}
	resourceDownloadSetting(t, s.db, "intake_source_directory", "/new-downloads")
	if _, err := s.db.UpdateSettings(map[string]any{"qb_password": "another-secret"}); err != nil {
		t.Fatal(err)
	}
	again, created, err := s.db.EnqueueResourceDownload(context.Background(), selection)
	if err != nil || created || again.ID != job.ID || again.Resource.DownloadPath != "/downloads" {
		t.Fatalf("repeat replaced intent: %+v created=%t %v", again, created, err)
	}
	var payload string
	if err := s.db.db.QueryRow("SELECT payload FROM media_jobs WHERE id=?", job.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, "secret") || strings.Contains(payload, "qb.example.test") {
		t.Fatal("credentials or raw downloader endpoint entered durable ledger")
	}
	for _, table := range []string{"rss_tasks", "seen_items", "task_runs", "javsp_runs", "catalog_scan_jobs"} {
		var count int
		if err := s.db.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("request fabricated automation: %s %d %v", table, count, err)
		}
	}
}

func TestResourceDownloadRejectsUnconfirmedOrChangedSelection(t *testing.T) {
	for _, mode := range []string{"missing_cache", "stale", "changed_snapshot", "different_hash", "invalid_hash", "invalid_code", "provider", "missing_destination", "root_destination", "relative_destination", "missing_downloader"} {
		t.Run(mode, func(t *testing.T) {
			s, input := resourceDownloadFixture(t)
			want := catalog.ErrInvalid
			switch mode {
			case "missing_cache":
				_, _ = s.db.db.Exec("DELETE FROM resource_search_cache")
				want = catalog.ErrConflict
			case "stale":
				if err := s.db.recordResourceFailure(context.Background(), javspengine.ResourceSearchResult{Code: input.Code, Provider: input.Provider, State: "blocked"}); err != nil {
					t.Fatal(err)
				}
				want = catalog.ErrConflict
			case "changed_snapshot":
				input.SearchedAt = "earlier"
				want = catalog.ErrConflict
			case "different_hash":
				input.InfoHash = strings.Repeat("f", 40)
				want = catalog.ErrConflict
			case "invalid_hash":
				input.InfoHash = "https://untrusted.invalid/file"
			case "invalid_code":
				input.Code = "_646"
			case "provider":
				input.Provider = "arbitrary"
			case "missing_destination":
				resourceDownloadSetting(t, s.db, "intake_source_directory", "")
			case "root_destination":
				resourceDownloadSetting(t, s.db, "intake_source_directory", "/")
			case "relative_destination":
				resourceDownloadSetting(t, s.db, "intake_source_directory", "relative")
			case "missing_downloader":
				_, _ = s.db.UpdateSettings(map[string]any{"qb_host": ""})
			}
			if _, _, err := s.db.EnqueueResourceDownload(context.Background(), input); !errors.Is(err, want) {
				t.Fatalf("%s accepted or wrong failure: %v", mode, err)
			}
			var count int
			_ = s.db.db.QueryRow("SELECT COUNT(*) FROM media_jobs").Scan(&count)
			if count != 0 {
				t.Fatalf("invalid selection left %d jobs", count)
			}
		})
	}
}

func TestResourceDownloadAndRSSShareReservationWithoutFakeRSS(t *testing.T) {
	for _, resourceFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "rss_first", true: "resource_first"}[resourceFirst], func(t *testing.T) {
			s, selection := resourceDownloadFixture(t)
			rss, err := s.db.EnqueueRSSAcquisition(Task{ID: 42}, FeedEntry{EntryID: "item", Title: "TEST-001"})
			if err != nil {
				t.Fatal(err)
			}
			rss, err = s.db.ClaimMediaJob(rss.ID, rss.Version)
			if err != nil {
				t.Fatal(err)
			}
			var resource MediaJob
			if resourceFirst {
				resource, _, err = s.db.EnqueueResourceDownload(context.Background(), selection)
				if err != nil {
					t.Fatal(err)
				}
			}
			rss, err = s.db.ReserveRSSDownload(rss.ID, rss.Version, "magnet:?xt=urn:btih:"+resourceTestHash)
			if err != nil {
				t.Fatal(err)
			}
			if resourceFirst {
				if rss.Status != "skipped" || rss.RelatedJobID != resource.ID {
					t.Fatalf("RSS bypassed resource reservation: %+v", rss)
				}
			} else {
				resource, created, err := s.db.EnqueueResourceDownload(context.Background(), selection)
				if err != nil || created || resource.ID != rss.ID {
					t.Fatalf("resource bypassed RSS reservation: %+v created=%t %v", resource, created, err)
				}
			}
		})
	}
}
