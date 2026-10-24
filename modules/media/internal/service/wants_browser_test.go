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

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

// Real HTTP/SQLite and neutral temporary files. This fixture cannot reach a
// production downloader, library, crawler, translator, or scheduler.
func TestWantsBrowserJourney(t *testing.T) {
	f := newResourceExecutionFixture(t)
	for _, table := range []string{"media_job_events", "media_jobs"} {
		if _, err := f.s.db.db.Exec("DELETE FROM " + table); err != nil {
			t.Fatal(err)
		}
	}
	root := registrationRoot(t)
	catalogLibrary(t, f.s, root)
	public, err := filepath.Abs("../../frontend/dist")
	if err != nil {
		t.Fatal(err)
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
		if r.URL.Path == "/__fixture/wants" {
			if r.Method == "POST" {
				switch r.URL.Query().Get("action") {
				case "seed_pages":
					for i := 1000; i < 1049; i++ {
						if _, _, err := f.s.db.catalog.AddWant(r.Context(), catalog.WantInput{Code: fmt.Sprintf("PAGE-%d", i)}); err != nil {
							http.Error(w, err.Error(), 500)
							return
						}
					}
				case "publish":
					catalogWrite(t, root, "TEST-001.mp4", "neutral video fixture")
					if _, err := f.s.db.catalog.RegisterFiles(r.Context(), []catalog.RegistrationTarget{registrationTarget(t, filepath.Join(root, "TEST-001.mp4"))}, nil); err != nil {
						http.Error(w, err.Error(), 500)
						return
					}
				case "conflict":
					page, err := f.s.db.catalog.Wants(r.Context(), catalog.WantFilter{Query: "TEST-002", Limit: 48})
					if err != nil || len(page.Items) != 1 {
						http.Error(w, "fixture want missing", 500)
						return
					}
					title := "Externally updated neutral title"
					if _, err := f.s.db.catalog.PatchWant(r.Context(), page.Items[0].ID, catalog.WantPatch{Version: page.Items[0].Version, Title: &title}); err != nil {
						http.Error(w, err.Error(), 500)
						return
					}
				default:
					w.WriteHeader(400)
					return
				}
			}
			counts := map[string]int64{"adds": int64(f.adds.Load()), "feeds": int64(f.feeds.Load())}
			for key, table := range map[string]string{"wants": "catalog_wants", "jobs": "media_jobs", "assets": "catalog_assets"} {
				var n int64
				if err := f.s.db.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				counts[key] = n
			}
			writeJSON(w, 200, counts)
			return
		}
		r.Header.Set("X-Trim-Isadmin", "true")
		r.Host = "localhost"
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if os.Getenv("CATALOG_REMOTE_FIXTURE") == "1" {
		fmt.Printf("WANTS_FIXTURE_URL=%s%s/\n", server.URL, f.s.prefix)
		select {
		case <-stop:
		case <-ctx.Done():
			t.Fatal("wants fixture deadline reached")
		}
	} else {
		node := os.Getenv("CATALOG_NODE")
		if node == "" {
			node = "node"
		}
		command := exec.CommandContext(ctx, node, "../../scripts/wants-browser.mjs", server.URL+f.s.prefix+"/")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("wants browser: %v\n%s", err, output)
		} else {
			t.Log(string(output))
		}
	}
	if f.adds.Load() != 0 || f.feeds.Load() != 0 {
		t.Fatal("opening wants unexpectedly dispatched network work")
	}
	for _, table := range []string{"task_runs", "javsp_runs", "catalog_scan_jobs"} {
		var n int
		if err := f.s.db.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("unexpected work: %s %d %v", table, n, err)
		}
	}
	var jobs, assets int
	if err := f.s.db.db.QueryRow("SELECT COUNT(*) FROM media_jobs").Scan(&jobs); err != nil || jobs != 1 {
		t.Fatal(fmt.Sprint("want removal touched producer: ", jobs, " ", err))
	}
	if err := f.s.db.db.QueryRow("SELECT COUNT(*) FROM catalog_assets WHERE available=1").Scan(&assets); err != nil || assets != 1 {
		t.Fatal("want removal touched neutral asset", assets, err)
	}
}
