package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

func startCatalogScan(t *testing.T, s *Service, libraryID int64) catalog.ScanJob {
	t.Helper()
	job, err := s.db.catalog.StartScan(context.Background(), libraryID)
	if err != nil || job.Status != "pending" || job.Version < 1 {
		t.Fatalf("start scan=%+v %v", job, err)
	}
	return job
}

func claimCatalogScan(t *testing.T, s *Service, job catalog.ScanJob) catalog.ScanJob {
	t.Helper()
	job, err := s.db.catalog.ClaimScan(context.Background(), job.ID, job.Version)
	if err != nil || job.Status != "running" {
		t.Fatalf("claim scan=%+v %v", job, err)
	}
	return job
}

func finishCatalogScan(t *testing.T, s *Service, job catalog.ScanJob) catalog.ScanJob {
	t.Helper()
	for range 100 {
		var err error
		job, err = s.db.catalog.ScanBatch(context.Background(), job.ID, job.Version, 1)
		if err != nil {
			t.Fatalf("batch=%+v %v", job, err)
		}
		if job.Status == "success" {
			return job
		}
	}
	t.Fatal("bounded fixture did not finish")
	return job
}

func checkpointCatalogScan(t *testing.T, s *Service, job catalog.ScanJob, files int) catalog.ScanJob {
	t.Helper()
	for range 100 {
		previousVersion := job.Version
		var err error
		job, err = s.db.catalog.ScanBatch(context.Background(), job.ID, job.Version, 1)
		if err != nil || job.Status != "running" || previousVersion != job.Version {
			t.Fatalf("progress changed lifecycle revision or finished early: %+v %v", job, err)
		}
		if job.ProcessedFiles >= files {
			return job
		}
	}
	t.Fatal("no durable progress")
	return job
}

func scanJobFixture(t *testing.T) (*Service, catalog.ScanJob, string) {
	t.Helper()
	s := testService(t)
	root := registrationRoot(t)
	for index := range 4 {
		code := fmt.Sprintf("TEST-%03d", index+1)
		writeIntakeFixture(t, filepath.Join(root, fmt.Sprintf("folder-%d", index/2)), map[string]string{
			code + ".mp4": "synthetic video " + code,
			code + ".nfo": "<movie><title>Sample " + code + "</title></movie>",
		})
	}
	lib := catalogLibrary(t, s, root)
	return s, startCatalogScan(t, s, lib.ID), root
}

func TestCatalogScanJobCheckpointsPauseAndRestartWithoutPartialPublication(t *testing.T) {
	s, job, _ := scanJobFixture(t)
	if _, err := s.db.catalog.StartScan(context.Background(), job.LibraryID); !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("duplicate active scan=%v", err)
	}
	job = checkpointCatalogScan(t, s, claimCatalogScan(t, s, job), 2)
	if len(catalogWorks(t, s, "")) != 0 {
		t.Fatal("checkpoint published partial inventory")
	}
	paused, err := s.db.catalog.ChangeScanJob(context.Background(), job.ID, job.Version, "pause")
	if err != nil || paused.Status != "paused" {
		t.Fatalf("pause=%+v %v", paused, err)
	}
	if _, err := s.db.catalog.ScanBatch(context.Background(), job.ID, job.Version, 1); !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("stale paused attempt wrote data: %v", err)
	}
	var sequence int
	var schema, databasePath string
	if err := s.db.db.QueryRow("PRAGMA database_list").Scan(&sequence, &schema, &databasePath); err != nil {
		t.Fatal(err)
	}
	reopen := func() {
		t.Helper()
		if err := s.db.Close(); err != nil {
			t.Fatal(err)
		}
		var err error
		s.db, err = OpenDatabase(databasePath)
		if err != nil {
			t.Fatal(err)
		}
	}
	reopen()
	paused, err = s.db.catalog.ScanJob(context.Background(), job.ID)
	if err != nil || paused.Status != "paused" || paused.ProcessedFiles != job.ProcessedFiles {
		t.Fatalf("restart discarded pause/checkpoint: %+v %v", paused, err)
	}
	resumed, err := s.db.catalog.ChangeScanJob(context.Background(), paused.ID, paused.Version, "resume")
	if err != nil {
		t.Fatal(err)
	}
	job = claimCatalogScan(t, s, resumed)
	reopen()
	job, err = s.db.catalog.ScanJob(context.Background(), job.ID)
	if err != nil || job.Status != "pending" || job.ProcessedFiles != paused.ProcessedFiles {
		t.Fatalf("running restart did not preserve cursor: %+v %v", job, err)
	}
	job = finishCatalogScan(t, s, claimCatalogScan(t, s, job))
	if job.Result == nil || job.Result.Assets != 4 || job.Result.Works != 4 || job.ProcessedFiles != 4 || len(catalogWorks(t, s, "")) != 4 {
		t.Fatalf("invalid completed inventory: %+v", job)
	}
	var staged int
	if err := s.db.db.QueryRow("SELECT COUNT(*) FROM catalog_scan_files WHERE job_id=?", job.ID).Scan(&staged); err != nil || staged != 0 {
		t.Fatalf("completed staging retained: %d %v", staged, err)
	}
}

