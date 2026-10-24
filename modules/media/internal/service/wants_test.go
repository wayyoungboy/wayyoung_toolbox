package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

func wantRequest(t *testing.T, s *Service, method, target string, body any, status int) map[string]any {
	t.Helper()
	return resourceResponse(t, s, method, target, body, status)
}

func addWant(t *testing.T, s *Service, code string) map[string]any {
	t.Helper()
	return wantRequest(t, s, "POST", "/api/wants", map[string]any{"code": code}, 201)["want"].(map[string]any)
}

func readWant(t *testing.T, s *Service, id any) map[string]any {
	t.Helper()
	return wantRequest(t, s, "GET", fmt.Sprintf("/api/wants/%.0f", id), nil, 200)
}

func TestWantsAreIndependentReadOnlyUntilExplicitMutation(t *testing.T) {
	s := testService(t)
	page := wantRequest(t, s, "GET", "/api/wants?limit=48&offset=0", nil, 200)
	if len(page["items"].([]any)) != 0 || page["total"] != float64(0) {
		t.Fatal(page)
	}
	created := wantRequest(t, s, "POST", "/api/wants", map[string]any{"code": "test-001", "title": "Manual title", "release_date": "9999-01-01", "actors": []string{"Test Actor"}}, 201)
	want := created["want"].(map[string]any)
	if !created["created"].(bool) || want["code"] != "TEST-001" || want["state"] != "wanted" || want["phase"] != "unreleased" || want["version"] != float64(1) {
		t.Fatal(created)
	}
	again := wantRequest(t, s, "POST", "/api/wants", map[string]any{"code": "TEST-001", "title": "Do not overwrite"}, 200)
	if again["created"] != false || again["want"].(map[string]any)["title"] != "Manual title" {
		t.Fatal(again)
	}
	path := fmt.Sprintf("/api/wants/%.0f", want["id"])
	updated := wantRequest(t, s, "PATCH", path, map[string]any{"version": 1, "state": "given_up", "release_date": "2000-01-01"}, 200)
	if updated["phase"] != "given_up" || updated["version"] != float64(2) {
		t.Fatal(updated)
	}
	wantRequest(t, s, "PATCH", path, map[string]any{"version": 1, "title": "stale"}, 409)
	wantRequest(t, s, "PATCH", path, map[string]any{"version": 2, "state": "acquired"}, 400)
	for i := 0; i < 3; i++ {
		wantRequest(t, s, "GET", "/api/wants", nil, 200)
		wantRequest(t, s, "GET", "/api/resources/search?code=TEST-001&provider=javdb", nil, 200)
	}
	if latest := readWant(t, s, want["id"]); latest["version"] != float64(2) || latest["state"] != "given_up" {
		t.Fatal("GET changed intent", latest)
	}
	for _, table := range []string{"media_jobs", "task_runs", "javsp_runs", "catalog_assets", "catalog_progress", "catalog_personal"} {
		var count int
		if err := s.db.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("want changed %s: %d %v", table, count, err)
		}
	}
	wantRequest(t, s, "DELETE", path, map[string]any{"version": 1}, 409)
	wantRequest(t, s, "DELETE", path, map[string]any{"version": 2}, 200)
	wantRequest(t, s, "GET", path, nil, 404)
}

func TestWantsValidationAndAdministrativeGuards(t *testing.T) {
	s := testService(t)
	for _, body := range []map[string]any{{"code": "_646-C"}, {"code": "646-C"}, {"code": "TEST-001", "url": "http://unsafe.invalid"}, {"code": "TEST-001", "release_date": "2026-02-30"}, {"code": "TEST-001", "title": strings.Repeat("x", 1001)}, {"code": "TEST-001", "actors": []string{strings.Repeat("x", 201)}}, {"code": "TEST-001", "state": "acquired"}} {
		wantRequest(t, s, "POST", "/api/wants", body, 400)
	}
	for _, query := range []string{"limit=0", "limit=101", "offset=-1", "phase=unknown", "limit=1&limit=2", "extra=true", "query=" + strings.Repeat("x", 501)} {
		wantRequest(t, s, "GET", "/api/wants?"+query, nil, 400)
	}
	for _, admin := range []bool{false, true} {
		if w := queueAPIRequest(s, "POST", "/api/wants", `{"code":"TEST-001"}`, admin); w.Code != 403 {
			t.Fatalf("missing authorization accepted: %d", w.Code)
		}
	}
	wantRequest(t, s, "GET", "/api/wants/not-an-id", nil, 400)
}

