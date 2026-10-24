package service

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

func TestCatalogScanWorkerPublishesOnlyExplicitJobsAndRespectsWriterGuards(t *testing.T) {
	s, job, _ := scanJobFixture(t)
	s.mediaFiles.Lock()
	if err := s.drainCatalogScans(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.mediaFiles.Unlock()
	current, err := s.db.catalog.ScanJob(t.Context(), job.ID)
	if err != nil || current.Status != "pending" {
		t.Fatalf("busy writer consumed claim: %+v %v", current, err)
	}
	transfer := enqueueQueueTestJob(t, s.db, "torrent:scan-guard")
	transfer, err = s.db.ClaimMediaJob(transfer.ID, transfer.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.drainCatalogScans(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err = s.db.catalog.ScanJob(t.Context(), job.ID)
	if err != nil || current.Status != "pending" {
		t.Fatalf("captured transfer guard ignored: %+v %v", current, err)
	}
	if _, err := s.db.TransitionMediaJob(transfer.ID, transfer.Version, MediaJobTransition{Phase: "done", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	if err := s.drainCatalogScans(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err = s.db.catalog.ScanJob(t.Context(), job.ID)
	if err != nil || current.Status != "success" || len(catalogWorks(t, s, "")) != 4 {
		t.Fatalf("worker did not publish: %+v %v", current, err)
	}
	// Empty durable queue must not discover an unrequested library.
	root := registrationRoot(t)
	writeIntakeFixture(t, root, map[string]string{"OTHER-009.mp4": "not requested"})
	catalogLibrary(t, s, root)
	if err := s.drainCatalogScans(t.Context()); err != nil || len(catalogWorks(t, s, "")) != 4 {
		t.Fatal("worker silently scanned another library", err)
	}
}

func TestCatalogScanWorkerReleasePreservesCheckpointAndNeverOverwritesPause(t *testing.T) {
	s, job, _ := scanJobFixture(t)
	job = checkpointCatalogScan(t, s, claimCatalogScan(t, s, job), 2)
	if err := s.db.catalog.ReleaseScanAttempt(t.Context(), job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	released, err := s.db.catalog.ScanJob(t.Context(), job.ID)
	if err != nil || released.Status != "pending" || released.ProcessedFiles != job.ProcessedFiles || released.Version <= job.Version {
		t.Fatalf("shutdown discarded cursor: %+v %v", released, err)
	}
	job = claimCatalogScan(t, s, released)
	paused, err := s.db.catalog.ChangeScanJob(t.Context(), job.ID, job.Version, "pause")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.catalog.ReleaseScanAttempt(t.Context(), job.ID, job.Version); !errors.Is(err, catalog.ErrConflict) {
		t.Fatal("old shutdown overwrote pause", err)
	}
	current, err := s.db.catalog.ScanJob(t.Context(), job.ID)
	if err != nil || current.Status != "paused" || current.Version != paused.Version {
		t.Fatal(current, err)
	}
}

func TestCatalogScanPauseCancelsExactAttemptBeforeWaitingForDatabase(t *testing.T) {
	s, job, _ := scanJobFixture(t)
	job = claimCatalogScan(t, s, job)
	ctx, cleanup := s.catalogScanContext(t.Context(), job)
	defer cleanup()
	s.cancelCatalogScanAttempt(job.ID, job.Version-1)
	if ctx.Err() != nil {
		t.Fatal("stale cancellation stopped new scan")
	}
	tx, err := s.db.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	request := httptest.NewRequest("POST", fmt.Sprintf("/api/catalog/scans/%d/pause", job.ID), strings.NewReader(fmt.Sprintf(`{"version":%d}`, job.Version)))
	request.Header.Set("X-Trim-Isadmin", "true")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Catalog-CSRF", s.catalogCSRF)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Handler().ServeHTTP(response, request)
	}()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("pause waited for sole SQLite connection before canceling active I/O")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pause did not persist after connection released")
	}
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	current, err := s.db.catalog.ScanJob(t.Context(), job.ID)
	if err != nil || current.Status != "paused" {
		t.Fatal(current, err)
	}
}

func TestCatalogScanPublicationFailureRollsBackInventoryAndSuccessTogether(t *testing.T) {
	s, job, root := scanJobFixture(t)
	if _, err := s.db.db.Exec(`CREATE TEMP TRIGGER reject_scan_success BEFORE UPDATE ON catalog_scan_jobs WHEN NEW.status='success' BEGIN SELECT RAISE(ABORT,'synthetic publish fault'); END`); err != nil {
		t.Fatal(err)
	}
	job = claimCatalogScan(t, s, job)
	var runErr error
	for range 100 {
		job, runErr = s.db.catalog.ScanBatch(t.Context(), job.ID, job.Version, 1)
		if runErr != nil || job.Status != "running" {
			break
		}
	}
	if runErr == nil || job.Status != "failed" || len(catalogWorks(t, s, "")) != 0 {
		t.Fatalf("partial inventory escaped publication rollback: %+v %v", job, runErr)
	}
	if root == "" || !s.mediaFiles.TryLock() {
		t.Fatal("fixture or writer lock leaked")
	}
	s.mediaFiles.Unlock()
}
