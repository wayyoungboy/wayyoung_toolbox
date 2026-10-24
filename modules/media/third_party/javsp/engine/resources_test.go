package engine

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yuukiy/javsp-go/internal/config"
	"gopkg.in/yaml.v3"
)

const resourceHash = "0123456789abcdef0123456789abcdef01234567"

func resourceOptions(t *testing.T, base string) ResourceSearchOptions {
	t.Helper()
	cfg, err := config.Load(filepath.Join("..", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Network.ProxyServer = nil
	cfg.Network.ProxyFree = map[string]string{"javdb": base}
	cfg.Crawler.Selection.Normal = []string{"javdb"}
	cfg.Crawler.Selection.FC2 = []string{"javdb"}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return ResourceSearchOptions{Options: Options{ConfigData: raw, ResourceRoot: t.TempDir()}, Code: "TEST-001", Provider: "javdb"}
}

func resourceDetail(code, links string) string {
	return `<html><body><section><div><div class="video-detail"><h2><strong class="current-title">` + code + ` Sample title</strong></h2><nav class="panel movie-panel-info"><div><strong>番號:</strong><span>` + code + `</span></div></nav>` + links + `</div></div></section></body></html>`
}

func resourceAnchor(magnet string) string {
	return `<div class="magnet-name column is-four-fifths"><a href="` + strings.ReplaceAll(magnet, "&", "&amp;") + `">download</a></div>`
}

func TestResourceSearchReadOnlyExactIdentityAndPrivateTrackerRemoval(t *testing.T) {
	var calls atomic.Int32
	decoded, _ := hex.DecodeString(resourceHash)
	b32 := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(decoded)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.Header.Get("Accept-Language") == "" || len(r.Cookies()) != 0 {
			t.Errorf("unsafe request: %v", r)
		}
		if r.URL.Path == "/search" {
			if r.URL.Query().Get("q") != "TEST-001" {
				t.Error("wrong query")
			}
			fmt.Fprint(w, `<a class="box" href="/v/test?passkey=secret"><div class="video-title"><strong>TEST-001</strong></div></a>`)
			return
		}
		links := resourceAnchor("magnet:?xt=urn:btih:"+strings.ToUpper(resourceHash)+"&dn=Sample%20release&tr=https://tracker.invalid/private?passkey=secret") +
			resourceAnchor("magnet:?xt=urn:btih:"+b32) + resourceAnchor("https://invalid.example/file.torrent") +
			resourceAnchor("magnet:?xt=urn:btih:bad") + resourceAnchor("magnet:?xt=urn:btih:"+resourceHash+"&xt=urn:btih:"+strings.Repeat("f", 40))
		fmt.Fprint(w, resourceDetail("TEST-001", links))
	}))
	defer server.Close()
	options := resourceOptions(t, server.URL)
	got, err := SearchResources(context.Background(), options)
	if err != nil || got.State != "ready" || got.Code != "TEST-001" || got.Provider != "javdb" || len(got.Items) != 1 {
		t.Fatalf("search = %+v, %v", got, err)
	}
	item := got.Items[0]
	if item.InfoHash != resourceHash || item.Magnet != "magnet:?xt=urn:btih:"+resourceHash || item.Name != "Sample release" || item.Verification != "unverified" || item.Source != "javdb" || item.SourceURL != server.URL+"/v/test" {
		t.Fatalf("candidate = %+v", item)
	}
	if calls.Load() != 2 {
		t.Fatalf("unexpected requests: %d", calls.Load())
	}
	entries, err := os.ReadDir(options.ResourceRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("search touched media/runtime: %v %v", entries, err)
	}
}

