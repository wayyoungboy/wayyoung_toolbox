package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestFetchEntriesCapsFeedSize(t *testing.T) {
	var items strings.Builder
	for index := 0; index < maxFeedEntries+10; index++ {
		fmt.Fprintf(&items, "<item><title>item-%d</title><link>https://example.com/%d</link></item>", index, index)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "<rss version=\"2.0\"><channel><title>test</title>%s</channel></rss>", items.String())
	}))
	defer server.Close()
	entries, err := (Collector{}).FetchEntries(context.Background(), Task{FeedURL: server.URL}, defaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != maxFeedEntries {
		t.Fatalf("got %d", len(entries))
	}
}

func TestFetchEntriesBypassesConfiguredProxy(t *testing.T) {
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `<rss version="2.0"><channel><title>test</title>`+
			`<item><title>item</title><link>https://example.com/item</link></item>`+
			`</channel></rss>`)
	}))
	defer feed.Close()

	tests := []struct {
		name          string
		taskProxy     bool
		settingsProxy bool
	}{
		{name: "global proxy", settingsProxy: true},
		{name: "task proxy", taskProxy: true},
		{name: "task and global proxies", taskProxy: true, settingsProxy: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			proxyRequests := 0
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				proxyRequests++
				http.Error(w, "feed request must not use the resolver proxy", http.StatusBadGateway)
			}))
			defer proxy.Close()

			task := Task{FeedURL: feed.URL}
			settings := defaultSettings()
			if test.taskProxy {
				task.ProxyURL = proxy.URL
			}
			if test.settingsProxy {
				settings.GlobalProxyURL = proxy.URL
			}

			entries, err := (Collector{}).FetchEntries(context.Background(), task, settings)
			if err != nil {
				t.Fatalf("FetchEntries() error = %v", err)
			}
			if len(entries) != 1 {
				t.Fatalf("FetchEntries() returned %d entries, want 1", len(entries))
			}
			if proxyRequests != 0 {
				t.Fatalf("resolver proxy received %d feed requests, want 0", proxyRequests)
			}
		})
	}
}

func TestFetchEntriesUsesConfiguredProxyWhenEnabled(t *testing.T) {
	proxyRequests := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyRequests++
		_, _ = fmt.Fprint(w, `<rss version="2.0"><channel><title>test</title>`+
			`<item><title>item</title><link>https://example.com/item</link></item>`+
			`</channel></rss>`)
	}))
	defer proxy.Close()

	settings := defaultSettings()
	settings.FeedUseProxy = true
	settings.GlobalProxyURL = proxy.URL
	entries, err := (Collector{}).FetchEntries(context.Background(), Task{
		FeedURL: "http://rsshub.invalid/feed",
	}, settings)
	if err != nil {
		t.Fatalf("FetchEntries() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("FetchEntries() returned %d entries, want 1", len(entries))
	}
	if proxyRequests != 1 {
		t.Fatalf("resolver proxy received %d feed requests, want 1", proxyRequests)
	}
}

func TestFetchEntriesRetriesTemporaryResponse(t *testing.T) {
	attempts := 0
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "try again", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, `<rss version="2.0"><channel><title>test</title>`+
			`<item><title>item</title><link>https://example.com/item</link></item>`+
			`</channel></rss>`)
	}))
	defer feed.Close()

	entries, err := (Collector{}).FetchEntries(context.Background(), Task{FeedURL: feed.URL}, defaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || len(entries) != 1 {
		t.Fatalf("attempts=%d entries=%d", attempts, len(entries))
	}
}

