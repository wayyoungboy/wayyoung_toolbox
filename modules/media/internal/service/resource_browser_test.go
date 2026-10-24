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
	"strconv"
	"testing"
	"time"
)

// Dedicated neutral, local-only provider/HTTP/SQLite journey. No real sites,
// credentials, NAS files, download submissions or production task runners.
func TestResourceBrowserJourney(t *testing.T) {
	s, mode, calls := resourceServiceFixture(t)
	public, err := filepath.Abs("../../frontend/dist")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(public, "index.html")); err != nil {
		t.Fatal("build media frontend first", err)
	}
	s.publicDir = public
	root := t.TempDir()
	catalogWrite(t, root, "TEST-001.mp4", "neutral non-playback sample")
	catalogWrite(t, root, "TEST-001.nfo", "<movie><num>TEST-001</num><title>中性测试作品</title></movie>")
	// The preserved RSS screen also reads its completed-library display cache.
	// Give that existing tool valid neutral paths rather than ignoring a known
	// missing-configuration 409 in browser console checks. Do not scan the path.
	cfg, err := s.db.JavspConfigPrivate()
	if err != nil {
		t.Fatal(err)
	}
	cfg.InputDirectory = root
	cfg.OutputDirectory = t.TempDir()
	if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	library := catalogLibrary(t, s, root)
	catalogScan(t, s, library)
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__fixture/resources" {
			if r.Method == "POST" {
				value, err := strconv.Atoi(r.URL.Query().Get("mode"))
				if err != nil || value < 0 || value > 3 {
					w.WriteHeader(400)
					return
				}
				mode.Store(int32(value))
			}
			busy := !s.resourceSearchMu.TryLock()
			if !busy {
				s.resourceSearchMu.Unlock()
			}
			writeJSON(w, 200, map[string]any{"calls": calls.Load(), "busy": busy})
			return
		}
		r.Header.Set("X-Trim-Isadmin", "true")
		r.Host = "localhost"
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	node := os.Getenv("CATALOG_NODE")
	if node == "" {
		node = "node"
	}
	command := exec.CommandContext(ctx, node, "../../scripts/resource-browser.mjs", server.URL+s.prefix+"/")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("resource browser: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
	for _, table := range []string{"media_jobs", "task_runs", "javsp_runs", "catalog_scan_jobs"} {
		var n int
		if err := s.db.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("search created tasks: %s %d %v", table, n, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "TEST-001.mp4")); err != nil || string(data) != "neutral non-playback sample" {
		t.Fatal("search altered media", err)
	}
	fmt.Println("RESOURCE_FIXTURE_PRESERVED=1")
}
