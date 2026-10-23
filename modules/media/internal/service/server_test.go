package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testService(t *testing.T) *Service {
	t.Helper()
	root := t.TempDir()
	public := filepath.Join(root, "public")
	bundle := filepath.Join(root, "javsp")
	if err := os.MkdirAll(public, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(public, "index.html"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(filepath.Join(root, "data"), public, bundle, "/app/media_acquisition_center", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.closePlayback(context.Background()); service.db.Close() })
	return service
}

func TestGatewayPrefixAndAuthorization(t *testing.T) {
	service := testService(t)
	handler := service.Handler()
	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/app/media_acquisition_center/api/health", nil))
	if health.Code != 200 {
		t.Fatalf("health=%d: %s", health.Code, health.Body.String())
	}
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/app/media_acquisition_center/api/tasks", nil))
	if denied.Code != 403 {
		t.Fatalf("denied=%d", denied.Code)
	}
	allowedRequest := httptest.NewRequest(http.MethodGet, "/app/media_acquisition_center/api/tasks", nil)
	allowedRequest.Header.Set("X-Trim-Isadmin", "true")
	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, allowedRequest)
	if allowed.Code != 200 {
		t.Fatalf("allowed=%d: %s", allowed.Code, allowed.Body.String())
	}
}

func TestPipelineSummaryAndUnifiedRunsCoverTheWholeWorkspace(t *testing.T) {
	service := testService(t)
	mediaRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(mediaRoot, "downloads")
	pending := filepath.Join(mediaRoot, "pending")
	completed := filepath.Join(mediaRoot, "completed")
	for _, directory := range []string{source, pending, completed} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	intake := defaultIntakeConfig()
	intake.SourceDirectory = source
	intake.DestinationDirectory = pending
	if _, err := service.db.UpdateIntakeConfig(intake); err != nil {
		t.Fatal(err)
	}
	javsp := defaultJavspConfig()
	javsp.InputDirectory = pending
	javsp.OutputDirectory = completed
	if _, err := service.db.UpdateJavspConfig(javsp); err != nil {
		t.Fatal(err)
	}
	maintenanceRun, err := service.db.StartMaintenanceRun("intake", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.db.FinishMaintenanceRun(maintenanceRun, "success", 12, 4, 4, []string{}, ""); err != nil {
		t.Fatal(err)
	}
	javspRun, err := service.db.StartJavspRun("schedule")
	if err != nil {
		t.Fatal(err)
	}
	exitCode := 0
	if err := service.db.FinishJavspRun(javspRun, "success", &exitCode, "done", ""); err != nil {
		t.Fatal(err)
	}

	request := func(target string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("X-Trim-Isadmin", "true")
		response := httptest.NewRecorder()
		service.Handler().ServeHTTP(response, req)
		return response
	}

	pipelineResponse := request("/api/pipeline")
	if pipelineResponse.Code != http.StatusOK {
		t.Fatalf("pipeline=%d: %s", pipelineResponse.Code, pipelineResponse.Body.String())
	}
	var pipeline struct {
		Stages []struct {
			Key string `json:"key"`
		} `json:"stages"`
		Paths struct {
			IntakeSource      string   `json:"intake_source"`
			IntakeDestination string   `json:"intake_destination"`
			JavspInput        string   `json:"javsp_input"`
			Consistent        bool     `json:"consistent"`
			Issues            []string `json:"issues"`
		} `json:"paths"`
		RecentRuns []map[string]any `json:"recent_runs"`
	}
	decodeResponseJSON(t, pipelineResponse, &pipeline)
	if len(pipeline.Stages) != 4 {
		t.Fatalf("stages=%+v", pipeline.Stages)
	}
	for index, want := range []string{"rss", "qb", "intake", "javsp"} {
		if pipeline.Stages[index].Key != want {
			t.Fatalf("stage[%d]=%q want %q", index, pipeline.Stages[index].Key, want)
		}
	}
	if !pipeline.Paths.Consistent || pipeline.Paths.IntakeSource == "" || pipeline.Paths.IntakeDestination != pipeline.Paths.JavspInput {
		t.Fatalf("paths=%+v", pipeline.Paths)
	}

	runsResponse := request("/api/runs/unified?limit=20")
	if runsResponse.Code != http.StatusOK {
		t.Fatalf("runs=%d: %s", runsResponse.Code, runsResponse.Body.String())
	}
	var runs struct {
		Items []struct {
			ID     string `json:"id"`
			Module string `json:"module"`
			Status string `json:"status"`
		} `json:"items"`
	}
	decodeResponseJSON(t, runsResponse, &runs)
	modules := map[string]bool{}
	for _, run := range runs.Items {
		if run.ID == "" || run.Status == "" {
			t.Fatalf("invalid run=%+v", run)
		}
		modules[run.Module] = true
	}
	if !modules["intake"] || !modules["javsp"] {
		t.Fatalf("modules=%v runs=%+v", modules, runs.Items)
	}
}

func TestUnifiedRunModuleFilterAppliesBeforeGlobalLimit(t *testing.T) {
	service := testService(t)
	intakeID, err := service.db.StartMaintenanceRun("intake", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.db.FinishMaintenanceRun(intakeID, "success", 1, 1, 0, []string{}, ""); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"empty_directories", "duplicates", "duplicate_delete"} {
		for index := 0; index < 100; index++ {
			id, startErr := service.db.StartMaintenanceRun(kind, "manual")
			if startErr != nil {
				t.Fatal(startErr)
			}
			if finishErr := service.db.FinishMaintenanceRun(id, "success", 1, 1, 1, []string{}, ""); finishErr != nil {
				t.Fatal(finishErr)
			}
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/api/runs/unified?module=intake&limit=20", nil)
	request.Header.Set("X-Trim-Isadmin", "true")
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", response.Code, response.Body.String())
	}
	var runs struct {
		Items []struct {
			Module string `json:"module"`
		} `json:"items"`
	}
	decodeResponseJSON(t, response, &runs)
	if len(runs.Items) != 1 || runs.Items[0].Module != "intake" {
		t.Fatalf("intake runs hidden by newer modules: %+v", runs.Items)
	}
}

func TestRejectsOversizedJSON(t *testing.T) {
	service := testService(t)
	request := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(`{"name":"`+strings.Repeat("x", maxRequestBody)+`"}`))
	request.Header.Set("X-Trim-Isadmin", "true")
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != 400 {
		t.Fatalf("got %d", response.Code)
	}
}

func TestRunHandlesAlreadyCanceledContext(t *testing.T) {
	service := testService(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Run(ctx, listener); err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestUpdateRejectsRunningTask(t *testing.T) {
	service := testService(t)
	lock := service.taskLock(1)
	lock.Lock()
	defer lock.Unlock()
	request := httptest.NewRequest(http.MethodPut, "/api/tasks/1", strings.NewReader(`{}`))
	request.Header.Set("X-Trim-Isadmin", "true")
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("got %d", response.Code)
	}
}

func TestStaticFileSymlinkCannotEscape(t *testing.T) {
	service := testService(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(service.publicDir, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/escape.txt", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("got %d: %s", response.Code, response.Body.String())
	}
}

func TestMaintenanceEndpointsReportDuplicatesAndSafelyCleanEmptyDirectories(t *testing.T) {
	service := testService(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "MIDA-734.mp4"), []byte("same"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "MIDA-734-C.mp4"), []byte("same"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(root, "old-empty")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(empty, old, old); err != nil {
		t.Fatal(err)
	}
	config := defaultMaintenanceConfig()
	config.RootDirectory = root
	config.MinimumAgeHours = 24
	if _, err := service.db.UpdateMaintenanceConfig(config); err != nil {
		t.Fatal(err)
	}

	request := func(method, target, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("X-Trim-Isadmin", "true")
		response := httptest.NewRecorder()
		service.Handler().ServeHTTP(response, req)
		return response
	}
	if response := request(http.MethodGet, "/api/maintenance", ""); response.Code != http.StatusOK {
		t.Fatalf("maintenance=%d: %s", response.Code, response.Body.String())
	}
	duplicateResponse := request(http.MethodPost, "/api/maintenance/duplicates/scan", `{"directory":`+quoteJSON(root)+`}`)
	if duplicateResponse.Code != http.StatusOK {
		t.Fatalf("duplicates=%d: %s", duplicateResponse.Code, duplicateResponse.Body.String())
	}
	var duplicates DuplicateScanResult
	decodeResponseJSON(t, duplicateResponse, &duplicates)
	if len(duplicates.Groups) != 1 || duplicates.Groups[0].MatchType != "fingerprint" {
		t.Fatalf("duplicates = %+v", duplicates)
	}
	previewResponse := request(http.MethodPost, "/api/maintenance/empty-directories/preview", `{}`)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("preview=%d: %s", previewResponse.Code, previewResponse.Body.String())
	}
	var preview EmptyDirectoryResult
	decodeResponseJSON(t, previewResponse, &preview)
	if preview.Found != 1 {
		t.Fatalf("preview = %+v", preview)
	}
	cleanupResponse := request(http.MethodPost, "/api/maintenance/empty-directories/cleanup", `{}`)
	if cleanupResponse.Code != http.StatusOK {
		t.Fatalf("cleanup=%d: %s", cleanupResponse.Code, cleanupResponse.Body.String())
	}
	if _, err := os.Stat(empty); !os.IsNotExist(err) {
		t.Fatalf("empty directory still exists: %v", err)
	}
	for _, name := range []string{"MIDA-734.mp4", "MIDA-734-C.mp4"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("video was changed: %s: %v", name, err)
		}
	}
}

func TestIntakeRunMovesCompletedContentBeforeDeletingQBittorrentRecord(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "download", "av")
	destination := filepath.Join(root, "media", "av", "未处理")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	content := filepath.Join(source, "MIDA-734.mp4")
	if err := os.WriteFile(content, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stops atomic.Int32
	var deletes atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			state := "uploading"
			if stops.Load() > 0 {
				state = "stoppedUP"
			}
			_, _ = fmt.Fprintf(w, `[{"hash":"hash-1","name":"MIDA-734","content_path":%q,"state":%q,"progress":1,"amount_left":0,"added_on":1700000000}]`, content, state)
		case "/api/v2/torrents/stop":
			if err := r.ParseForm(); err != nil || r.Form.Get("hashes") != "hash-1" {
				t.Errorf("stop form=%v err=%v", r.Form, err)
			}
			stops.Add(1)
		case "/api/v2/torrents/delete":
			if err := r.ParseForm(); err != nil || r.Form.Get("hashes") != "hash-1" || r.Form.Get("deleteFiles") != "false" {
				t.Errorf("delete form=%v err=%v", r.Form, err)
			}
			if _, err := os.Stat(filepath.Join(destination, "MIDA-734.mp4")); err != nil {
				t.Errorf("qB record deleted before target was verified: %v", err)
			}
			deletes.Add(1)
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()

	service := testService(t)
	if _, err := service.db.UpdateSettings(map[string]any{
		"qb_host": remote.URL, "qb_username": "test", "qb_password": "test",
	}); err != nil {
		t.Fatal(err)
	}
	config := defaultIntakeConfig()
	config.SourceDirectory = source
	config.DestinationDirectory = destination
	config.RunJavsp = false
	if _, err := service.db.UpdateIntakeConfig(config); err != nil {
		t.Fatal(err)
	}
	result, err := service.runIntake(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if stops.Load() != 1 || deletes.Load() != 1 || result.Moved != 1 || result.TasksDeleted != 1 || result.Failed != 0 {
		t.Fatalf("stops=%d deletes=%d result=%+v", stops.Load(), deletes.Load(), result)
	}
	if _, err := os.Stat(content); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
}

func TestIntakeRunFallsBackToLegacyPauseWhenStopEndpointIsMissing(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "download", "av")
	destination := filepath.Join(root, "media", "av", "未处理")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	content := filepath.Join(source, "legacy-qb.mp4")
	if err := os.WriteFile(content, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stops atomic.Int32
	var pauses atomic.Int32
	var deletes atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			state := "stalledUP"
			if pauses.Load() > 0 {
				state = "pausedUP"
			}
			_, _ = fmt.Fprintf(w, `[{"hash":"legacy-hash","name":"legacy-qb","content_path":%q,"state":%q,"progress":1,"amount_left":0,"added_on":1700000000}]`, content, state)
		case "/api/v2/torrents/stop":
			stops.Add(1)
			http.NotFound(w, r)
		case "/api/v2/torrents/pause":
			if err := r.ParseForm(); err != nil || r.Form.Get("hashes") != "legacy-hash" {
				t.Errorf("pause form=%v err=%v", r.Form, err)
			}
			pauses.Add(1)
		case "/api/v2/torrents/delete":
			deletes.Add(1)
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()

	service := testService(t)
	if _, err := service.db.UpdateSettings(map[string]any{
		"qb_host": remote.URL, "qb_username": "test", "qb_password": "test",
	}); err != nil {
		t.Fatal(err)
	}
	config := defaultIntakeConfig()
	config.SourceDirectory = source
	config.DestinationDirectory = destination
	config.RunJavsp = false
	if _, err := service.db.UpdateIntakeConfig(config); err != nil {
		t.Fatal(err)
	}
	result, err := service.runIntake(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if stops.Load() != 1 || pauses.Load() != 1 || deletes.Load() != 1 || result.Moved != 1 || result.TasksDeleted != 1 || result.Failed != 0 {
		t.Fatalf("stops=%d pauses=%d deletes=%d result=%+v", stops.Load(), pauses.Load(), deletes.Load(), result)
	}
}

func TestJavspDirectoryPickerStaysWithinConfiguredRoot(t *testing.T) {
	service := testService(t)
	root := t.TempDir()
	child := filepath.Join(root, "子目录")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "影片.mp4"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "越权链接")); err != nil {
		t.Fatal(err)
	}
	config := defaultJavspConfig()
	config.InputDirectory = root
	if _, err := service.db.UpdateJavspConfig(config); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	canonicalChild := filepath.Join(canonicalRoot, "子目录")

	request := httptest.NewRequest(http.MethodGet, "/api/javsp/directories", nil)
	request.Header.Set("X-Trim-Isadmin", "true")
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("root listing=%d: %s", response.Code, response.Body.String())
	}
	var listing struct {
		Root   string `json:"root"`
		Path   string `json:"path"`
		Parent string `json:"parent"`
		Items  []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"items"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if listing.Root != canonicalRoot || listing.Path != canonicalRoot || listing.Parent != "" || listing.Truncated {
		t.Fatalf("unexpected root listing: %+v", listing)
	}
	if len(listing.Items) != 1 || listing.Items[0].Name != "子目录" || listing.Items[0].Path != canonicalChild {
		t.Fatalf("unexpected items: %+v", listing.Items)
	}

	nestedRequest := httptest.NewRequest(http.MethodGet, "/api/javsp/directories?path="+url.QueryEscape(child), nil)
	nestedRequest.Header.Set("X-Trim-Isadmin", "true")
	nestedResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(nestedResponse, nestedRequest)
	if nestedResponse.Code != http.StatusOK || !strings.Contains(nestedResponse.Body.String(), `"parent":"`+canonicalRoot+`"`) {
		t.Fatalf("nested listing=%d: %s", nestedResponse.Code, nestedResponse.Body.String())
	}

	for _, target := range []string{outside, filepath.Join(root, "越权链接"), child + string(filepath.Separator) + "..", "relative"} {
		t.Run(target, func(t *testing.T) {
			blockedRequest := httptest.NewRequest(http.MethodGet, "/api/javsp/directories?path="+url.QueryEscape(target), nil)
			blockedRequest.Header.Set("X-Trim-Isadmin", "true")
			blockedResponse := httptest.NewRecorder()
			service.Handler().ServeHTTP(blockedResponse, blockedRequest)
			if blockedResponse.Code != http.StatusBadRequest {
				t.Fatalf("escape listing=%d: %s", blockedResponse.Code, blockedResponse.Body.String())
			}
		})
	}
}

func TestJavspDirectoryPickerUsesFnOSVolumeAsBrowseRoot(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "nested volume directory", input: "/vol1/shared/media", want: "/vol1"},
		{name: "volume root", input: "/vol1", want: "/vol1"},
		{name: "system directory stays contained", input: "/etc/nginx", want: "/etc/nginx"},
		{name: "similar prefix is not a volume", input: "/vol1-backup/media", want: "/vol1-backup/media"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := javspBrowseRoot(test.input); got != test.want {
				t.Fatalf("javspBrowseRoot(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestQBTestSettingsInheritTLSVerification(t *testing.T) {
	current := defaultSettings()
	current.QBHost = "https://qb.example.com"
	inherited := overlayQBSettings(current, map[string]any{})
	if !inherited.QBVerifyCertificate {
		t.Fatal("omitted field disabled TLS verification")
	}
	explicit := overlayQBSettings(current, map[string]any{"qb_verify_certificate": false})
	if explicit.QBVerifyCertificate {
		t.Fatal("explicit self-signed certificate choice was ignored")
	}
}

func TestSettingsValidateFeedProxyAndRunRetention(t *testing.T) {
	service := testService(t)
	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{name: "valid", body: `{"feed_use_proxy":true,"run_retention_count":100}`, wantStatus: http.StatusOK},
		{name: "feed proxy must be boolean", body: `{"feed_use_proxy":"yes"}`, wantStatus: http.StatusBadRequest},
		{name: "retention must be positive", body: `{"run_retention_count":0}`, wantStatus: http.StatusBadRequest},
		{name: "retention must be bounded", body: `{"run_retention_count":101}`, wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(test.body))
			request.Header.Set("X-Trim-Isadmin", "true")
			response := httptest.NewRecorder()
			service.Handler().ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

func TestRSSConcurrencyIsCappedAtTwo(t *testing.T) {
	service := testService(t)
	if !service.acquireRSSSlot() || !service.acquireRSSSlot() {
		t.Fatal("the first two RSS slots must be available")
	}
	if service.acquireRSSSlot() {
		t.Fatal("a third concurrent RSS task was accepted")
	}
	service.releaseRSSSlot()
	service.releaseRSSSlot()
}

func TestManualRSSRunReturnsAccepted(t *testing.T) {
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<rss version="2.0"><channel><title>test</title></channel></rss>`))
	}))
	defer feed.Close()
	service := testService(t)
	task, err := validateTask(Task{
		Name: "test", FeedURL: feed.URL, Enabled: true, ScheduleType: "interval",
		ScheduleMinutes: 60, ScheduleTime: "03:00", Extractor: "direct",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.db.CreateTask(task)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/tasks/%d/run", created.ID), strings.NewReader(`{}`))
	request.Header.Set("X-Trim-Isadmin", "true")
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"accepted":true`) {
		t.Fatalf("got %d: %s", response.Code, response.Body.String())
	}
	service.wait.Wait()
}

func TestRSSRunReusesOneQBSession(t *testing.T) {
	const firstHash = "0123456789abcdef0123456789abcdef01234567"
	const secondHash = "1123456789abcdef0123456789abcdef01234567"
	var logins atomic.Int32
	var submissions atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed":
			_, _ = fmt.Fprintf(w, `<rss version="2.0"><channel><title>test</title>`+
				`<item><title>one</title><description>magnet:?xt=urn:btih:%s</description></item>`+
				`<item><title>two</title><description>magnet:?xt=urn:btih:%s</description></item>`+
				`</channel></rss>`, firstHash, secondHash)
		case "/api/v2/auth/login":
			logins.Add(1)
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/add":
			submissions.Add(1)
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			hash := r.URL.Query().Get("hashes")
			if hash == firstHash && submissions.Load() >= 1 || hash == secondHash && submissions.Load() >= 2 {
				_, _ = fmt.Fprintf(w, `[{"hash":%q,"added_on":1700000000}]`, hash)
			} else {
				_, _ = w.Write([]byte("[]"))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()
	service := testService(t)
	if _, err := service.db.UpdateSettings(map[string]any{
		"preview_mode": false, "qb_host": remote.URL, "qb_username": "test", "qb_password": "test",
	}); err != nil {
		t.Fatal(err)
	}
	task, err := validateTask(Task{
		Name: "test", FeedURL: remote.URL + "/feed", Enabled: true, ScheduleType: "interval",
		ScheduleMinutes: 60, ScheduleTime: "03:00", Extractor: "direct",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.db.CreateTask(task)
	if err != nil {
		t.Fatal(err)
	}
	forceDry := false
	summary, err := service.executeTaskReserved(context.Background(), created.ID, "test", &forceDry)
	if err != nil {
		t.Fatal(err)
	}
	if logins.Load() != 1 || submissions.Load() != 2 || summary.Submitted != 2 {
		t.Fatalf("logins=%d submissions=%d summary=%+v", logins.Load(), submissions.Load(), summary)
	}
}

func TestRSSRunSkipsTorrentAlreadyInQBittorrent(t *testing.T) {
	const infoHash = "0123456789abcdef0123456789abcdef01234567"
	var submissions atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed":
			_, _ = fmt.Fprintf(w, `<rss version="2.0"><channel><title>test</title>`+
				`<item><title>existing</title><description>magnet:?xt=urn:btih:%s</description></item>`+
				`</channel></rss>`, infoHash)
		case "/api/v2/auth/login":
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			if got := r.URL.Query().Get("hashes"); got != infoHash {
				t.Errorf("hashes query = %q, want %q", got, infoHash)
			}
			_, _ = fmt.Fprintf(w, `[{"hash":%q,"added_on":1700000000}]`, infoHash)
		case "/api/v2/torrents/add":
			submissions.Add(1)
			_, _ = w.Write([]byte("Fails."))
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()

	service := testService(t)
	if _, err := service.db.UpdateSettings(map[string]any{
		"preview_mode": false, "qb_host": remote.URL, "qb_username": "test", "qb_password": "test",
	}); err != nil {
		t.Fatal(err)
	}
	task, err := validateTask(Task{
		Name: "test", FeedURL: remote.URL + "/feed", Enabled: true, ScheduleType: "interval",
		ScheduleMinutes: 60, ScheduleTime: "03:00", Extractor: "direct",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.db.CreateTask(task)
	if err != nil {
		t.Fatal(err)
	}
	forceDry := false
	summary, err := service.executeTaskReserved(context.Background(), created.ID, "test", &forceDry)
	if err != nil {
		t.Fatal(err)
	}
	if submissions.Load() != 0 || summary.Skipped != 1 || summary.Failed != 0 || summary.Submitted != 0 {
		t.Fatalf("submissions=%d summary=%+v", submissions.Load(), summary)
	}
}

func TestRSSRunTreatsConcurrentQBittorrentAddAsSkipped(t *testing.T) {
	const infoHash = "1123456789abcdef0123456789abcdef01234567"
	var infoRequests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed":
			_, _ = fmt.Fprintf(w, `<rss version="2.0"><channel><title>test</title>`+
				`<item><title>racing</title><description>magnet:?xt=urn:btih:%s</description></item>`+
				`</channel></rss>`, infoHash)
		case "/api/v2/auth/login":
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			if infoRequests.Add(1) == 1 {
				_, _ = w.Write([]byte("[]"))
				return
			}
			_, _ = fmt.Fprintf(w, `[{"hash":%q,"added_on":1700000000}]`, infoHash)
		case "/api/v2/torrents/add":
			_, _ = w.Write([]byte("Fails."))
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()

	service := testService(t)
	if _, err := service.db.UpdateSettings(map[string]any{
		"preview_mode": false, "qb_host": remote.URL, "qb_username": "test", "qb_password": "test",
	}); err != nil {
		t.Fatal(err)
	}
	task, err := validateTask(Task{
		Name: "test", FeedURL: remote.URL + "/feed", Enabled: true, ScheduleType: "interval",
		ScheduleMinutes: 60, ScheduleTime: "03:00", Extractor: "direct",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.db.CreateTask(task)
	if err != nil {
		t.Fatal(err)
	}
	forceDry := false
	summary, err := service.executeTaskReserved(context.Background(), created.ID, "test", &forceDry)
	if err != nil {
		t.Fatal(err)
	}
	if infoRequests.Load() != 2 || summary.Skipped != 1 || summary.Failed != 0 || summary.Submitted != 0 {
		t.Fatalf("info requests=%d summary=%+v", infoRequests.Load(), summary)
	}
}

func TestRSSRunKeepsUnconfirmedQBittorrentRejectionAsFailure(t *testing.T) {
	const infoHash = "2123456789abcdef0123456789abcdef01234567"
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed":
			_, _ = fmt.Fprintf(w, `<rss version="2.0"><channel><title>test</title>`+
				`<item><title>rejected</title><description>magnet:?xt=urn:btih:%s</description></item>`+
				`</channel></rss>`, infoHash)
		case "/api/v2/auth/login":
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			_, _ = w.Write([]byte("[]"))
		case "/api/v2/torrents/add":
			_, _ = w.Write([]byte("Fails."))
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()

	service := testService(t)
	if _, err := service.db.UpdateSettings(map[string]any{
		"preview_mode": false, "qb_host": remote.URL, "qb_username": "test", "qb_password": "test",
	}); err != nil {
		t.Fatal(err)
	}
	task, err := validateTask(Task{
		Name: "test", FeedURL: remote.URL + "/feed", Enabled: true, ScheduleType: "interval",
		ScheduleMinutes: 60, ScheduleTime: "03:00", Extractor: "direct",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.db.CreateTask(task)
	if err != nil {
		t.Fatal(err)
	}
	forceDry := false
	summary, err := service.executeTaskReserved(context.Background(), created.ID, "test", &forceDry)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Failed != 1 || summary.Skipped != 0 || summary.Submitted != 0 {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestRSSRunSkipsMediaAlreadyOrganizedByJavsp(t *testing.T) {
	const newInfoHash = "3123456789abcdef0123456789abcdef01234567"
	var submissions atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed":
			_, _ = fmt.Fprintf(w, `<rss version="2.0"><channel><title>test</title>`+
				`<item><title>HD VENX-345</title><description>没有磁力链接</description></item>`+
				`<item><title>HD VENX-346</title><description>magnet:?xt=urn:btih:%s</description></item>`+
				`</channel></rss>`, newInfoHash)
		case "/api/v2/auth/login":
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			if submissions.Load() > 0 {
				_, _ = fmt.Fprintf(w, `[{"hash":%q,"added_on":1700000000}]`, newInfoHash)
			} else {
				_, _ = w.Write([]byte("[]"))
			}
		case "/api/v2/torrents/add":
			submissions.Add(1)
			_, _ = w.Write([]byte("Ok."))
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()

	service := testService(t)
	inputDirectory := t.TempDir()
	completedMovie := filepath.Join(inputDirectory, "#整理完成", "actor", "[VENX-345] title")
	if err := os.MkdirAll(completedMovie, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(completedMovie, "VENX-345.mp4"), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(inputDirectory, "#整理完成", "actor", "[VENX-346] empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	javsp := defaultJavspConfig()
	javsp.InputDirectory = inputDirectory
	javsp.OutputFolderPattern = "#整理完成/{actress}/[{num}] {title}"
	if _, err := service.db.UpdateJavspConfig(javsp); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.UpdateSettings(map[string]any{
		"preview_mode": false, "qb_host": remote.URL, "qb_username": "test", "qb_password": "test",
	}); err != nil {
		t.Fatal(err)
	}
	task, err := validateTask(Task{
		Name: "test", FeedURL: remote.URL + "/feed", Enabled: true, ScheduleType: "interval",
		ScheduleMinutes: 60, ScheduleTime: "03:00", Extractor: "direct",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.db.CreateTask(task)
	if err != nil {
		t.Fatal(err)
	}
	forceDry := false
	summary, err := service.executeTaskReserved(context.Background(), created.ID, "test", &forceDry)
	if err != nil {
		t.Fatal(err)
	}
	if submissions.Load() != 1 || summary.Skipped != 1 || summary.Submitted != 1 || summary.Failed != 0 {
		t.Fatalf("submissions=%d summary=%+v", submissions.Load(), summary)
	}
}