func TestCatalogScanJobOnlySuccessfulInventoryMarksMissing(t *testing.T) {
	s := testService(t)
	root := registrationRoot(t)
	writeIntakeFixture(t, root, map[string]string{"OLD-001.mp4": "old"})
	lib := catalogLibrary(t, s, root)
	catalogScan(t, s, lib)
	old := catalogWorks(t, s, "")[0]
	if err := os.Remove(filepath.Join(root, "OLD-001.mp4")); err != nil {
		t.Fatal(err)
	}
	writeIntakeFixture(t, root, map[string]string{"NEW-001.mp4": "new", "NEW-002.mp4": "other"})
	job := checkpointCatalogScan(t, s, claimCatalogScan(t, s, startCatalogScan(t, s, lib.ID)), 1)
	if got, err := s.db.catalog.Work(context.Background(), old.ID); err != nil || got.AvailableCount != 1 {
		t.Fatal("partial scan marked old asset missing")
	}
	job = finishCatalogScan(t, s, job)
	if got, err := s.db.catalog.Work(context.Background(), old.ID); err != nil || got.AvailableCount != 0 || job.Result.Assets != 2 {
		t.Fatal("successful scan did not reconcile missing assets")
	}
}

func TestCatalogScanJobCancelAndStaleActionsNeverChangeMedia(t *testing.T) {
	s, pending, root := scanJobFixture(t)
	job := checkpointCatalogScan(t, s, claimCatalogScan(t, s, pending), 1)
	if _, err := s.db.catalog.ChangeScanJob(context.Background(), job.ID, pending.Version, "cancel"); !errors.Is(err, catalog.ErrConflict) {
		t.Fatal("old revision accepted")
	}
	canceled, err := s.db.catalog.ChangeScanJob(context.Background(), job.ID, job.Version, "cancel")
	if err != nil || canceled.Status != "canceled" {
		t.Fatalf("cancel=%+v %v", canceled, err)
	}
	if len(catalogWorks(t, s, "")) != 0 {
		t.Fatal("cancel published inventory")
	}
	if _, err := os.Stat(filepath.Join(root, "folder-0", "TEST-001.mp4")); err != nil {
		t.Fatal("cancel changed media", err)
	}
	if _, err := s.db.catalog.ScanBatch(context.Background(), job.ID, job.Version, 1); !errors.Is(err, catalog.ErrConflict) {
		t.Fatal("old canceled worker can publish")
	}
	retried, err := s.db.catalog.ChangeScanJob(context.Background(), canceled.ID, canceled.Version, "retry")
	if err != nil || retried.Status != "pending" || retried.ProcessedFiles != 0 {
		t.Fatalf("explicit retry=%+v %v", retried, err)
	}
	finishCatalogScan(t, s, claimCatalogScan(t, s, retried))
}

func TestCatalogScanJobRejectsChangedFilesSidecarsAndRootWithoutSweeping(t *testing.T) {
	for _, change := range []string{"media", "nfo", "new-entry", "root"} {
		t.Run(change, func(t *testing.T) {
			s, job, root := scanJobFixture(t)
			job = checkpointCatalogScan(t, s, claimCatalogScan(t, s, job), 2)
			switch change {
			case "media":
				catalogWrite(t, filepath.Join(root, "folder-0"), "TEST-001.mp4", "replacement video")
			case "nfo":
				catalogWrite(t, filepath.Join(root, "folder-0"), "TEST-001.nfo", "<movie><title>Changed NFO</title></movie>")
			case "new-entry":
				catalogWrite(t, root, "NEW-009.mp4", "not in initial inventory")
			case "root":
				if err := os.Rename(root, root+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Remove(root); err != nil {
						t.Error(err)
						return
					}
					if err := os.Rename(root+"-original", root); err != nil {
						t.Error(err)
					}
				})
			}
			for range 100 {
				var err error
				job, err = s.db.catalog.ScanBatch(context.Background(), job.ID, job.Version, 1)
				if err != nil || job.Status != "running" {
					break
				}
			}
			if job.Status != "failed" || job.Error == "" || len(catalogWorks(t, s, "")) != 0 {
				t.Fatalf("changed snapshot published or failed silently: %+v", job)
			}
		})
	}
}

