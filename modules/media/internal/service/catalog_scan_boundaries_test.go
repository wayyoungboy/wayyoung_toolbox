package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

func TestCatalogScanRejectsInvalidLifecycleAndBatchInputs(t *testing.T) {
	s, job, _ := scanJobFixture(t)
	ctx := t.Context()
	for _, pair := range [][2]int64{{0, 1}, {job.ID, 0}} {
		if _, err := s.db.catalog.ClaimScan(ctx, pair[0], pair[1]); !errors.Is(err, catalog.ErrInvalid) {
			t.Fatal("invalid claim", err)
		}
		if err := s.db.catalog.ReleaseScanAttempt(ctx, pair[0], pair[1]); !errors.Is(err, catalog.ErrInvalid) {
			t.Fatal("invalid release", err)
		}
		if _, err := s.db.catalog.ChangeScanJob(ctx, pair[0], pair[1], "pause"); !errors.Is(err, catalog.ErrInvalid) {
			t.Fatal("invalid action identity", err)
		}
	}
	if _, err := s.db.catalog.StartScan(ctx, 0); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.db.catalog.ScanJob(ctx, 0); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.db.catalog.ScanJob(ctx, 99999); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.db.catalog.ActiveLibraryScan(ctx, 0); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.db.catalog.LibraryScanJobs(ctx, 0, 0, 20); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.db.catalog.ScanJobs(ctx, -1, 20); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.db.catalog.ChangeScanJob(ctx, job.ID, job.Version, "unknown"); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.db.catalog.ChangeScanJob(ctx, job.ID, job.Version, "resume"); !errors.Is(err, catalog.ErrConflict) {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 257} {
		if _, err := s.db.catalog.ScanBatch(ctx, job.ID, job.Version, limit); !errors.Is(err, catalog.ErrInvalid) {
			t.Fatal("invalid batch size", err)
		}
	}
}

func TestCatalogScanCheckpointsExcludeSymlinksHiddenMediaAndEmptyVideos(t *testing.T) {
	s := testService(t)
	root := registrationRoot(t)
	writeIntakeFixture(t, root, map[string]string{"TEST-001.mp4": "synthetic", "TEST-001.nfo": strings.Repeat("x", (1<<20)+1), "EMPTY-009.mp4": ""})
	writeIntakeFixture(t, filepath.Join(root, ".hidden"), map[string]string{"HIDDEN-010.mp4": "not imported"})
	outside := filepath.Join(registrationRoot(t), "OUTSIDE-001.mp4")
	catalogWrite(t, filepath.Dir(outside), filepath.Base(outside), "outside")
	if err := os.Symlink(outside, filepath.Join(root, "LINK-001.mp4")); err != nil {
		t.Fatal(err)
	}
	lib := catalogLibrary(t, s, root)
	job := finishCatalogScan(t, s, claimCatalogScan(t, s, startCatalogScan(t, s, lib.ID)))
	if job.Result == nil || job.Result.Assets != 1 || job.Result.Issues != 1 || len(catalogWorks(t, s, "")) != 1 {
		t.Fatalf("unsafe import or oversized NFO discarded video: %+v", job)
	}
}

func TestCatalogScanLegacyCheckpointWithoutManifestRequiresExplicitRetry(t *testing.T) {
	s, job, _ := scanJobFixture(t)
	job = checkpointCatalogScan(t, s, claimCatalogScan(t, s, job), 2)
	if _, err := s.db.db.Exec("ALTER TABLE catalog_scan_dirs DROP COLUMN manifest"); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.New(s.db.db, unambiguousMediaID)
	if err != nil {
		t.Fatal(err)
	}
	s.db.catalog = store
	job, err = store.ScanJob(t.Context(), job.ID)
	if err != nil || job.Status != "pending" || job.ProcessedFiles != 2 {
		t.Fatal("legacy cursor lost during safe migration", job, err)
	}
	job = claimCatalogScan(t, s, job)
	for range 100 {
		job, err = store.ScanBatch(t.Context(), job.ID, job.Version, 1)
		if err != nil || job.Status != "running" {
			break
		}
	}
	if !errors.Is(err, catalog.ErrConflict) || job.Status != "failed" || len(catalogWorks(t, s, "")) != 0 {
		t.Fatalf("unverifiable old checkpoint published: %+v %v", job, err)
	}
	job, err = store.ChangeScanJob(t.Context(), job.ID, job.Version, "retry")
	if err != nil {
		t.Fatal(err)
	}
	finishCatalogScan(t, s, claimCatalogScan(t, s, job))
}

func TestCatalogScanRejectsFilesRemovedAfterCheckpointAndBudgetExhaustion(t *testing.T) {
	for _, change := range []string{"removed-sidecar", "removed-video", "file-budget"} {
		t.Run(change, func(t *testing.T) {
			s, job, root := scanJobFixture(t)
			job = claimCatalogScan(t, s, job)
			job = checkpointCatalogScan(t, s, job, 1)
			switch change {
			case "removed-sidecar":
				if err := os.Remove(filepath.Join(root, "folder-0", "TEST-001.nfo")); err != nil {
					t.Fatal(err)
				}
			case "removed-video":
				if err := os.Remove(filepath.Join(root, "folder-0", "TEST-001.mp4")); err != nil {
					t.Fatal(err)
				}
			case "file-budget":
				if _, err := s.db.db.Exec("UPDATE catalog_scan_jobs SET staged_bytes=? WHERE id=?", (128<<20)-64, job.ID); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			for range 100 {
				job, err = s.db.catalog.ScanBatch(t.Context(), job.ID, job.Version, 1)
				if err != nil || job.Status != "running" {
					break
				}
			}
			if err == nil || job.Status != "failed" || len(catalogWorks(t, s, "")) != 0 {
				t.Fatalf("invalid checkpoint published: %+v %v", job, err)
			}
		})
	}
}

func TestCatalogScanStoreNeverTreatsUnavailableDatabaseAsEmptyOrSuccess(t *testing.T) {
	s, job, _ := scanJobFixture(t)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if _, err := s.db.catalog.ScanJob(ctx, job.ID); err == nil {
		t.Fatal("read succeeded after database closed")
	}
	if _, err := s.db.catalog.StartScan(ctx, job.LibraryID); err == nil {
		t.Fatal("start succeeded after database closed")
	}
	if _, err := s.db.catalog.ScanJobs(ctx, 0, 20); err == nil {
		t.Fatal("unavailable database treated as empty history")
	}
	if _, err := s.db.catalog.ClaimScan(ctx, job.ID, job.Version); err == nil {
		t.Fatal("claim succeeded after database closed")
	}
	if _, err := s.db.catalog.ChangeScanJob(ctx, job.ID, job.Version, "pause"); err == nil {
		t.Fatal("pause succeeded after database closed")
	}
	if err := s.db.catalog.ReleaseScanAttempt(ctx, job.ID, job.Version); err == nil {
		t.Fatal("release succeeded after database closed")
	}
	if _, err := s.db.catalog.ActiveLibraryScan(ctx, job.LibraryID); err == nil {
		t.Fatal("unavailable database treated as no active job")
	}
	if err := s.drainCatalogScans(ctx); err == nil {
		t.Fatal("worker treated database failure as empty queue")
	}
}
