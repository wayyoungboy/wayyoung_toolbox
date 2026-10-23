package service

import (
	"context"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

type tasteTestConfig struct {
	Enabled bool            `json:"enabled"`
	Sources map[string]bool `json:"sources"`
	Version int64           `json:"version"`
}
type tasteTestReport struct {
	Config  tasteTestConfig `json:"config"`
	Sources []struct {
		Key     string `json:"key"`
		Records int    `json:"records"`
		Works   int    `json:"works"`
		Enabled bool   `json:"enabled"`
	} `json:"sources"`
	Facets []struct {
		Kind     string  `json:"kind"`
		Key      string  `json:"key"`
		Label    string  `json:"label"`
		Weight   float64 `json:"weight"`
		Evidence int     `json:"evidence_count"`
	} `json:"facets"`
	Items []struct {
		Work    catalog.Work `json:"work"`
		Score   float64      `json:"score"`
		Reasons []struct {
			Label   string   `json:"label"`
			Source  string   `json:"source"`
			WorkIDs []string `json:"work_ids"`
		} `json:"reasons"`
	} `json:"items"`
	Total int `json:"total"`
}

func tasteConfig(t *testing.T, s *Service, enabled bool, sources ...string) tasteTestConfig {
	t.Helper()
	current := catalogJSON[tasteTestConfig](t, s, "GET", "/api/catalog/taste/config", nil, 200)
	for key := range current.Sources {
		current.Sources[key] = false
	}
	for _, key := range sources {
		current.Sources[key] = true
	}
	current.Enabled = enabled
	return catalogJSON[tasteTestConfig](t, s, "PUT", "/api/catalog/taste/config", current, 200)
}
func tasteReport(t *testing.T, s *Service) tasteTestReport {
	t.Helper()
	return catalogJSON[tasteTestReport](t, s, "GET", "/api/catalog/taste?limit=24", nil, 200)
}

func TestCatalogTasteOptInSourceControlsAndClear(t *testing.T) {
	s, _, works := personalFixture(t)
	catalogJSON[catalog.Work](t, s, "PATCH", "/api/catalog/works/"+works[0].ID+"/personal", map[string]any{"favorite": true}, 200)
	report := tasteReport(t, s)
	if report.Config.Enabled || len(report.Facets) != 0 || len(report.Items) != 0 || report.Total != 0 {
		t.Fatalf("analysis not opt-in: %+v", report)
	}
	config := tasteConfig(t, s, true, "collection")
	report = tasteReport(t, s)
	if len(report.Facets) == 0 || report.Total != 1 || report.Items[0].Work.ID != works[1].ID || report.Items[0].Score <= 0 || len(report.Items[0].Reasons) == 0 {
		t.Fatalf("missing evidence-based recommendation: %+v", report)
	}
	for _, reason := range report.Items[0].Reasons {
		if reason.Source != "collection" || reason.Label == "" || len(reason.WorkIDs) != 1 || reason.WorkIDs[0] != works[0].ID {
			t.Fatalf("unproven reason: %+v", reason)
		}
	}
	config = tasteConfig(t, s, true)
	report = tasteReport(t, s)
	if len(report.Facets) != 0 || report.Total != 0 {
		t.Fatalf("stopped source still contributes: %+v", report)
	}
	if !catalogDetail(t, s, works[0].ID).Favorite {
		t.Fatal("stopping analysis erased original favorite")
	}
	config = tasteConfig(t, s, true, "collection")
	if tasteReport(t, s).Total != 1 {
		t.Fatal("resume did not use retained source")
	}
	if w := catalogRequest(t, s, "POST", "/api/catalog/taste/reset", map[string]any{"version": config.Version, "confirmation": "wrong"}); w.Code != 400 {
		t.Fatalf("unconfirmed clear: %d", w.Code)
	}
	config = catalogJSON[tasteTestConfig](t, s, "POST", "/api/catalog/taste/reset", map[string]any{"version": config.Version, "confirmation": "清除偏好"}, 200)
	if config.Enabled || tasteReport(t, s).Total != 0 || !catalogDetail(t, s, works[0].ID).Favorite {
		t.Fatal("clear did not disable derived analysis or erased original state")
	}
	if config.Sources["collection"] {
		t.Fatal("clear should stop all analysis sources")
	}
	store, err := catalog.New(s.db.db, unambiguousMediaID)
	if err != nil {
		t.Fatal(err)
	}
	s.db.catalog = store
	if tasteReport(t, s).Config.Enabled {
		t.Fatal("restart enabled analysis without consent")
	}
}

func TestCatalogTasteHistoryDeduplicatesWorksNotHeartbeatsOrEditions(t *testing.T) {
	s, _, works := personalFixture(t)
	work := catalogDetail(t, s, works[0].ID)
	asset := work.Assets[0].ID
	for i, session := range []string{"taste-session-one", "taste-session-two"} {
		catalogJSON[catalog.Progress](t, s, "PUT", "/api/catalog/assets/"+asset+"/progress", map[string]any{"position": 60, "duration": 100, "revision": i, "session_id": session}, 200)
	}
	tasteConfig(t, s, true, "history")
	first := tasteReport(t, s)
	if first.Total != 1 || first.Sources[1].Key != "history" || first.Sources[1].Works != 1 {
		t.Fatalf("bad source evidence: %+v", first)
	}
	catalogJSON[catalog.Progress](t, s, "PUT", "/api/catalog/assets/"+asset+"/progress", map[string]any{"position": 70, "duration": 100, "revision": 2, "session_id": "taste-session-two"}, 200)
	second := tasteReport(t, s)
	if !reflect.DeepEqual(first.Facets, second.Facets) || first.Items[0].Score != second.Items[0].Score {
		t.Fatal("repeated saves amplified preference")
	}
	rows := catalogJSON[catalog.HistoryPage](t, s, "GET", "/api/catalog/history", nil, 200)
	ids := []string{}
	for _, item := range rows.Items {
		ids = append(ids, item.ID)
	}
	catalogRequest(t, s, "POST", "/api/catalog/history/clear", map[string]any{"ids": ids})
	if report := tasteReport(t, s); report.Total != 0 || len(report.Facets) != 0 {
		t.Fatal("removed history remained in profile")
	}
}

func TestCatalogTasteFeedbackNegativeWeightsAndMissingMetadata(t *testing.T) {
	s, _, works := personalFixture(t)
	for i, feedback := range []string{"like", "dislike"} {
		catalogJSON[catalog.Work](t, s, "PATCH", "/api/catalog/works/"+works[i].ID+"/personal", map[string]any{"feedback": feedback, "notes_revision": 0}, 200)
	}
	tasteConfig(t, s, true, "feedback")
	if report := tasteReport(t, s); report.Total != 0 {
		t.Fatalf("recommended explicit dislike: %+v", report)
	}
	if _, err := s.db.db.Exec("UPDATE catalog_works SET actors='[]',genres='[]',studio='',year=0"); err != nil {
		t.Fatal(err)
	}
	if report := tasteReport(t, s); len(report.Facets) != 0 || report.Total != 0 {
		t.Fatal("invented missing metadata")
	}
}

func TestCatalogActorFollowUsesCanonicalIdentityAndIndependentCAS(t *testing.T) {
	s, _, works := personalFixture(t)
	work := catalogJSON[catalog.Work](t, s, "GET", "/api/catalog/works/"+works[0].ID, nil, 200)
	actor := work.ActorProfiles[0]
	path := "/api/catalog/actors/" + actor.ID + "/follow"
	type follow struct {
		Following bool  `json:"following"`
		Version   int64 `json:"version"`
	}
	state := catalogJSON[follow](t, s, "PUT", path, map[string]any{"following": true, "version": 0}, 200)
	if !state.Following || state.Version != 1 {
		t.Fatalf("follow: %+v", state)
	}
	if w := catalogRequest(t, s, "PUT", path, map[string]any{"following": false, "version": 0}); w.Code != 409 {
		t.Fatalf("stale follow: %d", w.Code)
	}
	a := catalogJSON[map[string]any](t, s, "GET", "/api/catalog/actors/"+actor.ID, nil, 200)
	if a["following"] != true || a["follow_version"] != float64(1) {
		t.Fatal("actor did not expose follow state")
	}
	catalogJSON[catalog.Actor](t, s, "PATCH", "/api/catalog/actors/"+actor.ID, map[string]any{"display_name": "Renamed actor", "biography": "", "aliases": []string{"Canonical Alias"}, "version": actor.Version}, 200)
	list := catalogJSON[catalog.ActorPage](t, s, "GET", "/api/catalog/actors?following=true", nil, 200)
	if list.Total != 1 || list.Items[0].ID != actor.ID {
		t.Fatal("follow lost after alias edit")
	}
	tasteConfig(t, s, true, "follows")
	report := tasteReport(t, s)
	if len(report.Facets) != 1 || report.Facets[0].Key != actor.ID || report.Facets[0].Label != "Renamed actor" {
		t.Fatalf("follow preference uses mutable name: %+v", report)
	}
	state = catalogJSON[follow](t, s, "PUT", path, map[string]any{"following": false, "version": state.Version}, 200)
	if state.Following || len(tasteReport(t, s).Facets) != 0 {
		t.Fatal("unfollow still contributes")
	}
	if w := catalogRequest(t, s, "PUT", "/api/catalog/actors/missing/follow", map[string]any{"following": true, "version": 0}); w.Code != 404 {
		t.Fatalf("missing follow: %d", w.Code)
	}
}

func TestCatalogTasteValidationSecurityAndAtomicConfig(t *testing.T) {
	s, _, _ := personalFixture(t)
	config := catalogJSON[tasteTestConfig](t, s, "GET", "/api/catalog/taste/config", nil, 200)
	config.Enabled = true
	catalogJSON[tasteTestConfig](t, s, "PUT", "/api/catalog/taste/config", config, 200)
	if w := catalogRequest(t, s, "PUT", "/api/catalog/taste/config", config); w.Code != 409 {
		t.Fatalf("stale config: %d", w.Code)
	}
	for _, payload := range []string{`{}`, `{"enabled":true,"version":2}`, `{"enabled":true,"version":2,"sources":{"unknown":true}}`, `{"enabled":true,"version":2,"sources":null}`, `{"enabled":true,"version":2,"sources":{"collection":true,"history":true,"feedback":true,"follows":true},"extra":1}`} {
		r := httptest.NewRequest("PUT", "/api/catalog/taste/config", strings.NewReader(payload))
		r.Header.Set("X-Trim-Isadmin", "true")
		r.Header.Set("X-Catalog-CSRF", s.catalogCSRF)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("accepted invalid config %s: %d", payload, w.Code)
		}
	}
	for _, path := range []string{"/api/catalog/taste?limit=0", "/api/catalog/taste?limit=101", "/api/catalog/taste?offset=-1", "/api/catalog/actors?following=perhaps"} {
		if w := catalogRequest(t, s, "GET", path, nil); w.Code != 400 {
			t.Fatalf("invalid query: %s %d", path, w.Code)
		}
	}
	for _, method := range []string{"GET", "PUT"} {
		path := "/api/catalog/taste"
		if method == "PUT" {
			path += "/config"
		}
		r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("nonadmin: %d", w.Code)
		}
	}
	r := httptest.NewRequest("PUT", "/api/catalog/taste/config", strings.NewReader(`{}`))
	r.Header.Set("X-Trim-Isadmin", "true")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("missing CSRF: %d", w.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.db.catalog.PersonalStats(ctx); err == nil {
		t.Fatal("canceled database read succeeded")
	}
}
