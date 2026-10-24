package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/playback"
)

const (
	maxRequestBody      = 1 << 20
	maxDirectoryEntries = 1000
	rssJobTimeout       = 30 * time.Minute
	previewTimeout      = 120 * time.Second
)

var (
	errTaskBusy        = errors.New("任务正在运行，请稍后再试")
	errRSSBusy         = errors.New("RSS 任务并发已满，请稍后再试")
	errMaintenanceBusy = errors.New("媒体维护任务正在运行，请稍后再试")
	errIntakeBusy      = errors.New("下载入库任务正在运行，请稍后再试")
)

type Service struct {
	db                 *Database
	collector          Collector
	javsp              *JavspRunner
	anidb              aniDBTitleProvider
	publicDir          string
	prefix             string
	localDev           bool
	catalogCSRF        string
	browserImportMu    sync.Mutex
	resourceSearchMu   sync.Mutex
	dataDir            string
	playbackMu         sync.Mutex
	playback           *playback.Manager
	playbackClosed     bool
	taskLocksMu        sync.Mutex
	taskLocks          map[int64]*sync.Mutex
	javspLock          sync.Mutex
	javspDiscoveryLock sync.Mutex
	discoveryMu        sync.Mutex
	discoveryState     movieDiscoveryStatus
	discoveryCancel    context.CancelFunc
	discoverMovies     movieDiscoveryFunc
	maintenanceLock    sync.Mutex
	intakeLock         sync.Mutex
	downloadLock       sync.Mutex
	completedLibrary   completedLibraryState
	mediaFiles         sync.Mutex // shared by every production media-file writer
	rssSlots           chan struct{}
	queueWake          chan struct{}
	mediaJobCancels    sync.Map
	catalogScanWake    chan struct{}
	catalogScanCancels sync.Map
	wait               sync.WaitGroup
	runCtx             context.Context
	cancel             context.CancelFunc
	server             *http.Server
	shutdownOnce       sync.Once
	shutdownDone       chan struct{}
	shutdownErr        error
}

func NewService(dataDir, publicDir, bundleDir, prefix string, localDev bool) (*Service, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	db, err := OpenDatabase(filepath.Join(dataDir, "tasks.db"))
	if err != nil {
		return nil, err
	}
	runner := NewJavspRunner(bundleDir, dataDir)
	if existing, readErr := runner.ReadConfig(); readErr == nil {
		if migrateErr := db.MigrateJavspAdvancedConfig(existing); migrateErr != nil {
			db.Close()
			return nil, migrateErr
		}
	} else {
		slog.Warn("JAVSP advanced config migration skipped", "error", readErr)
	}
	service := &Service{db: db, collector: Collector{}, javsp: runner, anidb: newAniDBTitleCache(dataDir), publicDir: publicDir, dataDir: dataDir,
		prefix: strings.TrimRight(prefix, "/"), localDev: localDev, catalogCSRF: rand.Text(), taskLocks: map[int64]*sync.Mutex{},
		rssSlots: make(chan struct{}, 2), queueWake: make(chan struct{}, 1), catalogScanWake: make(chan struct{}, 1), shutdownDone: make(chan struct{})}
	if err := service.recoverCanceledMediaJobs(); err != nil {
		db.Close()
		return nil, err
	}
	return service, nil
}

