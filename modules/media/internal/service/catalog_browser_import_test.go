package service

import (
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

type browserTestSource struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Reading bool   `json:"reading"`
	Version int64  `json:"version"`
	Records int    `json:"records"`
	Matched int    `json:"matched"`
}
type browserTestResult struct {
	Source    browserTestSource `json:"source"`
	Duplicate bool              `json:"duplicate"`
	Added     int               `json:"added"`
}

func browserPayload(name, kind, data string) map[string]any {
	return map[string]any{"name": name, "kind": kind, "filename": "export.json", "data": base64.StdEncoding.EncodeToString([]byte(data)), "version": 0}
}
func browserPreview(t *testing.T, s *Service, body map[string]any) map[string]any {
	t.Helper()
	p := catalogJSON[map[string]any](t, s, "POST", "/api/catalog/browser/import/preview", body, 200)
	body["digest"] = p["digest"]
	body["confirmation"] = "导入资料"
	return p
}
func browserCommit(t *testing.T, s *Service, body map[string]any) browserTestResult {
	t.Helper()
	return catalogJSON[browserTestResult](t, s, "POST", "/api/catalog/browser/import", body, 200)
}

func TestCatalogBrowserImportPreviewMergeStopDeleteAndTaste(t *testing.T) {
	s, _, works := personalFixture(t)
	body := browserPayload("Desktop history", "history", `[{"url":"https://www.javbus.com/DEMO-100?token=private","dt":1700000000,"metadata":{"title":"DEMO-100 Neutral"}},{"url":"https://example.test/unmatched","dt":1700000000}]`)
	preview := browserPreview(t, s, body)
	if preview["records"] != float64(2) || preview["matched"] != float64(1) {
		t.Fatalf("preview: %+v", preview)
	}
	list := catalogJSON[map[string][]browserTestSource](t, s, "GET", "/api/catalog/browser/sources", nil, 200)
	if len(list["items"]) != 0 {
		t.Fatal("preview mutated sources")
	}
	first := browserCommit(t, s, body)
	if first.Source.ID == "" || first.Source.Records != 2 || first.Source.Matched != 1 || !first.Source.Reading || first.Added != 2 {
		t.Fatalf("commit: %+v", first)
	}
	second := browserCommit(t, s, body)
	if !second.Duplicate || second.Source.ID != first.Source.ID || second.Source.Version != first.Source.Version {
		t.Fatal("duplicate upload grew evidence")
	}
	if tasteReport(t, s).Config.Enabled {
		t.Fatal("import enabled analysis")
	}
	tasteConfig(t, s, true, "browser")
	report := tasteReport(t, s)
	if report.Total != 1 || report.Items[0].Work.ID != works[1].ID {
		t.Fatalf("import did not inform related-work recommendation: %+v", report)
	}
	found := false
	for _, r := range report.Items[0].Reasons {
		if r.Source == "browser" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing browser provenance")
	}
	sourcePath := "/api/catalog/browser/sources/" + first.Source.ID
	stopped := catalogJSON[browserTestSource](t, s, "PUT", sourcePath, map[string]any{"version": first.Source.Version, "reading": false}, 200)
	if tasteReport(t, s).Total != report.Total {
		t.Fatal("stop reading erased retained snapshot contribution")
	}
	updated := browserPayload("Desktop history", "history", `[{"url":"https://www.javbus.com/DEMO-100","dt":1700000001}]`)
	updated["source_id"] = stopped.ID
	updated["version"] = stopped.Version
	browserPreview(t, s, updated)
	if w := catalogRequest(t, s, "POST", "/api/catalog/browser/import", updated); w.Code != 409 {
		t.Fatalf("stopped source updated: %d", w.Code)
	}
	resumed := catalogJSON[browserTestSource](t, s, "PUT", sourcePath, map[string]any{"version": stopped.Version, "reading": true}, 200)
	updated["version"] = resumed.Version
	merged := browserCommit(t, s, updated)
	if merged.Source.Records != 3 || merged.Added != 1 {
		t.Fatalf("history was replaced, not merged: %+v", merged)
	}
	if after := tasteReport(t, s); after.Items[0].Score != report.Items[0].Score {
		t.Fatal("repeated work visits amplified preference")
	}
	if w := catalogRequest(t, s, "DELETE", sourcePath, map[string]any{"version": resumed.Version, "confirmation": "删除来源"}); w.Code != 409 {
		t.Fatal("stale delete accepted")
	}
	catalogJSON[map[string]bool](t, s, "DELETE", sourcePath, map[string]any{"version": merged.Source.Version, "confirmation": "删除来源"}, 200)
	if tasteReport(t, s).Total != 0 {
		t.Fatal("deleted source retained contribution")
	}
	if w := catalogRequest(t, s, "POST", "/api/catalog/browser/import", updated); w.Code != 404 {
		t.Fatal("deleted source resurrected by stale upload")
	}
	if len(catalogWorks(t, s, "")) != 2 {
		t.Fatal("source delete removed media")
	}
}

