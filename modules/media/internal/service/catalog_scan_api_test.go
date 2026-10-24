package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

func TestCatalogScanAPIQueuesWithoutScanningAndUsesLifecycleCAS(t *testing.T) {
	s := testService(t)
	root := registrationRoot(t)
	writeIntakeFixture(t, root, map[string]string{"TEST-001.mp4": "synthetic"})
	lib := catalogLibrary(t, s, root)
	response := catalogRequest(t, s, "POST", fmt.Sprintf("/api/catalog/libraries/%d/scans", lib.ID), struct{}{})
	if response.Code != http.StatusAccepted {
		t.Fatalf("queue=%d %s", response.Code, response.Body.String())
	}
	var job catalog.ScanJob
	decodeResponseJSON(t, response, &job)
	if job.ID < 1 || job.Status != "pending" || job.ProcessedFiles != 0 || len(catalogWorks(t, s, "")) != 0 {
		t.Fatalf("handler scanned or did not queue: %+v", job)
	}
	if response.Header().Get("Location") != fmt.Sprintf("%s/api/catalog/scans/%d", s.prefix, job.ID) {
		t.Fatal("missing durable job location")
	}
	if again := catalogRequest(t, s, "POST", fmt.Sprintf("/api/catalog/libraries/%d/scans", lib.ID), struct{}{}); again.Code != 409 {
		t.Fatalf("duplicate active scan=%d", again.Code)
	}
	for _, action := range []string{"pause", "resume", "cancel", "retry"} {
		oldVersion := job.Version
		response = catalogRequest(t, s, "POST", fmt.Sprintf("/api/catalog/scans/%d/%s", job.ID, action), map[string]int64{"version": oldVersion})
		if response.Code != 200 {
			t.Fatalf("%s=%d %s", action, response.Code, response.Body.String())
		}
		decodeResponseJSON(t, response, &job)
		if job.Version <= oldVersion {
			t.Fatal("lifecycle revision not advanced")
		}
		if stale := catalogRequest(t, s, "POST", fmt.Sprintf("/api/catalog/scans/%d/cancel", job.ID), map[string]int64{"version": oldVersion}); stale.Code != 409 {
			t.Fatalf("stale mutation accepted=%d", stale.Code)
		}
	}
	response = catalogRequest(t, s, "GET", fmt.Sprintf("/api/catalog/scans/%d", job.ID), nil)
	if response.Code != 200 || strings.Contains(response.Body.String(), "root_identity") {
		t.Fatalf("job status=%d %s", response.Code, response.Body.String())
	}
}

func TestCatalogScanAPIValidatesBodyIDsAndPagination(t *testing.T) {
	s := testService(t)
	lib := catalogLibrary(t, s, registrationRoot(t))
	create := fmt.Sprintf("/api/catalog/libraries/%d/scans", lib.ID)
	for _, body := range []string{"null", "[]", `{"root":"/"}`, "{} {}", ""} {
		request := httptest.NewRequest("POST", create, strings.NewReader(body))
		request.Header.Set("X-Trim-Isadmin", "true")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Catalog-CSRF", s.catalogCSRF)
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		if response.Code != 400 {
			t.Fatalf("body %q accepted=%d", body, response.Code)
		}
	}
	for index := range 3 {
		job := startCatalogScan(t, s, lib.ID)
		if index < 2 {
			if _, err := s.db.catalog.ChangeScanJob(t.Context(), job.ID, job.Version, "cancel"); err != nil {
				t.Fatal(err)
			}
		}
	}
	var page struct {
		Items      []catalog.ScanJob `json:"items"`
		NextCursor int64             `json:"next_cursor"`
	}
	response := catalogRequest(t, s, "GET", "/api/catalog/scans?limit=2", nil)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	decodeResponseJSON(t, response, &page)
	if len(page.Items) != 2 || page.Items[0].ID <= page.Items[1].ID || page.NextCursor != page.Items[1].ID {
		t.Fatalf("first page=%+v", page)
	}
	response = catalogRequest(t, s, "GET", fmt.Sprintf("/api/catalog/scans?limit=2&before_id=%d", page.NextCursor), nil)
	decodeResponseJSON(t, response, &page)
	if len(page.Items) != 1 || page.NextCursor != 0 {
		t.Fatalf("last page=%+v", page)
	}
	for _, query := range []string{"limit=0", "limit=101", "limit=no", "before_id=-1", "before_id=no", "limit=2&limit=3", "unknown=1"} {
		if response := catalogRequest(t, s, "GET", "/api/catalog/scans?"+query, nil); response.Code != 400 {
			t.Fatalf("query %q accepted=%d", query, response.Code)
		}
	}
	for _, id := range []string{"0", "-1", "no"} {
		if response := catalogRequest(t, s, "GET", "/api/catalog/scans/"+id, nil); response.Code != 400 {
			t.Fatalf("invalid ID %q=%d", id, response.Code)
		}
	}
	for _, body := range []any{nil, json.RawMessage("null"), map[string]any{"version": 0}, map[string]any{"version": 1, "path": "/"}} {
		if response := catalogRequest(t, s, "POST", fmt.Sprintf("/api/catalog/scans/%d/pause", page.Items[0].ID), body); response.Code != 400 {
			t.Fatalf("invalid lifecycle body=%d %s", response.Code, response.Body.String())
		}
	}
}

