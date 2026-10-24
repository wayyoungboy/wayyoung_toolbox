package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const resourceTestHash = "0123456789abcdef0123456789abcdef01234567"

func resourceServiceFixture(t *testing.T) (*Service, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	s := testService(t)
	var mode, calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if mode.Load() == 1 {
			http.Error(w, "Just a moment", 403)
			return
		}
		if r.URL.Path == "/search" {
			fmt.Fprintf(w, `<a class="box" href="/v/sample"><div class="video-title"><strong>%s</strong></div></a>`, r.URL.Query().Get("q"))
			return
		}
		links := ""
		if mode.Load() != 2 {
			links = `<div class="magnet-name column is-four-fifths"><a href="magnet:?xt=urn:btih:` + resourceTestHash + `&amp;dn=Example&amp;tr=https://tracker.invalid/passkey/secret">download</a></div>`
		}
		fmt.Fprint(w, `<html><body><section><div><div class="video-detail"><h2><strong class="current-title">TEST-001 Sample</strong></h2><nav class="panel movie-panel-info"><div><strong>番號:</strong><span>TEST-001</span></div></nav>`+links+`</div></div></section></body></html>`)
	}))
	t.Cleanup(server.Close)
	bundle, data := javspTestRuntime(t)
	s.javsp = NewJavspRunner(bundle, data)
	cfg := defaultJavspConfig()
	cfg.NormalCrawlers = []string{"javdb"}
	cfg.ProxyServer = ""
	cfg.ProxyFree = map[string]string{"javdb": server.URL}
	if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	return s, &mode, &calls
}

func resourceResponse(t *testing.T, s *Service, method, target string, body any, want int) map[string]any {
	t.Helper()
	w := catalogRequest(t, s, method, target, body)
	if w.Code != want {
		t.Fatalf("%s %s: %d %s", method, target, w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestResourceSearchAPICacheFailureAndNoAutomation(t *testing.T) {
	s, mode, calls := resourceServiceFixture(t)
	before, _ := s.db.JavspConfigPrivate()
	query := "/api/resources/search?code=TEST-001&provider=javdb"
	got := resourceResponse(t, s, "GET", query, nil, 200)
	if got["state"] != "not_searched" || calls.Load() != 0 {
		t.Fatal("cache read contacted source", got)
	}
	body := map[string]string{"code": "test-001", "provider": "javdb"}
	got = resourceResponse(t, s, "POST", "/api/resources/search", body, 200)
	if got["state"] != "ready" || got["cached"] != false || len(got["items"].([]any)) != 1 || got["searched_at"] == "" {
		t.Fatal(got)
	}
	successTime := got["searched_at"]
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "tracker.invalid") {
		t.Fatal("private tracker escaped")
	}
	count := calls.Load()
	got = resourceResponse(t, s, "GET", query, nil, 200)
	if calls.Load() != count || got["cached"] != true || got["state"] != "ready" {
		t.Fatal("GET was not read-only", got)
	}
	mode.Store(1)
	got = resourceResponse(t, s, "POST", "/api/resources/search", body, 200)
	if got["state"] != "blocked" || got["stale"] != true || got["cached"] != true || got["searched_at"] != successTime || len(got["items"].([]any)) != 1 {
		t.Fatal("failure destroyed or mislabeled cache", got)
	}
	got = resourceResponse(t, s, "GET", query, nil, 200)
	if got["state"] != "ready" || got["searched_at"] != successTime {
		t.Fatal("failure overwrote last success", got)
	}
	mode.Store(2)
	got = resourceResponse(t, s, "POST", "/api/resources/search", body, 200)
	if got["state"] != "empty" || len(got["items"].([]any)) != 0 || got["stale"] != false {
		t.Fatal("real empty result not distinguished", got)
	}
	for _, table := range []string{"media_jobs", "runs", "javsp_runs", "catalog_works", "catalog_scan_jobs"} {
		var n int
		if err := s.db.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("search mutated %s: %d %v", table, n, err)
		}
	}
	after, _ := s.db.JavspConfigPrivate()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("search changed configured sites or schedule")
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := OpenDatabase(filepath.Join(s.dataDir, "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.db = db
	got = resourceResponse(t, s, "GET", query, nil, 200)
	if got["state"] != "empty" || got["cached"] != true {
		t.Fatal("cache lost on restart", got)
	}
}

func TestResourceSearchAPIValidationAndAuthorizationBeforeHTTP(t *testing.T) {
	s, _, calls := resourceServiceFixture(t)
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, "/api/resources/search?code=TEST-001&provider=javdb", strings.NewReader(`{"code":"TEST-001","provider":"javdb"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code == 200 {
			t.Fatal("anonymous search accepted")
		}
	}
	for _, token := range []string{"", "wrong"} {
		r := httptest.NewRequest("POST", "/api/resources/search", strings.NewReader(`{"code":"TEST-001","provider":"javdb"}`))
		r.Header.Set("X-Trim-Isadmin", "true")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Catalog-CSRF", token)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("missing CSRF: %d", w.Code)
		}
	}
	for _, body := range []map[string]any{{"code": "-646", "provider": "javdb"}, {"code": "TEST-001 extra", "provider": "javdb"}, {"code": "TEST-001", "provider": "other"}, {"code": "TEST-001", "provider": "javdb", "url": "https://internal.invalid"}} {
		resourceResponse(t, s, "POST", "/api/resources/search", body, 400)
	}
	for _, query := range []string{"?code=-646&provider=javdb", "?code=TEST-001&provider=other", "?code=TEST-001&provider=javdb&url=x", "?code=TEST-001&code=OTHER-001&provider=javdb"} {
		resourceResponse(t, s, "GET", "/api/resources/search"+query, nil, 400)
	}
	if calls.Load() != 0 {
		t.Fatal("unauthorized/invalid request reached remote site")
	}
}

func TestResourceSearchAPIConcurrentBudgetAndCancellation(t *testing.T) {
	s, _, _ := resourceServiceFixture(t)
	started := make(chan struct{})
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(stopped) }))
	defer server.Close()
	cfg, _ := s.db.JavspConfigPrivate()
	cfg.ProxyFree = map[string]string{"javdb": server.URL}
	if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "/api/resources/search", bytes.NewBufferString(`{"code":"TEST-001","provider":"javdb"}`)).WithContext(ctx)
	r.Header.Set("X-Trim-Isadmin", "true")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Catalog-CSRF", s.catalogCSRF)
	done := make(chan struct{})
	go func() { s.Handler().ServeHTTP(httptest.NewRecorder(), r); close(done) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("search did not start")
	}
	resourceResponse(t, s, "POST", "/api/resources/search", map[string]string{"code": "TEST-001", "provider": "javdb"}, 409)
	cancel()
	for _, ch := range []chan struct{}{done, stopped} {
		select {
		case <-ch:
		case <-time.After(3 * time.Second):
			t.Fatal("request cancellation did not stop networking")
		}
	}
}