func TestCatalogScansNeverHideUnfinishedDeletionReceipts(t *testing.T) {
	for _, mode := range []string{"synchronous", "durable"} {
		t.Run(mode, func(t *testing.T) {
			s := testService(t)
			root := registrationRoot(t)
			writeIntakeFixture(t, root, map[string]string{"TEST-001.mp4": "original"})
			lib := catalogLibrary(t, s, root)
			catalogScan(t, s, lib)
			writeIntakeFixture(t, filepath.Join(root, ".catalog-delete-interrupted"), map[string]string{"receipt.json": "{}", "captured.mp4": "captured"})
			if mode == "synchronous" {
				if _, err := s.db.catalog.Scan(context.Background(), lib.ID); !errors.Is(err, catalog.ErrDeletePending) {
					t.Fatalf("legacy scanner hides unfinished deletion: %v", err)
				}
			} else {
				job := claimCatalogScan(t, s, startCatalogScan(t, s, lib.ID))
				job, err := s.db.catalog.ScanBatch(context.Background(), job.ID, job.Version, 1)
				if !errors.Is(err, catalog.ErrDeletePending) || job.Status != "failed" {
					t.Fatalf("durable scan hides unfinished deletion: %+v %v", job, err)
				}
			}
			if works := catalogWorks(t, s, ""); len(works) != 1 {
				t.Fatal("failed scan changed inventory")
			}
		})
	}
}

func TestCatalogScanDirectoryFrontierConsumesStagingBudget(t *testing.T) {
	s := testService(t)
	root := registrationRoot(t)
	if err := os.Mkdir(filepath.Join(root, strings.Repeat("deep-directory-", 14)), 0o700); err != nil {
		t.Fatal(err)
	}
	lib := catalogLibrary(t, s, root)
	job := claimCatalogScan(t, s, startCatalogScan(t, s, lib.ID))
	if _, err := s.db.db.Exec("UPDATE catalog_scan_jobs SET staged_bytes=? WHERE id=?", (128<<20)-64, job.ID); err != nil {
		t.Fatal(err)
	}
	job, err := s.db.catalog.ScanBatch(context.Background(), job.ID, job.Version, 1)
	if !errors.Is(err, catalog.ErrInvalid) || job.Status != "failed" {
		t.Fatalf("directory paths bypass budget: %+v %v", job, err)
	}
}

func TestCatalogScanManifestDetectsRestoredDirectoryTimestamp(t *testing.T) {
	s := testService(t)
	root := registrationRoot(t)
	writeIntakeFixture(t, root, map[string]string{"KEEP-001.mp4": "keep original"})
	lib := catalogLibrary(t, s, root)
	catalogScan(t, s, lib)
	old := catalogWorks(t, s, "")[0]
	away := filepath.Join(registrationRoot(t), "KEEP-001.mp4")
	if err := os.Rename(filepath.Join(root, "KEEP-001.mp4"), away); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	job := claimCatalogScan(t, s, startCatalogScan(t, s, lib.ID))
	job, err = s.db.catalog.ScanBatch(context.Background(), job.ID, job.Version, 1)
	if err != nil || job.Status != "running" {
		t.Fatal(job, err)
	}
	if err := os.Rename(away, filepath.Join(root, "KEEP-001.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(root, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	job, err = s.db.catalog.ScanBatch(context.Background(), job.ID, job.Version, 1)
	if !errors.Is(err, catalog.ErrConflict) || job.Status != "failed" {
		t.Fatalf("restored mtime accepted incomplete manifest: %+v %v", job, err)
	}
	if work, err := s.db.catalog.Work(context.Background(), old.ID); err != nil || work.AvailableCount != 1 {
		t.Fatal("existing file marked missing", work, err)
	}
}

func TestCatalogScanNFOContentChangeCannotHideBehindRestoredFingerprint(t *testing.T) {
	s, job, root := scanJobFixture(t)
	job = checkpointCatalogScan(t, s, claimCatalogScan(t, s, job), 2)
	nfo := filepath.Join(root, "folder-0", "TEST-001.nfo")
	info, err := os.Stat(nfo)
	if err != nil {
		t.Fatal(err)
	}
	// Same inode, size, and restored mtime; only a content-bound proof notices.
	catalogWrite(t, filepath.Dir(nfo), filepath.Base(nfo), "<movie><title>Tample TEST-001</title></movie>")
	if err := os.Chtimes(nfo, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	for range 100 {
		job, err = s.db.catalog.ScanBatch(context.Background(), job.ID, job.Version, 1)
		if err != nil || job.Status != "running" {
			break
		}
	}
	if !errors.Is(err, catalog.ErrConflict) || job.Status != "failed" || len(catalogWorks(t, s, "")) != 0 {
		t.Fatalf("stale NFO published: %+v %v", job, err)
	}
}
