package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
	javspengine "github.com/yuukiy/javsp-go/engine"
)

func resourceDownloadFixture(t *testing.T) (*Service, ResourceDownloadSelection) {
	t.Helper()
	s := testService(t)
	if _, err := s.db.UpdateSettings(map[string]any{"qb_host": "http://qb.example.test", "qb_username": "example", "qb_password": "private-secret", "intake_source_directory": "/downloads"}); err != nil {
		t.Fatal(err)
	}
	result, err := s.db.saveResources(context.Background(), javspengine.ResourceSearchResult{Code: "TEST-001", Provider: "javdb", State: "ready", Items: []javspengine.ResourceCandidate{{InfoHash: resourceTestHash, Magnet: "magnet:?xt=urn:btih:" + resourceTestHash, Name: "Example release", Source: "javdb", SourceURL: "https://source.example.test/v/example", Verification: "unverified"}}})
	if err != nil {
		t.Fatal(err)
	}
	return s, ResourceDownloadSelection{Code: "TEST-001", Provider: "javdb", InfoHash: resourceTestHash, SearchedAt: result.SearchedAt}
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
	if _, err := s.db.UpdateSettings(map[string]any{"intake_source_directory": "/new-downloads", "qb_password": "another-secret"}); err != nil {
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
				_, _ = s.db.UpdateSettings(map[string]any{"intake_source_directory": ""})
			case "root_destination":
				_, _ = s.db.UpdateSettings(map[string]any{"intake_source_directory": "/"})
			case "relative_destination":
				_, _ = s.db.UpdateSettings(map[string]any{"intake_source_directory": "relative"})
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