func (s *Service) acquireRSSSlot() bool {
	select {
	case s.rssSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Service) releaseRSSSlot() { <-s.rssSlots }

func (s *Service) taskLock(id int64) *sync.Mutex {
	s.taskLocksMu.Lock()
	defer s.taskLocksMu.Unlock()
	if s.taskLocks[id] == nil {
		s.taskLocks[id] = &sync.Mutex{}
	}
	return s.taskLocks[id]
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		slog.Error("write response", "error", err)
	}
}
func writeError(writer http.ResponseWriter, status int, err error) {
	writeJSON(writer, status, map[string]string{"error": err.Error()})
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBody)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("请求内容无效：%w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("请求内容无效：%w", err)
		}
		return fmt.Errorf("请求只能包含一个 JSON 对象")
	}
	return nil
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	s.registerCatalogRoutes(mux)
	s.registerPlaybackRoutes(mux)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": Version, "runtime": "go", "process_model": "single"})
	})
	mux.HandleFunc("GET /api/dashboard", s.handleDashboard)
	mux.HandleFunc("GET /api/pipeline", s.handlePipeline)
	mux.HandleFunc("GET /api/queue", s.handleQueue)
	mux.HandleFunc("GET /api/queue/{id}", s.handleQueueDetail)
	mux.HandleFunc("POST /api/queue/{id}/{action}", s.handleQueueAction)
	mux.HandleFunc("GET /api/tasks", s.handleTasks)
	mux.HandleFunc("GET /api/library/completed", s.handleCompletedLibrary)
	mux.HandleFunc("POST /api/library/completed/scan", s.handleScanCompletedLibrary)
	mux.HandleFunc("POST /api/tasks", s.handleCreateTask)
	mux.HandleFunc("PUT /api/tasks/{id}", s.handleUpdateTask)
	mux.HandleFunc("DELETE /api/tasks/{id}", s.handleDeleteTask)
	mux.HandleFunc("POST /api/tasks/preview", s.handlePreviewTask)
	mux.HandleFunc("POST /api/tasks/{id}/preview", s.handlePreviewSavedTask)
	mux.HandleFunc("POST /api/tasks/{id}/run", s.handleRunTask)
	mux.HandleFunc("GET /api/runs", s.handleRuns)
	mux.HandleFunc("GET /api/runs/unified", s.handleUnifiedRuns)
	mux.HandleFunc("GET /api/javsp", s.handleJavsp)
	mux.HandleFunc("GET /api/javsp/directories", s.handleJavspDirectories)
	mux.HandleFunc("PUT /api/javsp/config", s.handleJavspConfig)
	mux.HandleFunc("POST /api/javsp/naming-preview", s.handleJavspNamingPreview)
	mux.HandleFunc("POST /api/javsp/run", s.handleJavspRun)
	mux.HandleFunc("POST /api/javsp/cancel", s.handleJavspCancel)
	mux.HandleFunc("POST /api/javsp/discovery/cancel", s.handleMovieDiscoveryCancel)
	mux.HandleFunc("POST /api/javsp/test", s.handleJavspTest)
	mux.HandleFunc("GET /api/anime", s.handleAnime)
	mux.HandleFunc("GET /api/anime/directories", s.handleAnimeDirectories)
	mux.HandleFunc("PUT /api/anime/config", s.handleAnimeConfig)
	mux.HandleFunc("POST /api/anime/titles/refresh", s.handleAnimeTitleRefresh)
	mux.HandleFunc("POST /api/anime/scan", s.handleAnimeScan)
	mux.HandleFunc("POST /api/anime/nfo", s.handleAnimeNFO)
	mux.HandleFunc("GET /api/maintenance", s.handleMaintenance)
	mux.HandleFunc("GET /api/maintenance/directories", s.handleMaintenanceDirectories)
	mux.HandleFunc("PUT /api/maintenance/config", s.handleMaintenanceConfig)
	mux.HandleFunc("POST /api/maintenance/duplicates/scan", s.handleDuplicateScan)
	mux.HandleFunc("POST /api/maintenance/duplicates/delete", s.handleDuplicateDelete)
	mux.HandleFunc("POST /api/maintenance/duplicates/preview", s.handleAutomaticDuplicatePreview)
	mux.HandleFunc("POST /api/maintenance/duplicates/cleanup", s.handleAutomaticDuplicateCleanup)
	mux.HandleFunc("POST /api/maintenance/orphan-folders/preview", s.handleOrphanPreview)
	mux.HandleFunc("POST /api/maintenance/orphan-folders/cleanup", s.handleOrphanCleanup)
	mux.HandleFunc("POST /api/maintenance/empty-directories/preview", s.handleEmptyDirectoryPreview)
	mux.HandleFunc("POST /api/maintenance/empty-directories/cleanup", s.handleEmptyDirectoryCleanup)
	mux.HandleFunc("GET /api/intake", s.handleIntake)
	mux.HandleFunc("GET /api/intake/directories", s.handleIntakeDirectories)
	mux.HandleFunc("PUT /api/intake/config", s.handleIntakeConfig)
	mux.HandleFunc("POST /api/intake/preview", s.handleIntakePreview)
	mux.HandleFunc("POST /api/intake/run", s.handleIntakeRun)
	mux.HandleFunc("GET /api/settings", s.handleSettings)
	mux.HandleFunc("GET /api/settings/paths", s.handleMediaPathSettings)
	mux.HandleFunc("POST /api/settings/paths/preview", s.handleMediaPathPreview)
	mux.HandleFunc("PUT /api/settings/paths", s.handleUpdateMediaPathSettings)
	mux.HandleFunc("PUT /api/settings", s.handleUpdateSettings)
	mux.HandleFunc("POST /api/settings/test-qbittorrent", s.handleTestQB)
	mux.HandleFunc("/", s.handleStatic)
	return s.recover(s.stripPrefix(s.authorize(mux)))
}

type directoryItem struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type directoryListing struct {
	Root      string          `json:"root"`
	Path      string          `json:"path"`
	Parent    string          `json:"parent,omitempty"`
	Items     []directoryItem `json:"items"`
	Truncated bool            `json:"truncated"`
}