func TestCatalogBrowserBookmarksAreAtomicSnapshotsAndRestartSafe(t *testing.T) {
	s, _, _ := personalFixture(t)
	body := browserPayload("Bookmarks", "bookmarks", `[{"url":"https://www.javbus.com/DEMO-100","title":"Neutral"}]`)
	browserPreview(t, s, body)
	first := browserCommit(t, s, body)
	body = browserPayload("Bookmarks", "bookmarks", `[]`)
	body["source_id"] = first.Source.ID
	body["version"] = first.Source.Version
	browserPreview(t, s, body)
	if _, err := s.db.db.Exec(`CREATE TRIGGER fail_browser_snapshot BEFORE UPDATE ON catalog_browser_sources BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if w := catalogRequest(t, s, "POST", "/api/catalog/browser/import", body); w.Code != 500 {
		t.Fatalf("fault injection: %d", w.Code)
	}
	if _, err := s.db.db.Exec("DROP TRIGGER fail_browser_snapshot"); err != nil {
		t.Fatal(err)
	}
	list := catalogJSON[map[string][]browserTestSource](t, s, "GET", "/api/catalog/browser/sources", nil, 200)
	if list["items"][0].Records != 1 {
		t.Fatal("failed snapshot destroyed old data")
	}
	result := browserCommit(t, s, body)
	if result.Source.Records != 0 {
		t.Fatal("bookmark snapshot did not replace")
	}
	reopened, err := catalog.New(s.db.db, unambiguousMediaID)
	if err != nil {
		t.Fatal(err)
	}
	s.db.catalog = reopened
	list = catalogJSON[map[string][]browserTestSource](t, s, "GET", "/api/catalog/browser/sources", nil, 200)
	if len(list["items"]) != 1 || list["items"][0].Records != 0 || list["items"][0].Version != result.Source.Version {
		t.Fatal("restart changed source")
	}
}

func TestCatalogBrowserImportRejectsInvalidConfirmationAndAccess(t *testing.T) {
	s, _, _ := personalFixture(t)
	body := browserPayload("History", "history", `[{"url":"https://example.test/item","dt":1700000000}]`)
	for _, change := range []map[string]any{{"name": ""}, {"kind": "bad"}, {"data": "bad-base64"}, {"filename": "../export.json"}, {"version": -1}} {
		copy := map[string]any{}
		for k, v := range body {
			copy[k] = v
		}
		for k, v := range change {
			copy[k] = v
		}
		if w := catalogRequest(t, s, "POST", "/api/catalog/browser/import/preview", copy); w.Code != 400 {
			t.Fatalf("bad field accepted: %+v %d", change, w.Code)
		}
	}
	browserPreview(t, s, body)
	body["confirmation"] = "wrong"
	if w := catalogRequest(t, s, "POST", "/api/catalog/browser/import", body); w.Code != 400 {
		t.Fatal("unconfirmed import")
	}
	body["confirmation"] = "导入资料"
	body["digest"] = "stale"
	if w := catalogRequest(t, s, "POST", "/api/catalog/browser/import", body); w.Code != 409 {
		t.Fatal("changed preview accepted")
	}
	for _, path := range []string{"/api/catalog/browser/sources", "/api/catalog/browser/import/preview"} {
		method := "GET"
		if strings.Contains(path, "preview") {
			method = "POST"
		}
		r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("anonymous browser data: %d", w.Code)
		}
	}
	r := httptest.NewRequest("POST", "/api/catalog/browser/import/preview", strings.NewReader(`{}`))
	r.Header.Set("X-Trim-Isadmin", "true")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
}

func TestCatalogBrowserIdentityRejectsAmbiguityAndUnknownSites(t *testing.T) {
	s, _, _ := personalFixture(t)
	body := browserPayload("Ambiguous", "history", `[
 {"url":"https://www.javbus.com/DEMO-100/DEMO-101","dt":1700000000,"metadata":{"title":"DEMO-100"}},
 {"url":"https://www.javbus.com/DEMO-100","dt":1700000001,"metadata":{"title":"DEMO-100 DEMO-101"}},
 {"url":"https://example.test/DEMO-100","dt":1700000002,"metadata":{"title":"DEMO-100"}},
 {"url":"https://www.javbus.com/DEMO-100","dt":1700000003,"metadata":{"title":"DEMO-101"}}]`)
	preview := browserPreview(t, s, body)
	if preview["matched"] != float64(0) {
		t.Fatalf("ambiguous/unknown identities guessed: %+v", preview)
	}
	body = browserPayload("Explicit", "history", `[{"url":"https://javdb.com/v/opaque","dt":1700000000,"metadata":{"title":"DEMO-100 Neutral"}}]`)
	preview = browserPreview(t, s, body)
	if preview["matched"] != float64(1) {
		t.Fatal("explicit unique title identity not recognized")
	}
}
