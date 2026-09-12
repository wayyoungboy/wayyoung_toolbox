package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHealthAndAboutExposeVersionAndAccurateAttribution(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	server := newTestServer(t, root, root)

	health := requestJSON(t, server, http.MethodGet, "/app/music_converter/api/health", nil, false)
	if health.Code != http.StatusOK || health.Body["version"] != Version {
		t.Fatalf("health = %#v", health)
	}
	about := requestJSON(t, server, http.MethodGet, "/app/music_converter/api/about", nil, false)
	encoded, _ := json.Marshal(about.Body)
	text := string(encoded)
	for _, required := range []string{
		"git.taurusxin.com/taurusxin/ncmdump-go",
		"v1.7.5",
		"actual_dependency",
		"reference_only",
		"仅处理你合法拥有的本地文件",
		"不能替代上游许可证或适用法律",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("about response missing %q: %s", required, text)
		}
	}
}

func TestFrontendPrefixRedirectsToTrailingSlashForRelativeAssets(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	server := newTestServer(t, root, root)

	request := httptest.NewRequest(http.MethodGet, "/app/music_converter", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusPermanentRedirect {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusPermanentRedirect)
	}
	if location := response.Header().Get("Location"); location != "/app/music_converter/" {
		t.Fatalf("Location = %q", location)
	}
}