func (s *Service) handleJavspDirectories(w http.ResponseWriter, r *http.Request) {
	config, err := s.db.JavspConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	selected := config.InputDirectory
	if r.URL.Query().Get("target") == "output" && config.OutputDirectory != "" {
		selected = config.OutputDirectory
	}
	configuredRoot, err := canonicalDirectory(selected)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("当前 JAVSP 选定目录不可访问"))
		return
	}
	root := configuredRoot
	if candidate := javspBrowseRoot(configuredRoot); candidate != configuredRoot {
		if volumeRoot, rootErr := canonicalDirectory(candidate); rootErr == nil && pathWithinRoot(volumeRoot, configuredRoot) {
			root = volumeRoot
		}
	}
	target := configuredRoot
	if requested := strings.TrimSpace(r.URL.Query().Get("path")); requested != "" {
		if strings.ContainsRune(requested, 0) || !filepath.IsAbs(requested) || hasParentSegment(requested) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("目录路径无效"))
			return
		}
		target, err = canonicalDirectory(requested)
		if err != nil || !pathWithinRoot(root, target) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("只能浏览当前存储空间内可访问的目录"))
			return
		}
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("无法读取目录：%w", err))
		return
	}
	listing := directoryListing{Root: root, Path: target, Items: make([]directoryItem, 0, min(len(entries), maxDirectoryEntries))}
	if target != root {
		listing.Parent = filepath.Dir(target)
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if len(listing.Items) == maxDirectoryEntries {
			listing.Truncated = true
			break
		}
		child, childErr := canonicalDirectory(filepath.Join(target, entry.Name()))
		if childErr != nil || !pathWithinRoot(root, child) {
			continue
		}
		listing.Items = append(listing.Items, directoryItem{Name: entry.Name(), Path: child})
	}
	writeJSON(w, http.StatusOK, listing)
}

func (s *Service) handleAnime(w http.ResponseWriter, _ *http.Request) {
	config, err := s.db.AnimeConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	cache := AniDBCacheInfo{Source: aniDBTitleURL}
	if provider, ok := s.anidb.(*aniDBTitleCache); ok {
		if info, statErr := os.Stat(provider.path); statErr == nil {
			cache.UpdatedAt = info.ModTime().Format(time.RFC3339)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"config": config, "cache": cache, "metadata_mode": "title_aid", "official_client_required": true,
	})
}

func (s *Service) handleAnimeConfig(w http.ResponseWriter, r *http.Request) {
	var config AnimeConfig
	if err := decodeJSON(w, r, &config); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	config, err := s.db.UpdateAnimeConfig(config)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (s *Service) handleAnimeDirectories(w http.ResponseWriter, r *http.Request) {
	config, err := s.db.AnimeConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	targetPath := config.Directory
	if requested := strings.TrimSpace(r.URL.Query().Get("path")); requested != "" {
		targetPath = requested
	}
	target, err := canonicalDirectory(targetPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("目录路径无效或不可访问"))
		return
	}
	root := target
	if candidate := javspBrowseRoot(target); candidate != target {
		if volumeRoot, rootErr := canonicalDirectory(candidate); rootErr == nil && pathWithinRoot(volumeRoot, target) {
			root = volumeRoot
		}
	}
	s.writeDirectoryListing(w, root, target)
}

func (s *Service) writeDirectoryListing(w http.ResponseWriter, root, target string) {
	entries, err := os.ReadDir(target)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("无法读取目录：%w", err))
		return
	}
	listing := directoryListing{Root: root, Path: target, Items: make([]directoryItem, 0, min(len(entries), maxDirectoryEntries))}
	if target != root {
		listing.Parent = filepath.Dir(target)
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if len(listing.Items) == maxDirectoryEntries {
			listing.Truncated = true
			break
		}
		child, childErr := canonicalDirectory(filepath.Join(target, entry.Name()))
		if childErr != nil || !pathWithinRoot(root, child) {
			continue
		}
		listing.Items = append(listing.Items, directoryItem{Name: entry.Name(), Path: child})
	}
	writeJSON(w, http.StatusOK, listing)
}

func (s *Service) handleAnimeTitleRefresh(w http.ResponseWriter, r *http.Request) {
	titles, cache, err := s.anidb.Load(r.Context(), true)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	cache.TitleCount = len(titles)
	writeJSON(w, http.StatusOK, cache)
}

