package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResourceDownloadSelectionLookupIsReadOnlyAndIncludesExistingRSSOwner(t *testing.T) {
	s, input := resourceDownloadFixture(t)
	path := "/api/resources/downloads/selection?code=" + input.Code + "&provider=" + input.Provider + "&infohash=" + input.InfoHash
	if got := resourceResponse(t, s, "GET", path, nil, 200); got["job"] != nil {
		t.Fatal(got)
	}
	job, _, err := s.db.EnqueueResourceDownload(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.db.Exec("DELETE FROM resource_search_cache"); err != nil {
		t.Fatal(err)
	}
	got := resourceResponse(t, s, "GET", path, nil, 200)
	if got["job"].(map[string]any)["id"] != float64(job.ID) {
		t.Fatal(got)
	}
	var count int
	if err := s.db.db.QueryRow("SELECT COUNT(*) FROM media_jobs").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	for _, suffix := range []string{"&extra=1", "&code=OTHER-001"} {
		resourceResponse(t, s, "GET", path+suffix, nil, 400)
	}

	other, selection := resourceDownloadFixture(t)
	rss, err := other.db.EnqueueRSSAcquisition(Task{ID: 42}, FeedEntry{EntryID: "one", Title: "TEST-001"})
	if err != nil {
		t.Fatal(err)
	}
	rss, err = other.db.ClaimMediaJob(rss.ID, rss.Version)
	if err != nil {
		t.Fatal(err)
	}
	rss, err = other.db.ReserveRSSDownload(rss.ID, rss.Version, "magnet:?xt=urn:btih:"+selection.InfoHash)
	if err != nil {
		t.Fatal(err)
	}
	got = resourceResponse(t, other, "GET", path, nil, 200)
	if got["job"].(map[string]any)["id"] != float64(rss.ID) || got["job"].(map[string]any)["rss_id"] != float64(42) {
		t.Fatal(got)
	}
}

func resourceDownloadBody(input ResourceDownloadSelection) map[string]any {
	return map[string]any{"code": input.Code, "provider": input.Provider, "infohash": input.InfoHash, "searched_at": input.SearchedAt, "confirmation": "下载资源"}
}

func TestResourceDownloadHTTPQueuesOnceAndGETIsReadOnly(t *testing.T) {
	s, input := resourceDownloadFixture(t)
	result := resourceResponse(t, s, "POST", "/api/resources/downloads", resourceDownloadBody(input), 202)
	if result["created"] != true {
		t.Fatal(result)
	}
	job := result["job"].(map[string]any)
	if job["phase"] != "discovered" || job["resource"] == nil || job["discovery"] != nil {
		t.Fatal(job)
	}
	if result = resourceResponse(t, s, "POST", "/api/resources/downloads", resourceDownloadBody(input), 200); result["created"] != false || result["job"].(map[string]any)["id"] != job["id"] {
		t.Fatal(result)
	}
	result = resourceResponse(t, s, "GET", "/api/resources/downloads?limit=20", nil, 200)
	if len(result["items"].([]any)) != 1 {
		t.Fatal(result)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "qb.example.test") {
		t.Fatal("credentials exposed")
	}
}

func TestResourceDownloadHTTPRequiresAdminCSRFAndExplicitConfirmation(t *testing.T) {
	s, input := resourceDownloadFixture(t)
	s.localDev = false
	for _, admin := range []bool{false, true} {
		r := httptest.NewRequest("POST", "/api/resources/downloads", strings.NewReader(`{"confirmation":"下载资源"}`))
		r.Header.Set("Content-Type", "application/json")
		if admin {
			r.Header.Set("X-Trim-Isadmin", "true")
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("unauthorized request: %d", w.Code)
		}
	}
	body := resourceDownloadBody(input)
	body["confirmation"] = ""
	resourceResponse(t, s, "POST", "/api/resources/downloads", body, 400)
	for _, query := range []string{"?limit=0", "?limit=101", "?limit=x", "?limit=1&limit=2", "?other=x"} {
		resourceResponse(t, s, "GET", "/api/resources/downloads"+query, nil, 400)
	}
	var count int
	if err := s.db.db.QueryRow("SELECT COUNT(*) FROM media_jobs").Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid request enqueued: %d %v", count, err)
	}
}
