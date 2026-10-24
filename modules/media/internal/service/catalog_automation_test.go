package service

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type catalogAutomationFixture struct {
	AfterOrganize bool  `json:"after_organize"`
	Version       int64 `json:"version"`
}

func setCatalogAutomation(t *testing.T, s *Service, enabled bool) catalogAutomationFixture {
	t.Helper()
	w := catalogRequest(t, s, "GET", "/api/catalog/automation", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("read automation=%d %s", w.Code, w.Body.String())
	}
	var config catalogAutomationFixture
	decodeResponseJSON(t, w, &config)
	w = catalogRequest(t, s, "PUT", "/api/catalog/automation", map[string]any{"after_organize": enabled, "version": config.Version})
	if w.Code != http.StatusOK {
		t.Fatalf("save automation=%d %s", w.Code, w.Body.String())
	}
	decodeResponseJSON(t, w, &config)
	return config
}

func TestCatalogAutomationDefaultsOffAndRequiresFreshExplicitConfiguration(t *testing.T) {
	s := testService(t)
	w := catalogRequest(t, s, "GET", "/api/catalog/automation", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("automation=%d %s", w.Code, w.Body.String())
	}
	var config catalogAutomationFixture
	decodeResponseJSON(t, w, &config)
	if config.AfterOrganize || config.Version != 1 {
		t.Fatalf("upgrade opted into scanning: %+v", config)
	}
	saved := setCatalogAutomation(t, s, true)
	if !saved.AfterOrganize || saved.Version != 2 {
		t.Fatalf("config not saved: %+v", saved)
	}
	for _, body := range []map[string]any{{"version": 2}, {"version": 0, "after_organize": true}, {"version": 2, "after_organize": nil}, {"version": 2, "after_organize": true, "root": "/"}} {
		if w := catalogRequest(t, s, "PUT", "/api/catalog/automation", body); w.Code != 400 {
			t.Fatalf("invalid config accepted: %d %s", w.Code, w.Body.String())
		}
	}
	if w := catalogRequest(t, s, "PUT", "/api/catalog/automation", map[string]any{"version": 1, "after_organize": false}); w.Code != 409 {
		t.Fatalf("stale config=%d", w.Code)
	}
	for _, admin := range []bool{false, true} {
		// Neither a missing admin nor missing CSRF may mutate this setting.
		w := queueAPIRequest(s, "PUT", "/api/catalog/automation", fmt.Sprintf(`{"version":%d,"after_organize":false}`, saved.Version), admin)
		if w.Code != 403 {
			t.Fatalf("guard admin=%t status=%d", admin, w.Code)
		}
	}
	if works := catalogWorks(t, s, ""); len(works) != 0 {
		t.Fatal("saving config scanned files")
	}
}

func TestMovieQueueRegistersOnlyAfterConfirmedOrganization(t *testing.T) {
	f := newMovieQueueFixture(t)
	organized := filepath.Join(f.root, "organized")
	if err := os.MkdirAll(organized, 0o700); err != nil {
		t.Fatal(err)
	}
	catalogLibrary(t, f.s, organized)
	setCatalogAutomation(t, f.s, true)
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := f.s.db.MediaJob(f.job.ID)
	if err != nil || job.Phase != "catalog" || job.Status != "pending" {
		t.Fatalf("missing durable successor: %+v %v", job, err)
	}
	if works := catalogWorks(t, f.s, ""); len(works) != 0 {
		t.Fatal("organization silently ran a full scan")
	}
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.MediaJob(job.ID)
	works := catalogWorks(t, f.s, "")
	if err != nil || job.Phase != "done" || job.Status != "success" || f.runs != 1 || len(works) != 1 || works[0].Title != "Example title" {
		t.Fatalf("registration failed or reran organizer: job=%+v works=%+v runs=%d %v", job, works, f.runs, err)
	}
	var histories int
	if err := f.s.db.db.QueryRow("SELECT count(*) FROM javsp_runs").Scan(&histories); err != nil || histories != 1 {
		t.Fatalf("registration created JAVSP history: %d %v", histories, err)
	}
}

func TestMovieRegistrationMissingLibraryRetriesOnlyRegistration(t *testing.T) {
	f := newMovieQueueFixture(t)
	setCatalogAutomation(t, f.s, true)
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := f.s.db.MediaJob(f.job.ID)
	if err != nil || job.Phase != "catalog" || job.Status != "needs_attention" || job.Error == "" {
		t.Fatalf("unregistered output reported success: %+v %v", job, err)
	}
	catalogLibrary(t, f.s, filepath.Join(f.root, "organized"))
	if _, err := f.s.db.RetryMediaJob(job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.MediaJob(job.ID)
	if err != nil || job.Status != "success" || len(catalogWorks(t, f.s, "")) != 1 || f.runs != 1 {
		t.Fatalf("retry did not resume only registration: %+v runs=%d %v", job, f.runs, err)
	}
}

func TestMoviePartialOrganizationDoesNotRegisterCommittedPaths(t *testing.T) {
	f := newMovieQueueFixture(t)
	if err := os.MkdirAll(filepath.Join(f.root, "organized"), 0o700); err != nil {
		t.Fatal(err)
	}
	catalogLibrary(t, f.s, filepath.Join(f.root, "organized"))
	setCatalogAutomation(t, f.s, true)
	f.afterMoveError = context.DeadlineExceeded
	if err := f.s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := f.s.db.MediaJob(f.job.ID)
	if err != nil || job.Phase != "organize" || job.Status == "success" || len(catalogWorks(t, f.s, "")) != 0 {
		t.Fatalf("partial outputs were registered: %+v %v", job, err)
	}
}