func (s *Service) handleAnimeScan(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Directory string `json:"directory"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, err := canonicalDirectory(payload.Directory); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("番剧扫描目录不可访问"))
		return
	}
	titles, cache, err := s.anidb.Load(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	result, err := scanAnimeDirectory(payload.Directory, titles, cache)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) handleAnimeNFO(w http.ResponseWriter, r *http.Request) {
	if !s.acquireMediaFileWriter() {
		writeError(w, http.StatusConflict, errMaintenanceBusy)
		return
	}
	defer s.mediaFiles.Unlock()
	var payload animeNFORequest
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := writeAnimeNFOs(payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) handleMaintenance(w http.ResponseWriter, _ *http.Request) {
	config, err := s.db.MaintenanceConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	runs, err := s.db.ListMaintenanceRunsByKinds(100, "duplicates", "duplicate_delete", "duplicate_cleanup", "orphan_cleanup", "empty_directories")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": config, "runs": runs})
}

func (s *Service) handleMaintenanceConfig(w http.ResponseWriter, r *http.Request) {
	var config MaintenanceConfig
	if err := decodeJSON(w, r, &config); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	config, err := s.db.UpdateMaintenanceConfig(config)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (s *Service) handleMaintenanceDirectories(w http.ResponseWriter, r *http.Request) {
	config, err := s.db.MaintenanceConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	targetPath := config.RootDirectory
	if requested := strings.TrimSpace(r.URL.Query().Get("path")); requested != "" {
		targetPath = requested
	}
	target, err := canonicalDirectory(targetPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("目录路径无效或不可访问"))
		return
	}
	root := target
	if candidate := javspBrowseRoot(target); candidate != target {
		if volumeRoot, rootErr := canonicalDirectory(candidate); rootErr == nil && pathWithinRoot(volumeRoot, target) {
			root = volumeRoot
		}
	}
	s.writeDirectoryListing(w, root, target)
}

func (s *Service) handleDuplicateScan(w http.ResponseWriter, r *http.Request) {
	if !s.maintenanceLock.TryLock() {
		writeError(w, http.StatusConflict, errMaintenanceBusy)
		return
	}
	defer s.maintenanceLock.Unlock()
	var payload struct {
		Directory string `json:"directory"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(payload.Directory) == "" {
		config, err := s.db.MaintenanceConfig()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		payload.Directory = config.RootDirectory
	}
	runID, err := s.db.StartMaintenanceRun("duplicates", "manual")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	result, scanErr := scanDuplicateVideos(payload.Directory)
	if scanErr != nil {
		_ = s.db.FinishMaintenanceRun(runID, "failed", 0, 0, 0, []string{}, scanErr.Error())
		writeError(w, http.StatusBadRequest, scanErr)
		return
	}
	details := make([]string, 0, min(len(result.Groups), 1000))
	for _, group := range result.Groups {
		if len(details) == cap(details) {
			break
		}
		details = append(details, group.MediaID)
	}
	if err := s.db.FinishMaintenanceRun(runID, "success", result.ScannedVideos, len(result.Groups), 0, details, ""); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) handleEmptyDirectoryPreview(w http.ResponseWriter, _ *http.Request) {
	if !s.maintenanceLock.TryLock() {
		writeError(w, http.StatusConflict, errMaintenanceBusy)
		return
	}
	defer s.maintenanceLock.Unlock()
	config, err := s.db.MaintenanceConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	result, err := inspectEmptyDirectories(config.RootDirectory, time.Duration(config.MinimumAgeHours)*time.Hour, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) handleEmptyDirectoryCleanup(w http.ResponseWriter, _ *http.Request) {
	result, err := s.runMaintenanceCleanup("manual")
	if errors.Is(err, errMaintenanceBusy) {
		writeError(w, http.StatusConflict, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) runMaintenanceCleanup(trigger string) (EmptyDirectoryResult, error) {
	if !s.acquireMediaFileWriter() {
		return EmptyDirectoryResult{}, errMaintenanceBusy
	}
	defer s.mediaFiles.Unlock()
	if !s.maintenanceLock.TryLock() {
		return EmptyDirectoryResult{}, errMaintenanceBusy
	}
	defer s.maintenanceLock.Unlock()
	config, err := s.db.MaintenanceConfig()
	if err != nil {
		return EmptyDirectoryResult{}, err
	}
	if err := s.db.MarkMaintenanceStarted(config); err != nil {
		return EmptyDirectoryResult{}, err
	}
	return s.emptyDirectoryCleanupLocked(config, trigger)
}

func (s *Service) emptyDirectoryCleanupLocked(config MaintenanceConfig, trigger string) (EmptyDirectoryResult, error) {
	runID, err := s.db.StartMaintenanceRun("empty_directories", trigger)
	if err != nil {
		return EmptyDirectoryResult{}, err
	}
	result, cleanupErr := inspectEmptyDirectories(config.RootDirectory, time.Duration(config.MinimumAgeHours)*time.Hour, true)
	status, message := "success", ""
	if cleanupErr != nil {
		status, message = "failed", cleanupErr.Error()
	}
	details := append([]string(nil), result.Directories...)
	if len(details) > 1000 {
		details = details[:1000]
	}
	if finishErr := s.db.FinishMaintenanceRun(runID, status, result.ScannedDirs, result.Found, result.Removed, details, message); finishErr != nil {
		return result, finishErr
	}
	return result, cleanupErr
}

func javspBrowseRoot(path string) string {
	cleaned := filepath.Clean(path)
	parts := strings.Split(strings.TrimPrefix(filepath.ToSlash(cleaned), "/"), "/")
	if len(parts) == 0 || len(parts[0]) <= len("vol") || !strings.HasPrefix(parts[0], "vol") {
		return cleaned
	}
	volumeID, err := strconv.Atoi(strings.TrimPrefix(parts[0], "vol"))
	if err != nil || volumeID < 1 || parts[0] != "vol"+strconv.Itoa(volumeID) {
		return cleaned
	}
	return filepath.Join(string(filepath.Separator), parts[0])
}

func canonicalDirectory(path string) (string, error) {
	if strings.ContainsRune(path, 0) || !filepath.IsAbs(path) || hasParentSegment(path) {
		return "", fmt.Errorf("invalid absolute directory")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(resolved)
	if err != nil || abs == string(filepath.Separator) {
		return "", fmt.Errorf("directory must not be root")
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("not a directory")
	}
	return abs, nil
}

func hasParentSegment(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func (s *Service) stripPrefix(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.prefix != "" && (r.URL.Path == s.prefix || strings.HasPrefix(r.URL.Path, s.prefix+"/")) {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, s.prefix)
			if r.URL.Path == "" {
				r.URL.Path = "/"
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Service) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/health" && !s.localDev && !strings.EqualFold(r.Header.Get("X-Trim-Isadmin"), "true") {
			writeError(w, http.StatusForbidden, fmt.Errorf("仅管理员可以管理采集任务"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Service) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if value := recover(); value != nil {
				slog.Error("request panic", "value", value)
				writeError(w, http.StatusInternalServerError, fmt.Errorf("服务器内部错误"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Service) handleDashboard(w http.ResponseWriter, _ *http.Request) {
	data, err := s.db.Dashboard()
	if err != nil {
		writeError(w, 500, err)
		return
	}
	settings, err := s.db.Settings(false)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	data["settings"] = settings
	writeJSON(w, 200, data)
}
func (s *Service) handleTasks(w http.ResponseWriter, _ *http.Request) {
	items, err := s.db.Tasks()
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Service) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var task Task
	if err := decodeJSON(w, r, &task); err != nil {
		writeError(w, 400, err)
		return
	}
	task, err := validateTask(task)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	created, err := s.db.CreateTask(task)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, created)
}
func parseID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("任务编号无效")
	}
	return id, nil
}
func (s *Service) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	lock := s.taskLock(id)
	if !lock.TryLock() {
		writeError(w, http.StatusConflict, fmt.Errorf("任务正在运行，暂时不能修改"))
		return
	}
	defer lock.Unlock()
	var task Task
	if err := decodeJSON(w, r, &task); err != nil {
		writeError(w, 400, err)
		return
	}
	task, err = validateTask(task)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	updated, err := s.db.UpdateTask(id, task)
	if errors.Is(err, ErrNotFound) {
		writeError(w, 404, fmt.Errorf("任务不存在"))
		return
	}
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, updated)
}
func (s *Service) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	lock := s.taskLock(id)
	if !lock.TryLock() {
		writeError(w, http.StatusConflict, fmt.Errorf("任务正在运行，暂时不能删除"))
		return
	}
	defer func() {
		lock.Unlock()
		s.taskLocksMu.Lock()
		if s.taskLocks[id] == lock {
			delete(s.taskLocks, id)
		}
		s.taskLocksMu.Unlock()
	}()
	deleted, err := s.db.DeleteTask(id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if !deleted {
		writeError(w, 404, fmt.Errorf("任务不存在"))
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Service) handlePreviewTask(w http.ResponseWriter, r *http.Request) {
	var task Task
	if err := decodeJSON(w, r, &task); err != nil {
		writeError(w, 400, err)
		return
	}
	task, err := validateTask(task)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	s.preview(w, r, task)
}
func (s *Service) handlePreviewSavedTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	task, err := s.db.Task(id)
	if errors.Is(err, ErrNotFound) {
		writeError(w, 404, fmt.Errorf("任务不存在"))
		return
	}
	if err != nil {
		writeError(w, 500, err)
		return
	}
	s.preview(w, r, task)
}
func (s *Service) preview(w http.ResponseWriter, r *http.Request, task Task) {
	if !s.acquireRSSSlot() {
		writeError(w, http.StatusTooManyRequests, fmt.Errorf("RSS 任务并发已满，请稍后再试"))
		return
	}
	defer s.releaseRSSSlot()
	ctx, cancel := context.WithTimeout(r.Context(), previewTimeout)
	defer cancel()
	settings, err := s.db.Settings(true)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	result, err := s.collector.Preview(ctx, task, settings)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, result)
}

func (s *Service) executeTaskReserved(ctx context.Context, id int64, trigger string, forceDry *bool) (RunSummary, error) {
	jobCtx, cancel := context.WithTimeout(ctx, rssJobTimeout)
	defer cancel()
	task, err := s.db.Task(id)
	if err != nil {
		return RunSummary{}, err
	}
	settings, err := s.db.Settings(true)
	if err != nil {
		return RunSummary{}, err
	}
	dry := settings.PreviewMode
	if forceDry != nil {
		dry = *forceDry
	}
	runID, err := s.db.StartRun(id, trigger, dry)
	if err != nil {
		return RunSummary{}, err
	}
	summary := RunSummary{RunID: runID, DryRun: dry, Details: []RunDetail{}}
	defer func() {
		if scheduleErr := s.db.MarkTaskScheduled(id, task); scheduleErr != nil {
			slog.Error("schedule task", "error", scheduleErr, "task_id", id)
		}
	}()
	entries, err := s.collector.FetchEntries(jobCtx, task, settings)
	if err != nil {
		if finishErr := s.db.FinishRun(runID, summary, "failed", err.Error()); finishErr != nil {
			return summary, fmt.Errorf("%v；记录运行结果失败：%w", err, finishErr)
		}
		return summary, err
	}
	summary.Entries = len(entries)
	completedIndex := completedMediaIndex{ids: map[string]struct{}{}}
	if javspConfig, configErr := s.db.JavspConfig(); configErr != nil {
		slog.Warn("load JAVSP config for RSS deduplication", "error", configErr, "task_id", id)
	} else if index, indexErr := s.completedIndex(ctx, javspConfig); indexErr != nil {
		if javspConfig.OutputDirectory != "" {
			err := fmt.Errorf("成品媒体库无法核对，已暂停本次 RSS 投递，请检查整理完成目录：%w", indexErr)
			finishErr := s.db.FinishRun(runID, summary, "failed", err.Error())
			return summary, errors.Join(err, finishErr)
		}
		slog.Warn("index organized media for RSS deduplication", "error", indexErr, "task_id", id)
	} else {
		completedIndex = index
	}
	var qbSession *QBSession
	defer func() {
		if qbSession != nil {
			qbSession.Close()
		}
	}()
	for _, entry := range entries {
		if !entry.Matched {
			continue
		}
		summary.Matched++
		detail := RunDetail{Title: boundedText(entry.Title, 1024), Status: "matched"}
		knownReason := ""
		if mediaID, completed := completedIndex.MatchTitle(entry.Title); completed {
			knownReason = "整理完成目录中已存在：" + mediaID
		}
		if dry {
			detail.Status = "preview"
			if knownReason != "" {
				detail.Status, detail.Message = "skipped", knownReason
			} else if _, err := s.collector.ResolveMagnet(jobCtx, entry, task, settings); err != nil {
				detail.Status, detail.Message = "failed", boundedText(err.Error(), 2048)
			}
		} else {
			job, enqueueErr := s.db.enqueueRSSAcquisition(task, entry, knownReason)
			if enqueueErr != nil {
				detail.Status, detail.Message = "failed", boundedText(enqueueErr.Error(), 2048)
			} else {
				detail, err = s.executeRSSAcquisition(jobCtx, job, settings, &qbSession)
				detail.JobID = job.ID
				if err != nil {
					detail.Status, detail.Message = "failed", boundedText(err.Error(), 2048)
				}
			}
		}
		switch detail.Status {
		case "submitted":
			summary.Submitted++
		case "skipped":
			summary.Skipped++
		case "failed":
			summary.Failed++
		}
		summary.Details = append(summary.Details, detail)
	}
	status := "success"
	if summary.Failed > 0 {
		status = "failed"
		if summary.Submitted > 0 || dry {
			status = "partial"
		}
	}
	if err := s.db.FinishRun(runID, summary, status, ""); err != nil {
		return summary, err
	}
	return summary, nil
}

func (s *Service) startTask(ctx context.Context, id int64, trigger string, forceDry *bool) error {
	lock := s.taskLock(id)
	if !lock.TryLock() {
		return errTaskBusy
	}
	if !s.acquireRSSSlot() {
		lock.Unlock()
		return errRSSBusy
	}
	if _, err := s.db.Task(id); err != nil {
		s.releaseRSSSlot()
		lock.Unlock()
		return err
	}
	jobCtx := s.runCtx
	if jobCtx == nil {
		jobCtx = context.WithoutCancel(ctx)
	}
	s.wait.Add(1)
	go func() {
		defer s.wait.Done()
		defer s.releaseRSSSlot()
		defer lock.Unlock()
		if _, err := s.executeTaskReserved(jobCtx, id, trigger, forceDry); err != nil {
			slog.Error("background RSS task", "error", err, "task_id", id, "trigger", trigger)
		}
	}()
	return nil
}

func (s *Service) handleRunTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var payload struct {
		DryRun *bool `json:"dry_run"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	err = s.startTask(r.Context(), id, "manual", payload.DryRun)
	if errors.Is(err, ErrNotFound) {
		writeError(w, 404, fmt.Errorf("任务不存在"))
		return
	}
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}
func (s *Service) handleRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 {
		limit = 100
	}
	items, err := s.db.ListRuns(limit)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Service) handleJavsp(w http.ResponseWriter, r *http.Request) {
	config, err := s.db.JavspConfig()
	if err != nil {
		writeError(w, 500, err)
		return
	}
	runs, err := s.db.ListJavspRuns(20)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	running := true
	if s.javspLock.TryLock() {
		running = false
		s.javspLock.Unlock()
	}
	discovery := s.movieDiscoveryStatus()
	writeJSON(w, 200, map[string]any{"config": config, "component": s.javsp.Component(r.Context()), "config_path": s.javsp.ConfigPath(), "running": running || discovery.Status == "running", "organizing": running, "discovery": discovery, "progress": s.javsp.LastProgress(), "live_output": s.javsp.CurrentOutput(), "runs": runs})
}

func (s *Service) handleJavspConfig(w http.ResponseWriter, r *http.Request) {
	var config JavspConfig
	if err := decodeJSON(w, r, &config); err != nil {
		writeError(w, 400, err)
		return
	}
	if config.TranslatorAPIKey == "__UNCHANGED__" {
		current, currentErr := s.db.JavspConfigPrivate()
		if currentErr != nil {
			writeError(w, 500, currentErr)
			return
		}
		config.TranslatorAPIKey = current.TranslatorAPIKey
	}
	config, err := validateJavspConfig(config)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	// Running organizers own an in-memory configuration snapshot. Validate
	// future settings without touching the runtime assets they are using.
	prepared, err := s.javsp.prepareConfigDataContext(r.Context(), config, false)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	for index := range prepared {
		prepared[index] = 0
	}
	updated, err := s.db.UpdateJavspConfigIfRevision(r.Context(), config)
	if errors.Is(err, errJavspConfigConflict) {
		writeError(w, http.StatusConflict, err)
		return
	}
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, updated)
}

func (s *Service) startJavsp(ctx context.Context, trigger string) (int64, error) {
	return s.startMovieDiscovery(ctx, trigger)
}
func (s *Service) handleJavspRun(w http.ResponseWriter, r *http.Request) {
	var payload map[string]any
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	id, err := s.startJavsp(r.Context(), "manual")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 202, map[string]int64{"run_id": id})
}

func (s *Service) handleJavspCancel(w http.ResponseWriter, _ *http.Request) {
	if !s.javsp.StopCurrent() {
		writeError(w, 400, fmt.Errorf("当前没有正在运行的 JAVSP 任务"))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

func (s *Service) handleJavspTest(w http.ResponseWriter, r *http.Request) {
	var payload map[string]any
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	if !s.javspLock.TryLock() {
		writeError(w, 400, fmt.Errorf("JAVSP 正在运行，请稍后再检测"))
		return
	}
	defer s.javspLock.Unlock()
	config, err := s.db.JavspConfigPrivate()
	if err != nil {
		writeError(w, 500, err)
		return
	}
	component := s.javsp.Probe(r.Context())
	if available, _ := component["available"].(bool); !available {
		writeError(w, 400, fmt.Errorf("%v", component["error"]))
		return
	}
	prepared, err := s.javsp.PrepareConfigDataContext(r.Context(), config)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	for index := range prepared {
		prepared[index] = 0
	}
	component["config_path"] = s.javsp.ConfigPath()
	component["config_source"] = "database-memory"
	writeJSON(w, 200, component)
}

func (s *Service) handleSettings(w http.ResponseWriter, _ *http.Request) {
	settings, err := s.db.Settings(false)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, settings)
}
func (s *Service) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var values map[string]any
	if err := decodeJSON(w, r, &values); err != nil {
		writeError(w, 400, err)
		return
	}
	if _, ok := values["qb_password_set"]; ok {
		delete(values, "qb_password_set")
	}
	if value, ok := values["qb_password"].(string); ok && value == "__UNCHANGED__" {
		delete(values, "qb_password")
	}
	stringKeys := []string{"qb_host", "qb_username", "qb_password", "global_proxy_url"}
	for _, key := range stringKeys {
		if value, exists := values[key]; exists {
			text, valid := value.(string)
			if !valid || len(text) > 4096 {
				writeError(w, 400, fmt.Errorf("%s 格式无效", key))
				return
			}
		}
	}
	for _, key := range []string{"preview_mode", "qb_verify_certificate", "feed_use_proxy"} {
		if value, exists := values[key]; exists {
			if _, valid := value.(bool); !valid {
				writeError(w, 400, fmt.Errorf("%s 必须是布尔值", key))
				return
			}
		}
	}
	for key, bounds := range map[string][2]int{"qb_port": {1, 65535}, "request_timeout": {5, 120}, "run_retention_count": {1, 100}} {
		if value, exists := values[key]; exists {
			number, valid := value.(float64)
			integer := int(number)
			if !valid || number != float64(integer) || integer < bounds[0] || integer > bounds[1] {
				writeError(w, 400, fmt.Errorf("%s 超出有效范围", key))
				return
			}
			values[key] = integer
		}
	}
	allowed := map[string]bool{"preview_mode": true, "qb_host": true, "qb_port": true, "qb_username": true, "qb_password": true, "qb_verify_certificate": true, "feed_use_proxy": true, "global_proxy_url": true, "request_timeout": true, "run_retention_count": true}
	for key := range values {
		if !allowed[key] {
			writeError(w, 400, fmt.Errorf("不支持的设置项：%s", key))
			return
		}
	}
	if value, ok := values["qb_host"].(string); ok && value != "" {
		if err := validateHTTPURL(value, "qBittorrent 地址"); err != nil {
			writeError(w, 400, err)
			return
		}
	}
	if value, ok := values["global_proxy_url"].(string); ok && value != "" {
		if err := validateHTTPURL(value, "代理地址"); err != nil {
			writeError(w, 400, err)
			return
		}
	}
	updated, err := s.db.UpdateSettings(values)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, updated)
}
func (s *Service) handleTestQB(w http.ResponseWriter, r *http.Request) {
	var values map[string]any
	if err := decodeJSON(w, r, &values); err != nil {
		writeError(w, 400, err)
		return
	}
	current, err := s.db.Settings(true)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	candidate := overlayQBSettings(current, values)
	result, err := s.collector.TestQBittorrent(r.Context(), candidate)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, result)
}

func overlayQBSettings(current Settings, values map[string]any) Settings {
	candidate := current
	if value, ok := values["qb_host"].(string); ok {
		candidate.QBHost = value
	}
	if value, ok := values["qb_port"].(float64); ok && value == float64(int(value)) {
		candidate.QBPort = int(value)
	}
	if value, ok := values["qb_username"].(string); ok {
		candidate.QBUsername = value
	}
	if value, ok := values["qb_password"].(string); ok && value != "" && value != "__UNCHANGED__" {
		candidate.QBPassword = value
	}
	if value, ok := values["qb_verify_certificate"].(bool); ok {
		candidate.QBVerifyCertificate = value
	}
	if value, ok := values["request_timeout"].(float64); ok && value == float64(int(value)) {
		candidate.RequestTimeout = int(value)
	}
	return candidate
}

func (s *Service) handleStatic(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), "/")
	target := filepath.Join(s.publicDir, path)
	relative, err := filepath.Rel(s.publicDir, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		writeError(w, 404, fmt.Errorf("资源不存在"))
		return
	}
	if path != "" {
		if info, statErr := os.Stat(target); statErr == nil && !info.IsDir() {
			if !pathWithinRoot(s.publicDir, target) {
				writeError(w, 404, fmt.Errorf("资源不存在"))
				return
			}
			http.ServeFile(w, r, target)
			return
		}
	}
	index := filepath.Join(s.publicDir, "index.html")
	if _, err := os.Stat(index); err != nil || !pathWithinRoot(s.publicDir, index) {
		writeError(w, 503, fmt.Errorf("前端资源尚未构建"))
		return
	}
	http.ServeFile(w, r, index)
}

func pathWithinRoot(root, target string) bool {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedTarget)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (s *Service) scheduler(ctx context.Context) {
	defer s.wait.Done()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ids, err := s.db.DueTaskIDs()
			if err != nil {
				slog.Error("find due tasks", "error", err)
				continue
			}
			for _, id := range ids {
				if err := s.startTask(ctx, id, "schedule", nil); err != nil && !errors.Is(err, errRSSBusy) && !errors.Is(err, errTaskBusy) {
					slog.Error("schedule RSS task", "error", err, "task_id", id)
				}
			}
			due, err := s.db.JavspDue()
			if err != nil {
				slog.Error("check javsp schedule", "error", err)
			} else if due {
				if _, err := s.startJavsp(ctx, "schedule"); err != nil {
					slog.Warn("start scheduled javsp", "error", err)
				}
			}
			maintenanceDue, err := s.db.MaintenanceDue()
			if err != nil {
				slog.Error("check media maintenance schedule", "error", err)
			} else if maintenanceDue {
				if err := s.runScheduledMaintenance(); err != nil && !errors.Is(err, errMaintenanceBusy) {
					slog.Warn("run scheduled media cleanup", "error", err)
				}
			}
			intakeDue, err := s.db.IntakeDue()
			if err != nil {
				slog.Error("check download intake schedule", "error", err)
			} else if intakeDue {
				if _, err := s.runIntake(ctx, "schedule"); err != nil && !errors.Is(err, errIntakeBusy) {
					slog.Warn("run scheduled download intake", "error", err)
				}
			}
		}
	}
}

// Start activates the scheduler and queue workers without opening another
// listener. It is used when the media service is hosted in a larger process.
func (s *Service) Start(ctx context.Context) error {
	if s.cancel != nil {
		return errors.New("media service already started")
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.runCtx = runCtx
	s.wait.Add(1)
	go s.scheduler(runCtx)
	s.wait.Add(1)
	go func() { defer s.wait.Done(); s.queueWorker(runCtx, 2*time.Second) }()
	s.wait.Add(1)
	go func() { defer s.wait.Done(); s.rssQueueWorker(runCtx, 2*time.Second) }()
	s.wait.Add(1)
	go func() { defer s.wait.Done(); s.downloadQueueWorker(runCtx) }()
	s.wait.Add(1)
	go func() { defer s.wait.Done(); s.catalogScanWorker(runCtx) }()
	return nil
}

func (s *Service) Run(ctx context.Context, listener net.Listener) error {
	if err := s.Start(ctx); err != nil {
		return err
	}
	s.server = &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 130 * time.Second, WriteTimeout: 130 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutdownCancel()
		if err := s.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("shutdown", "error", err)
		}
	}()
	err := s.server.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutdownCancel()
		_ = s.Shutdown(shutdownCtx)
	}
	<-s.shutdownDone
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	if err != nil {
		return err
	}
	return s.shutdownErr
}

func (s *Service) Shutdown(ctx context.Context) error {
	s.shutdownOnce.Do(func() {
		// A caller's wait deadline must not consume sync.Once and abandon the
		// remaining cleanup. The single shutdown operation continues in order.
		go func() {
			defer close(s.shutdownDone)
			if s.cancel != nil {
				s.cancel()
			}
			s.javsp.StopCurrent()
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cleanupErr := s.closePlayback(cleanupCtx)
			if s.server != nil {
				if err := s.server.Shutdown(cleanupCtx); err != nil {
					cleanupErr = errors.Join(cleanupErr, err, s.server.Close())
				}
			}
			s.wait.Wait()
			s.shutdownErr = errors.Join(cleanupErr, s.db.Close())
		}()
	})
	select {
	case <-s.shutdownDone:
		return s.shutdownErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