func TestWantAcquisitionCommitsWithActualCatalogFilesAndProducer(t *testing.T) {
	for _, mode := range []string{"direct", "scan", "background", "producer"} {
		t.Run(mode, func(t *testing.T) {
			f := newMovieQueueFixture(t)
			want := addWant(t, f.s, "TEST-001")
			root := filepath.Join(f.root, "organized")
			writeIntakeFixture(t, root, map[string]string{"placeholder.txt": "not a movie"})
			lib := catalogLibrary(t, f.s, root)
			setCatalogAutomation(t, f.s, mode == "producer")
			if err := f.s.drainPendingMovies(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := readWant(t, f.s, want["id"]); got["state"] != "wanted" {
				t.Fatal("organization is not catalog publication", got)
			}
			switch mode {
			case "direct":
				if _, err := f.s.db.catalog.RegisterFiles(context.Background(), []catalog.RegistrationTarget{registrationTarget(t, f.target)}, nil); err != nil {
					t.Fatal(err)
				}
			case "scan":
				catalogScan(t, f.s, lib)
			case "background":
				wantRequest(t, f.s, "POST", fmt.Sprintf("/api/catalog/libraries/%d/scans", lib.ID), map[string]any{}, 202)
				if err := f.s.drainCatalogScans(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "producer":
				if err := f.s.drainPendingMovies(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			got := readWant(t, f.s, want["id"])
			proof, ok := got["acquisition"].(map[string]any)
			if got["state"] != "acquired" || got["phase"] != "acquired" || !ok || proof["asset_id"] == "" || proof["work_id"] == "" || proof["batch"] == "" {
				t.Fatal(got)
			}
			if mode == "producer" && proof["producer_job_id"] != float64(f.job.ID) {
				t.Fatal("producer proof lost", proof)
			}
			wantRequest(t, f.s, "POST", "/api/wants", map[string]any{"code": "TEST-001"}, 409)
		})
	}
}

func TestWantAcquisitionRollsBackWithCatalogPublication(t *testing.T) {
	s := testService(t)
	want := addWant(t, s, "TEST-001")
	root := registrationRoot(t)
	catalogLibrary(t, s, root)
	catalogWrite(t, root, "TEST-001.mp4", "neutral video")
	_, err := s.db.catalog.RegisterFiles(context.Background(), []catalog.RegistrationTarget{registrationTarget(t, filepath.Join(root, "TEST-001.mp4"))}, func(tx *sql.Tx, _ catalog.RegistrationResult) error { return errors.New("injected producer failure") })
	if err == nil {
		t.Fatal("injected failure ignored")
	}
	if got := readWant(t, s, want["id"]); got["state"] != "wanted" || got["acquisition"] != nil {
		t.Fatal("partial acquisition survived rollback", got)
	}
	if len(catalogWorks(t, s, "")) != 0 {
		t.Fatal("failed receipt published catalog")
	}
}

func TestWantDownloadAssociationDoesNotImplyAcquired(t *testing.T) {
	s, selection := resourceDownloadFixture(t)
	want := addWant(t, s, "TEST-001")
	job, created, err := s.db.EnqueueResourceDownload(context.Background(), selection)
	if err != nil || !created {
		t.Fatalf("enqueue: %v", err)
	}
	got := readWant(t, s, want["id"])
	if got["state"] != "wanted" || got["last_job_id"] != float64(job.ID) || got["acquisition"] != nil {
		t.Fatal("download association is not acquisition", got)
	}
	// Removing the want is only a list mutation; it cannot cancel the job.
	wantRequest(t, s, "DELETE", fmt.Sprintf("/api/wants/%.0f", want["id"]), map[string]any{"version": got["version"]}, http.StatusOK)
	current, err := s.db.MediaJob(job.ID)
	if err != nil || current.Status != "pending" || current.Version != job.Version {
		t.Fatal("want removal touched downloader", current, err)
	}
}

func TestWantHistoricalReceiptCannotClaimRehomedAssetStillMatches(t *testing.T) {
	s := testService(t)
	want := addWant(t, s, "TEST-001")
	root := registrationRoot(t)
	catalogWrite(t, root, "unknown.mp4", "neutral unchanged video")
	catalogWrite(t, root, "unknown.nfo", "<movie><num>TEST-001</num><title>Original identity</title></movie>")
	lib := catalogLibrary(t, s, root)
	catalogScan(t, s, lib)
	if got := readWant(t, s, want["id"]); got["available"] != true {
		t.Fatal(got)
	}
	catalogWrite(t, root, "unknown.nfo", "<movie><num>TEST-002</num><title>Corrected identity</title></movie>")
	catalogScan(t, s, lib)
	got := readWant(t, s, want["id"])
	if got["state"] != "acquired" || got["available"] != false {
		t.Fatal("historical receipt incorrectly matches another work", got)
	}
}