func TestSettingsRequireAdminPersistAndMaskNothingSensitive(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	source := t.TempDir()
	output := t.TempDir()
	server := newTestServer(t, dataDir, filepath.Dir(source))

	settings := Settings{
		SourceDir:      source,
		OutputDir:      output,
		Recursive:      true,
		PreserveTree:   true,
		FetchRemoteArt: false,
		Conflict:       "rename",
		Concurrency:    3,
		RetainRuns:     25,
		MigrateMP3:     true,
	}
	unauthorized := requestJSON(t, server, http.MethodPut, "/app/music_converter/api/settings", settings, false)
	if unauthorized.Code != http.StatusForbidden {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	saved := requestJSON(t, server, http.MethodPut, "/app/music_converter/api/settings", settings, true)
	if saved.Code != http.StatusOK || saved.Body["source_dir"] != source {
		t.Fatalf("saved settings = %#v", saved)
	}

	reloaded := newTestServer(t, dataDir, filepath.Dir(source))
	got := requestJSON(t, reloaded, http.MethodGet, "/app/music_converter/api/settings", nil, false)
	if got.Body["conflict"] != "rename" || got.Body["concurrency"].(float64) != 3 || got.Body["retain_runs"].(float64) != 25 || got.Body["migrate_mp3"] != true {
		t.Fatalf("settings were not persisted: %#v", got.Body)
	}
	encoded, _ := json.Marshal(got.Body)
	if strings.Contains(strings.ToLower(string(encoded)), "password") || strings.Contains(strings.ToLower(string(encoded)), "cookie") {
		t.Fatalf("settings unexpectedly expose credentials: %s", encoded)
	}
}

func TestScanDoesNotConvertAndJobCompletesInBackground(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	source := t.TempDir()
	output := t.TempDir()
	mustWrite(t, filepath.Join(source, "one.ncm"), "encrypted")
	mustWrite(t, filepath.Join(source, "ignore.txt"), "ignored")
	engine := &recordingEngine{}
	server := newTestServerWithEngine(t, dataDir, filepath.Dir(source), engine)
	settings := Settings{SourceDir: source, OutputDir: output, Recursive: true, PreserveTree: true, Conflict: "skip", Concurrency: 2, RetainRuns: 100}
	requestJSON(t, server, http.MethodPut, "/app/music_converter/api/settings", settings, true)

	scan := requestJSON(t, server, http.MethodPost, "/app/music_converter/api/scan", map[string]any{}, true)
	if scan.Code != http.StatusOK || scan.Body["total"].(float64) != 1 {
		t.Fatalf("scan = %#v", scan)
	}
	if engine.Calls() != 0 {
		t.Fatalf("scan converted %d files", engine.Calls())
	}

	started := requestJSON(t, server, http.MethodPost, "/app/music_converter/api/jobs", map[string]any{}, true)
	if started.Code != http.StatusAccepted {
		t.Fatalf("start = %#v", started)
	}
	job := waitForJob(t, server, "completed")
	if job.Body["completed"].(float64) != 1 || engine.Calls() != 1 {
		t.Fatalf("completed job = %#v, calls=%d", job, engine.Calls())
	}
	if _, err := os.Stat(filepath.Join(output, "one.mp3")); err != nil {
		t.Fatalf("converted output missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(source, "one.ncm")); err != nil {
		t.Fatalf("source file must remain: %v", err)
	}

	runs := requestJSON(t, server, http.MethodGet, "/app/music_converter/api/runs", nil, false)
	if runs.Code != http.StatusOK || len(runs.Body["runs"].([]any)) != 1 {
		t.Fatalf("runs = %#v", runs)
	}
}

func TestDirectoryBrowserCannotEscapeAuthorizedRoots(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	child := filepath.Join(root, "Music")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	server := newTestServer(t, t.TempDir(), root)
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonical root: %v", err)
	}
	canonicalChild, err := filepath.EvalSymlinks(child)
	if err != nil {
		t.Fatalf("canonical child: %v", err)
	}

	listing := requestJSON(t, server, http.MethodGet, "/app/music_converter/api/directories?path="+child, nil, false)
	if listing.Code != http.StatusOK || listing.Body["current"] != canonicalChild || listing.Body["parent"] != canonicalRoot {
		t.Fatalf("listing = %#v", listing)
	}
	escape := requestJSON(t, server, http.MethodGet, "/app/music_converter/api/directories?path="+filepath.Dir(root), nil, false)
	if escape.Code != http.StatusForbidden {
		t.Fatalf("escape status = %d, body=%#v", escape.Code, escape.Body)
	}
}

func TestMixedJobMigratesMP3AndOnlyConvertsNCM(t *testing.T) {
	t.Parallel()
	source, output := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(source, "source.ncm"), "encrypted")
	mustWrite(t, filepath.Join(source, "ready.mp3"), "ID3 original audio and tags")
	engine := &recordingEngine{}
	server := newTestServerWithEngine(t, t.TempDir(), filepath.Dir(source), engine)
	settings := Settings{SourceDir: source, OutputDir: output, Recursive: true, MigrateMP3: true, Conflict: "skip", Concurrency: 2, RetainRuns: 100}
	requestJSON(t, server, http.MethodPut, "/app/music_converter/api/settings", settings, true)
	scan := requestJSON(t, server, http.MethodPost, "/app/music_converter/api/scan", map[string]any{}, true)
	if scan.Code != http.StatusOK || scan.Body["total"].(float64) != 2 {
		t.Fatalf("scan = %#v", scan)
	}
	if _, err := os.Stat(filepath.Join(source, "ready.mp3")); err != nil {
		t.Fatal("scan moved MP3")
	}
	requestJSON(t, server, http.MethodPost, "/app/music_converter/api/jobs", map[string]any{}, true)
	job := waitForJob(t, server, "completed")
	if job.Body["completed"].(float64) != 2 || engine.Calls() != 1 {
		t.Fatalf("job = %#v, conversions = %d", job, engine.Calls())
	}
	if _, err := os.Stat(filepath.Join(source, "ready.mp3")); !os.IsNotExist(err) {
		t.Fatalf("MP3 was not moved: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(output, "ready.mp3"))
	if err != nil || string(body) != "ID3 original audio and tags" {
		t.Fatalf("MP3 changed: %q %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(source, "source.ncm")); err != nil {
		t.Fatal("NCM source was removed")
	}
}

func TestCancelRunningJobAndClearRunHistory(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	source := t.TempDir()
	output := t.TempDir()
	mustWrite(t, filepath.Join(source, "slow.ncm"), "encrypted")
	engine := &blockingEngine{started: make(chan struct{})}
	server := newTestServerWithEngine(t, dataDir, filepath.Dir(source), engine)
	settings := Settings{SourceDir: source, OutputDir: output, Recursive: true, PreserveTree: true, Conflict: "skip", Concurrency: 1, RetainRuns: 100}
	requestJSON(t, server, http.MethodPut, "/app/music_converter/api/settings", settings, true)
	requestJSON(t, server, http.MethodPost, "/app/music_converter/api/jobs", map[string]any{}, true)
	select {
	case <-engine.started:
	case <-time.After(time.Second):
		t.Fatal("conversion did not start")
	}
	cancel := requestJSON(t, server, http.MethodPost, "/app/music_converter/api/jobs/current/cancel", map[string]any{}, true)
	if cancel.Code != http.StatusAccepted {
		t.Fatalf("cancel = %#v", cancel)
	}
	job := waitForJob(t, server, "canceled")
	if job.Body["canceled"].(float64) != 1 {
		t.Fatalf("canceled job = %#v", job)
	}
	runs := requestJSON(t, server, http.MethodGet, "/app/music_converter/api/runs", nil, false)
	if len(runs.Body["runs"].([]any)) != 1 {
		t.Fatalf("runs before clear = %#v", runs)
	}
	cleared := requestJSON(t, server, http.MethodDelete, "/app/music_converter/api/runs", nil, true)
	if cleared.Code != http.StatusOK || cleared.Body["cleared"] != true {
		t.Fatalf("clear runs = %#v", cleared)
	}
	runs = requestJSON(t, server, http.MethodGet, "/app/music_converter/api/runs", nil, false)
	if len(runs.Body["runs"].([]any)) != 0 {
		t.Fatalf("runs after clear = %#v", runs)
	}
}

func TestFrontendServesAssetsAndSPAFallback(t *testing.T) {
	t.Parallel()
	publicDir := t.TempDir()
	mustWrite(t, filepath.Join(publicDir, "index.html"), "<main>音乐转换中心</main>")
	mustWrite(t, filepath.Join(publicDir, "app.js"), "console.log('ok')")
	root := t.TempDir()
	app, err := New(Options{DataDir: t.TempDir(), PublicDir: publicDir, Prefix: "/app/music_converter", BrowseRoots: []string{root}, Engine: &recordingEngine{}})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	for path, want := range map[string]string{
		"/app/music_converter/":          "音乐转换中心",
		"/app/music_converter/app.js":    "console.log('ok')",
		"/app/music_converter/tasks/123": "音乐转换中心",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		app.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), want) {
			t.Fatalf("GET %s = %d %q", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestInvalidSettingsEmptyStatesAndDirectoryRoots(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	server := newTestServer(t, t.TempDir(), root)
	if response := requestJSON(t, server, http.MethodGet, "/app/music_converter/api/jobs/current", nil, false); response.Code != http.StatusNotFound {
		t.Fatalf("empty current job = %#v", response)
	}
	if response := requestJSON(t, server, http.MethodPost, "/app/music_converter/api/scan", map[string]any{}, true); response.Code != http.StatusBadRequest {
		t.Fatalf("scan without settings = %#v", response)
	}
	rootListing := requestJSON(t, server, http.MethodGet, "/app/music_converter/api/directories", nil, false)
	if rootListing.Code != http.StatusOK || len(rootListing.Body["entries"].([]any)) != 1 {
		t.Fatalf("root listing = %#v", rootListing)
	}
	selectedRoot := requestJSON(t, server, http.MethodGet, "/app/music_converter/api/directories?path="+root, nil, false)
	if selectedRoot.Code != http.StatusOK || selectedRoot.Body["parent"] != "" {
		t.Fatalf("selected root = %#v", selectedRoot)
	}

	validDir := t.TempDir()
	for name, settings := range map[string]Settings{
		"relative":    {SourceDir: "relative", OutputDir: validDir, Conflict: "skip", Concurrency: 1, RetainRuns: 1},
		"concurrency": {SourceDir: validDir, OutputDir: validDir, Conflict: "skip", Concurrency: 9, RetainRuns: 1},
		"retention":   {SourceDir: validDir, OutputDir: validDir, Conflict: "skip", Concurrency: 1, RetainRuns: 0},
		"conflict":    {SourceDir: validDir, OutputDir: validDir, Conflict: "invalid", Concurrency: 1, RetainRuns: 1},
	} {
		t.Run(name, func(t *testing.T) {
			response := requestJSON(t, server, http.MethodPut, "/app/music_converter/api/settings", settings, true)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid settings accepted: %#v", response)
			}
		})
	}
}

func TestNewRejectsInvalidDirectoriesAndRoots(t *testing.T) {
	t.Parallel()
	valid := t.TempDir()
	for name, options := range map[string]Options{
		"relative data":   {DataDir: "data", PublicDir: valid, BrowseRoots: []string{valid}},
		"relative public": {DataDir: valid, PublicDir: "public", BrowseRoots: []string{valid}},
		"relative root":   {DataDir: valid, PublicDir: valid, BrowseRoots: []string{"root"}},
		"missing root":    {DataDir: valid, PublicDir: valid, BrowseRoots: []string{filepath.Join(valid, "missing")}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(options); err == nil {
				t.Fatal("expected constructor error")
			}
		})
	}
}

type responseJSON struct {
	Code int
	Body map[string]any
}

func newTestServer(t *testing.T, dataDir, browseRoot string) http.Handler {
	t.Helper()
	return newTestServerWithEngine(t, dataDir, browseRoot, &recordingEngine{})
}

func newTestServerWithEngine(t *testing.T, dataDir, browseRoot string, engine Engine) http.Handler {
	t.Helper()
	app, err := New(Options{
		DataDir:     dataDir,
		PublicDir:   t.TempDir(),
		Prefix:      "/app/music_converter",
		BrowseRoots: []string{browseRoot},
		Engine:      engine,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return app.Handler()
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, value any, admin bool) responseJSON {
	t.Helper()
	var body io.Reader
	if value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("Content-Type", "application/json")
	if admin {
		request.Header.Set("X-Trim-Isadmin", "true")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	result := responseJSON{Code: recorder.Code, Body: map[string]any{}}
	if recorder.Body.Len() > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &result.Body); err != nil {
			t.Fatalf("decode response %s %s: %v\n%s", method, path, err, recorder.Body.String())
		}
	}
	return result
}

func waitForJob(t *testing.T, handler http.Handler, status string) responseJSON {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job := requestJSON(t, handler, http.MethodGet, "/app/music_converter/api/jobs/current", nil, false)
		if job.Body["status"] == status {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job did not reach %s", status)
	return responseJSON{}
}

type recordingEngine struct {
	calls atomic.Int64
}

type blockingEngine struct {
	started chan struct{}
}

func (engine *blockingEngine) Convert(ctx context.Context, _, _ string, _ bool) (string, error) {
	close(engine.started)
	<-ctx.Done()
	return "", ctx.Err()
}

func (engine *recordingEngine) Convert(_ context.Context, source, stagingDir string, _ bool) (string, error) {
	engine.calls.Add(1)
	output := filepath.Join(stagingDir, strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))+".mp3")
	if err := os.WriteFile(output, []byte("audio"), 0o600); err != nil {
		return "", err
	}
	return output, nil
}

func (engine *recordingEngine) Calls() int {
	return int(engine.calls.Load())
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func Example_aboutDisclaimer() {
	fmt.Println("仅供学习与个人研究使用；不能替代上游许可证或适用法律")
	// Output: 仅供学习与个人研究使用；不能替代上游许可证或适用法律
}
