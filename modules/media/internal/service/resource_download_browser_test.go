//go:build catalog_browser

package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Browser → real local HTTP/SQLite → synthetic qB only. No schedulers, media
// scans, production credentials or live download destinations are involved.
func TestResourceDownloadBrowserJourney(t *testing.T) {
	f := newResourceExecutionFixture(t)
	for _, table := range []string{"media_job_events", "media_jobs"} {
		if _, err := f.s.db.db.Exec("DELETE FROM " + table); err != nil {
			t.Fatal(err)
		}
	}
	public, err := filepath.Abs("../../frontend/dist")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(public, "index.html")); err != nil {
		t.Fatal("build media frontend first", err)
	}
	f.s.publicDir = public
	handler := f.s.Handler()
	stop := make(chan struct{})
	var stopOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("CATALOG_REMOTE_FIXTURE") == "1" && r.Method == "POST" && r.URL.Path == "/__fixture/stop" {
			stopOnce.Do(func() { close(stop) })
			w.WriteHeader(200)
			return
		}
		if r.URL.Path == "/__fixture/downloads" {
			if r.Method == "POST" {
				switch r.URL.Query().Get("action") {
				case "drain":
					if err := f.s.drainPendingRSS(r.Context()); err != nil {
						http.Error(w, err.Error(), 500)
						return
					}
				case "stale":
					f.lookupFails.Store(true)
				default:
					w.WriteHeader(400)
					return
				}
				pollErr := f.s.refreshQueuedDownloads(r.Context())
				if (pollErr != nil) != (r.URL.Query().Get("action") == "stale") {
					http.Error(w, "unexpected synthetic observation result", 500)
					return
				}
			}
			var jobs int
			if err := f.s.db.db.QueryRow("SELECT COUNT(*) FROM media_jobs").Scan(&jobs); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			writeJSON(w, 200, map[string]any{"adds": f.adds.Load(), "jobs": jobs, "feeds": f.feeds.Load()})
			return
		}
		r.Header.Set("X-Trim-Isadmin", "true")
		r.Host = "localhost"
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if os.Getenv("CATALOG_REMOTE_FIXTURE") == "1" {
		fmt.Printf("RESOURCE_DOWNLOAD_FIXTURE_URL=%s%s/\n", server.URL, f.s.prefix)
		select {
		case <-stop:
		case <-ctx.Done():
			t.Fatal("resource download fixture deadline reached")
		}
	} else {
		node := os.Getenv("CATALOG_NODE")
		if node == "" {
			node = "node"
		}
		command := exec.CommandContext(ctx, node, "../../scripts/resource-download-browser.mjs", server.URL+f.s.prefix+"/")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("resource download browser: %v\n%s", err, output)
		} else {
			t.Log(string(output))
		}
	}
	if f.adds.Load() != 1 || f.feeds.Load() != 0 {
		t.Fatalf("unexpected synthetic network mutations: adds=%d feeds=%d", f.adds.Load(), f.feeds.Load())
	}
	for _, table := range []string{"task_runs", "javsp_runs", "catalog_scan_jobs"} {
		var n int
		if err := f.s.db.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("unexpected work: %s %d %v", table, n, err)
		}
	}
}