func TestResolveMagnetRetriesRateLimit(t *testing.T) {
	detailAttempts := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/link.php":
			detailAttempts++
			if detailAttempts == 1 {
				w.Header().Set("Retry-After", "0")
				http.Error(w, "slow down", http.StatusTooManyRequests)
				return
			}
			_, _ = fmt.Fprint(w, `<input NAME="reff" value="token">`)
		case "/download.php":
			_, _ = fmt.Fprint(w, `magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()

	magnet, err := (Collector{}).ResolveMagnet(context.Background(), FeedEntry{
		Summary: fmt.Sprintf(`<a href="%s/link.php?hash=0123456789abcdef0123456789abcdef01234567">download</a>`, remote.URL),
	}, Task{Extractor: "rmdown"}, defaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	if detailAttempts != 2 || !strings.Contains(magnet, "urn:btih:") {
		t.Fatalf("detail attempts=%d magnet=%q", detailAttempts, magnet)
	}
}

func TestQBittorrentBypassesConfiguredAndDefaultTransportProxies(t *testing.T) {
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			_, _ = fmt.Fprint(w, "Ok.")
		case "/api/v2/app/version":
			_, _ = fmt.Fprint(w, "5.1.0")
		default:
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()

	proxyRequests := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyRequests++
		http.Error(w, "qBittorrent request must not use a proxy", http.StatusBadGateway)
	}))
	defer proxy.Close()

	originalTransport := http.DefaultTransport
	proxiedTransport := originalTransport.(*http.Transport).Clone()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxiedTransport.Proxy = http.ProxyURL(proxyURL)
	http.DefaultTransport = proxiedTransport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	settings := defaultSettings()
	settings.QBHost = qb.URL
	settings.QBPort = 0
	settings.GlobalProxyURL = proxy.URL
	result, err := (Collector{}).TestQBittorrent(context.Background(), settings)
	if err != nil {
		t.Fatalf("TestQBittorrent() error = %v", err)
	}
	if result["version"] != "5.1.0" {
		t.Fatalf("TestQBittorrent() version = %v, want 5.1.0", result["version"])
	}
	if proxyRequests != 0 {
		t.Fatalf("proxy received %d qBittorrent requests, want 0", proxyRequests)
	}
}

func TestQBittorrentTorrentActionsUseLegacyFallbackOnlyForMissingCurrentEndpoint(t *testing.T) {
	tests := []struct {
		name          string
		currentPath   string
		legacyPath    string
		currentStatus int
		action        func(context.Context, *QBSession) error
		wantErr       bool
	}{
		{
			name:          "stop falls back to pause on 404",
			currentPath:   "/api/v2/torrents/stop",
			legacyPath:    "/api/v2/torrents/pause",
			currentStatus: http.StatusNotFound,
			action:        func(ctx context.Context, session *QBSession) error { return session.StopTorrent(ctx, "hash") },
		},
		{
			name:          "start falls back to resume on 404",
			currentPath:   "/api/v2/torrents/start",
			legacyPath:    "/api/v2/torrents/resume",
			currentStatus: http.StatusNotFound,
			action:        func(ctx context.Context, session *QBSession) error { return session.StartTorrent(ctx, "hash") },
		},
		{
			name:          "server error does not fall back",
			currentPath:   "/api/v2/torrents/stop",
			legacyPath:    "/api/v2/torrents/pause",
			currentStatus: http.StatusInternalServerError,
			action:        func(ctx context.Context, session *QBSession) error { return session.StopTorrent(ctx, "hash") },
			wantErr:       true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths := []string{}
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil || r.Form.Get("hashes") != "hash" {
					t.Errorf("form=%v err=%v", r.Form, err)
				}
				paths = append(paths, r.URL.Path)
				if r.URL.Path == test.currentPath {
					w.WriteHeader(test.currentStatus)
					return
				}
				if r.URL.Path != test.legacyPath {
					http.NotFound(w, r)
				}
			}))
			defer remote.Close()
			base, err := url.Parse(remote.URL)
			if err != nil {
				t.Fatal(err)
			}
			session := &QBSession{client: remote.Client(), base: base}
			err = test.action(context.Background(), session)
			if test.wantErr && err == nil {
				t.Fatal("expected current endpoint error")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("action error: %v", err)
			}
			wantPaths := []string{test.currentPath, test.legacyPath}
			if test.wantErr {
				wantPaths = wantPaths[:1]
			}
			if strings.Join(paths, ",") != strings.Join(wantPaths, ",") {
				t.Fatalf("paths=%v want=%v", paths, wantPaths)
			}
		})
	}
}

func TestMagnetLengthIsBounded(t *testing.T) {
	if got := firstMagnet("magnet:?" + strings.Repeat("x", 20<<10)); got != "" {
		t.Fatalf("unexpected oversized magnet")
	}
}

func TestPreviewReturnsAtMostTenEntries(t *testing.T) {
	var items strings.Builder
	for index := 0; index < 20; index++ {
		fmt.Fprintf(&items, "<item><title>item-%d</title><link>https://example.com/%d</link></item>", index, index)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "<rss version=\"2.0\"><channel><title>test</title>%s</channel></rss>", items.String())
	}))
	defer server.Close()
	result, err := (Collector{}).Preview(context.Background(), Task{
		Name: "test", FeedURL: server.URL, IncludeAll: []string{"never-match"},
	}, defaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	entries, ok := result["entries"].([]FeedEntry)
	if !ok || len(entries) != 10 {
		t.Fatalf("unexpected preview entries: %#v", result["entries"])
	}
}