func TestCatalogScanAPIRequiresAdministratorCSRFAndSameOrigin(t *testing.T) {
	s := testService(t)
	lib := catalogLibrary(t, s, registrationRoot(t))
	for _, test := range []struct {
		name, method, target, csrf, site string
		admin                            bool
	}{
		{"read not admin", "GET", "/api/catalog/scans", "", "", false},
		{"write not admin", "POST", fmt.Sprintf("/api/catalog/libraries/%d/scans", lib.ID), s.catalogCSRF, "", false},
		{"missing csrf", "POST", fmt.Sprintf("/api/catalog/libraries/%d/scans", lib.ID), "", "", true},
		{"cross-site", "POST", fmt.Sprintf("/api/catalog/libraries/%d/scans", lib.ID), s.catalogCSRF, "cross-site", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.target, strings.NewReader("{}"))
			if test.admin {
				request.Header.Set("X-Trim-Isadmin", "true")
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Catalog-CSRF", test.csrf)
			request.Header.Set("Sec-Fetch-Site", test.site)
			response := httptest.NewRecorder()
			s.Handler().ServeHTTP(response, request)
			if response.Code != 403 {
				t.Fatalf("guard=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestCatalogScanAPILibraryHistoryCannotBeHiddenByOtherLibraries(t *testing.T) {
	s := testService(t)
	first := catalogLibrary(t, s, registrationRoot(t))
	second := catalogLibrary(t, s, registrationRoot(t))
	job := startCatalogScan(t, s, first.ID)
	startCatalogScan(t, s, second.ID)
	response := catalogRequest(t, s, "GET", fmt.Sprintf("/api/catalog/libraries/%d/scans?limit=1", first.ID), nil)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var page struct {
		Items []catalog.ScanJob `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].ID != job.ID {
		t.Fatalf("wrong library scan history: %+v %v", page, err)
	}
	if response := catalogRequest(t, s, "GET", "/api/catalog/libraries/99999/scans", nil); response.Code != 404 {
		t.Fatalf("unknown library history=%d", response.Code)
	}
	old, err := s.db.catalog.ChangeScanJob(t.Context(), job.ID, job.Version, "cancel")
	if err != nil {
		t.Fatal(err)
	}
	newer := startCatalogScan(t, s, first.ID)
	if _, err := s.db.catalog.ChangeScanJob(t.Context(), newer.ID, newer.Version, "cancel"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.catalog.ChangeScanJob(t.Context(), old.ID, old.Version, "retry"); err != nil {
		t.Fatal(err)
	}
	response = catalogRequest(t, s, "GET", fmt.Sprintf("/api/catalog/libraries/%d/scans?limit=1", first.ID), nil)
	var active struct {
		Active *catalog.ScanJob `json:"active"`
	}
	decodeResponseJSON(t, response, &active)
	if active.Active == nil || active.Active.ID != old.ID || active.Active.Status != "pending" {
		t.Fatalf("retry of older job hidden by newer history: %+v", active)
	}
}