func TestResourceSearchDistinguishesEmptyRestrictedBlockedAndMismatch(t *testing.T) {
	for _, mode := range []string{"empty", "login", "blocked", "mismatch", "identity_missing", "not_found", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "blocked" {
					http.Error(w, "Just a moment", 403)
					return
				}
				if r.URL.Path == "/search" {
					if mode == "not_found" {
						fmt.Fprint(w, "<html>none</html>")
						return
					}
					fmt.Fprint(w, `<a class="box" href="/v/test"><div class="video-title"><strong>TEST-001</strong></div></a>`)
					return
				}
				if mode == "login" && r.URL.Path != "/login" {
					http.Redirect(w, r, "/login", 302)
					return
				}
				if mode == "malformed" || mode == "login" {
					fmt.Fprint(w, "<html>no details</html>")
					return
				}
				code := "TEST-001"
				if mode == "mismatch" {
					code = "OTHER-001"
				}
				if mode == "identity_missing" {
					code = ""
				}
				fmt.Fprint(w, resourceDetail(code, ""))
			}))
			defer server.Close()
			got, err := SearchResources(context.Background(), resourceOptions(t, server.URL))
			want := map[string]string{"empty": "empty", "login": "restricted", "blocked": "blocked", "mismatch": "identity_mismatch", "identity_missing": "identity_mismatch", "not_found": "not_found", "malformed": "unavailable"}[mode]
			if err != nil || got.State != want || len(got.Items) != 0 {
				t.Fatalf("got %+v %v, want %s", got, err, want)
			}
		})
	}
}

func TestResourceSearchRejectsInvalidOrDisabledBeforeNetworking(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	for _, code := range []string{"", "-646", "TEST-001 extra", "https://localhost/TEST-001", "TEST-001-C", strings.Repeat("A", 129)} {
		opts := resourceOptions(t, server.URL)
		opts.Code = code
		if _, err := SearchResources(context.Background(), opts); !errors.Is(err, ErrResourceInput) {
			t.Fatalf("invalid code accepted: %q %v", code, err)
		}
	}
	options := resourceOptions(t, server.URL)
	options.Provider = "arbitrary"
	if _, err := SearchResources(context.Background(), options); !errors.Is(err, ErrResourceInput) {
		t.Fatal("arbitrary provider accepted", err)
	}
	options = resourceOptions(t, server.URL)
	var cfg config.Config
	_ = yaml.Unmarshal(options.ConfigData, &cfg)
	cfg.Crawler.Selection.Normal = []string{"javbus"}
	options.ConfigData, _ = yaml.Marshal(cfg)
	got, err := SearchResources(context.Background(), options)
	if err != nil || got.State != "disabled" {
		t.Fatalf("disabled source: %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SearchResources(ctx, resourceOptions(t, server.URL)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
	if calls.Load() != 0 {
		t.Fatal("invalid query made external requests")
	}
}

func TestResourceSearchBlocksOffOriginRequestsAndOversizedBodies(t *testing.T) {
	for _, mode := range []string{"redirect", "detail_link", "too_large", "too_many"} {
		t.Run(mode, func(t *testing.T) {
			var outside atomic.Int32
			external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { outside.Add(1) }))
			defer external.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "redirect" {
					http.Redirect(w, r, external.URL, 302)
					return
				}
				if mode == "too_large" {
					fmt.Fprint(w, strings.Repeat("x", (2<<20)+1))
					return
				}
				if r.URL.Path == "/search" {
					link := "/v/test"
					if mode == "detail_link" {
						link = external.URL
					}
					fmt.Fprintf(w, `<a class="box" href="%s"><div class="video-title"><strong>TEST-001</strong></div></a>`, link)
					return
				}
				links := ""
				for i := 0; i < 80; i++ {
					links += resourceAnchor(fmt.Sprintf("magnet:?xt=urn:btih:%040x&dn=%s", i+1, url.QueryEscape("release")))
				}
				fmt.Fprint(w, resourceDetail("TEST-001", links))
			}))
			defer server.Close()
			got, err := SearchResources(context.Background(), resourceOptions(t, server.URL))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "too_many" {
				if got.State != "ready" || len(got.Items) != 50 {
					t.Fatalf("unbounded candidates: %+v", got)
				}
			} else if got.State != "unavailable" {
				t.Fatalf("unsafe response accepted: %+v", got)
			}
			if outside.Load() != 0 {
				t.Fatal("off-origin request reached server")
			}
		})
	}
}
