package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wayyoungboy/fnos-music-converter/internal/converter"
)

type Options struct {
	DataDir     string
	PublicDir   string
	Prefix      string
	BrowseRoots []string
	Engine      Engine
	LocalDev    bool
}

type Service struct {
	mu          sync.RWMutex
	dataDir     string
	statePath   string
	publicDir   string
	prefix      string
	browseRoots []string
	engine      Engine
	localDev    bool
	settings    Settings
	runs        []Run
	current     *Job
	cancel      context.CancelFunc
	handler     http.Handler
	wait        sync.WaitGroup
	closed      bool
}

func New(options Options) (*Service, error) {
	if options.DataDir == "" || !filepath.IsAbs(options.DataDir) {
		return nil, errors.New("data directory must be absolute")
	}
	if options.PublicDir == "" || !filepath.IsAbs(options.PublicDir) {
		return nil, errors.New("public directory must be absolute")
	}
	if err := os.MkdirAll(options.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	if options.Engine == nil {
		options.Engine = converter.NewNCMEngine()
	}
	roots, err := canonicalRoots(options.BrowseRoots)
	if err != nil {
		return nil, err
	}
	prefix := strings.TrimSuffix(options.Prefix, "/")
	if prefix == "" {
		prefix = "/app/music_converter"
	}
	service := &Service{
		dataDir:     filepath.Clean(options.DataDir),
		statePath:   filepath.Join(options.DataDir, "state.json"),
		publicDir:   filepath.Clean(options.PublicDir),
		prefix:      prefix,
		browseRoots: roots,
		engine:      options.Engine,
		localDev:    options.LocalDev,
		settings:    defaultSettings(),
		runs:        []Run{},
	}
	if err := service.load(); err != nil {
		return nil, err
	}
	service.handler = service.routes()
	return service, nil
}

func (service *Service) Handler() http.Handler {
	return service.handler
}

func (service *Service) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", service.handleHealth)
	mux.HandleFunc("GET /api/about", service.handleAbout)
	mux.HandleFunc("GET /api/settings", service.handleSettings)
	mux.HandleFunc("PUT /api/settings", service.handleSettings)
	mux.HandleFunc("GET /api/directories", service.handleDirectories)
	mux.HandleFunc("POST /api/scan", service.handleScan)
	mux.HandleFunc("POST /api/jobs", service.handleStartJob)
	mux.HandleFunc("GET /api/jobs/current", service.handleCurrentJob)
	mux.HandleFunc("POST /api/jobs/current/cancel", service.handleCancelJob)
	mux.HandleFunc("GET /api/runs", service.handleRuns)
	mux.HandleFunc("DELETE /api/runs", service.handleRuns)
	mux.HandleFunc("/", service.handleFrontend)

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if service.prefix != "" {
			if request.URL.Path == service.prefix {
				http.Redirect(writer, request, service.prefix+"/", http.StatusPermanentRedirect)
				return
			} else if strings.HasPrefix(request.URL.Path, service.prefix+"/") {
				request.URL.Path = strings.TrimPrefix(request.URL.Path, service.prefix)
			}
		}
		if request.Method != http.MethodGet && !service.localDev && !strings.EqualFold(request.Header.Get("X-Trim-Isadmin"), "true") {
			writeError(writer, http.StatusForbidden, "需要飞牛管理员权限")
			return
		}
		mux.ServeHTTP(writer, request)
	})
}

func (service *Service) handleHealth(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"app":     AppName,
		"version": Version,
		"status":  "ok",
	})
}

func (service *Service) handleAbout(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"project": map[string]any{
			"name":       "音乐转换中心",
			"repository": "https://github.com/wayyoungboy/fnos-music-converter",
			"version":    Version,
		},
		"dependencies": []map[string]string{
			{
				"name":       "ncmdump-go",
				"role":       "actual_dependency",
				"version":    "v1.7.5",
				"license":    "MIT",
				"repository": "https://git.taurusxin.com/taurusxin/ncmdump-go",
			},
		},
		"references": []map[string]string{
			{"name": "ncmdump-gui", "role": "reference_only", "license": "MIT", "repository": "https://git.taurusxin.com/taurusxin/ncmdump-gui"},
			{"name": "ncmdump", "role": "reference_only", "license": "MIT", "repository": "https://github.com/taurusxin/ncmdump"},
			{"name": "ncmdump-py", "role": "reference_only", "license": "MIT", "repository": "https://github.com/ww-rm/ncmdump-py"},
		},
		"usage": []string{
			"仅处理你合法拥有的本地文件",
			"仅供学习与个人研究使用",
			"不提供下载、账号接入或在线版权规避功能",
			"软件按现状提供，不作任何保证",
			"用途声明不能替代上游许可证或适用法律",
		},
	})
}

func (service *Service) handleSettings(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodGet {
		service.mu.RLock()
		settings := service.settings
		service.mu.RUnlock()
		writeJSON(writer, http.StatusOK, settings)
		return
	}
	var settings Settings
	if err := decodeJSON(request, &settings); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	settings.SourceDir = filepath.Clean(settings.SourceDir)
	settings.OutputDir = filepath.Clean(settings.OutputDir)
	if err := validateSettings(settings); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	service.mu.Lock()
	service.settings = settings
	err := service.saveLocked()
	service.mu.Unlock()
	if err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, settings)
}

func (service *Service) handleScan(writer http.ResponseWriter, request *http.Request) {
	service.mu.RLock()
	settings := service.settings
	service.mu.RUnlock()
	if err := validateSettings(settings); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	items, err := converter.Scan(request.Context(), converter.ScanOptions{SourceDir: settings.SourceDir, OutputDir: settings.OutputDir, Recursive: settings.Recursive, MigrateMP3: settings.MigrateMP3})
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (service *Service) handleStartJob(writer http.ResponseWriter, request *http.Request) {
	service.mu.RLock()
	settings := service.settings
	service.mu.RUnlock()
	if err := validateSettings(settings); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	items, err := converter.Scan(request.Context(), converter.ScanOptions{SourceDir: settings.SourceDir, OutputDir: settings.OutputDir, Recursive: settings.Recursive, MigrateMP3: settings.MigrateMP3})
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		writeError(writer, http.StatusServiceUnavailable, "模块正在关闭")
		return
	}
	if service.current != nil && service.current.Status == "running" {
		service.mu.Unlock()
		writeError(writer, http.StatusConflict, "已有转换任务正在运行")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &Job{
		ID:        strconv.FormatInt(time.Now().UnixNano(), 36),
		Status:    "running",
		StartedAt: time.Now(),
		Total:     len(items),
		Results:   []converter.Result{},
	}
	service.current = job
	service.cancel = cancel
	snapshot := *job
	service.wait.Add(1)
	service.mu.Unlock()

	go func() {
		defer service.wait.Done()
		service.runJob(ctx, settings, items, job.ID)
	}()
	writeJSON(writer, http.StatusAccepted, map[string]any{"job": snapshot})
}

// Shutdown cancels active work and waits for in-process jobs to finish.
func (service *Service) Shutdown(ctx context.Context) error {
	service.mu.Lock()
	service.closed = true
	if service.cancel != nil {
		service.cancel()
	}
	service.mu.Unlock()
	done := make(chan struct{})
	go func() {
		service.wait.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (service *Service) runJob(ctx context.Context, settings Settings, items []converter.Item, jobID string) {
	runner := converter.NewRunner(service.engine)
	results := runner.Run(ctx, converter.RunOptions{
		SourceDir:      settings.SourceDir,
		OutputDir:      settings.OutputDir,
		PreserveTree:   settings.PreserveTree,
		FetchRemoteArt: settings.FetchRemoteArt,
		MigrateMP3:     settings.MigrateMP3,
		Conflict:       converter.ConflictPolicy(settings.Conflict),
		Concurrency:    settings.Concurrency,
	}, items)
	ended := time.Now()
	job := Job{ID: jobID, Status: "completed", StartedAt: ended, EndedAt: &ended, Total: len(results), Results: results}
	service.mu.Lock()
	if service.current != nil && service.current.ID == jobID {
		job.StartedAt = service.current.StartedAt
	}
	for _, result := range results {
		switch result.Status {
		case converter.StatusCompleted:
			job.Completed++
		case converter.StatusSkipped:
			job.Skipped++
		case converter.StatusCanceled:
			job.Canceled++
		default:
			job.Failed++
		}
	}
	if job.Canceled > 0 {
		job.Status = "canceled"
	} else if job.Failed > 0 {
		job.Status = "completed_with_errors"
	}
	service.current = &job
	service.cancel = nil
	service.runs = append([]Run{{
		ID: job.ID, Status: job.Status, StartedAt: job.StartedAt, EndedAt: ended,
		Total: job.Total, Completed: job.Completed, Failed: job.Failed, Skipped: job.Skipped, Canceled: job.Canceled,
	}}, service.runs...)
	if len(service.runs) > settings.RetainRuns {
		service.runs = service.runs[:settings.RetainRuns]
	}
	_ = service.saveLocked()
	service.mu.Unlock()
}

func (service *Service) handleCurrentJob(writer http.ResponseWriter, _ *http.Request) {
	service.mu.RLock()
	defer service.mu.RUnlock()
	if service.current == nil {
		writeError(writer, http.StatusNotFound, "暂无转换任务")
		return
	}
	writeJSON(writer, http.StatusOK, service.current)
}

func (service *Service) handleCancelJob(writer http.ResponseWriter, _ *http.Request) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.current == nil || service.current.Status != "running" || service.cancel == nil {
		writeError(writer, http.StatusConflict, "当前没有正在运行的任务")
		return
	}
	service.cancel()
	writeJSON(writer, http.StatusAccepted, map[string]bool{"canceling": true})
}

func (service *Service) handleRuns(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodDelete {
		service.mu.Lock()
		service.runs = []Run{}
		err := service.saveLocked()
		service.mu.Unlock()
		if err != nil {
			writeError(writer, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(writer, http.StatusOK, map[string]bool{"cleared": true})
		return
	}
	service.mu.RLock()
	runs := append([]Run{}, service.runs...)
	service.mu.RUnlock()
	writeJSON(writer, http.StatusOK, map[string]any{"runs": runs})
}

func (service *Service) handleDirectories(writer http.ResponseWriter, request *http.Request) {
	requested := request.URL.Query().Get("path")
	if requested == "" {
		entries := make([]map[string]string, 0, len(service.browseRoots))
		for _, root := range service.browseRoots {
			entries = append(entries, map[string]string{"name": filepath.Base(root), "path": root})
		}
		writeJSON(writer, http.StatusOK, map[string]any{"current": "", "parent": "", "entries": entries})
		return
	}
	path, root, err := service.authorizedDirectory(requested)
	if err != nil {
		writeError(writer, http.StatusForbidden, err.Error())
		return
	}
	entriesOnDisk, err := os.ReadDir(path)
	if err != nil {
		writeError(writer, http.StatusBadRequest, fmt.Sprintf("读取目录失败: %v", err))
		return
	}
	entries := make([]map[string]string, 0)
	for _, entry := range entriesOnDisk {
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			entries = append(entries, map[string]string{"name": entry.Name(), "path": filepath.Join(path, entry.Name())})
		}
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left]["name"] < entries[right]["name"] })
	parent := filepath.Dir(path)
	if path == root {
		parent = ""
	}
	writeJSON(writer, http.StatusOK, map[string]any{"current": path, "parent": parent, "entries": entries})
}

func (service *Service) authorizedDirectory(requested string) (string, string, error) {
	if !filepath.IsAbs(requested) {
		return "", "", errors.New("目录必须是绝对路径")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(requested))
	if err != nil {
		return "", "", errors.New("目录不存在或不可访问")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", "", errors.New("所选路径不是目录")
	}
	for _, root := range service.browseRoots {
		if isWithin(root, resolved) {
			return resolved, root, nil
		}
	}
	return "", "", errors.New("目录不在应用获授权的存储空间内")
}

func (service *Service) handleFrontend(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writeError(writer, http.StatusMethodNotAllowed, "不支持的请求方法")
		return
	}
	path := strings.TrimPrefix(filepath.Clean(request.URL.Path), string(filepath.Separator))
	if path == "." || path == "" {
		path = "index.html"
	}
	requested := filepath.Join(service.publicDir, path)
	if !isWithin(service.publicDir, requested) {
		writeError(writer, http.StatusForbidden, "静态资源路径无效")
		return
	}
	if info, err := os.Stat(requested); err == nil && !info.IsDir() {
		http.ServeFile(writer, request, requested)
		return
	}
	index := filepath.Join(service.publicDir, "index.html")
	if _, err := os.Stat(index); err != nil {
		writeError(writer, http.StatusNotFound, "前端资源尚未构建")
		return
	}
	http.ServeFile(writer, request, index)
}

func (service *Service) load() error {
	content, err := os.ReadFile(service.statePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	var state persistedState
	if err := json.Unmarshal(content, &state); err != nil {
		return fmt.Errorf("decode state: %w", err)
	}
	if state.Settings.Concurrency != 0 {
		service.settings = state.Settings
	}
	if state.Runs != nil {
		service.runs = state.Runs
	}
	return nil
}

func (service *Service) saveLocked() error {
	content, err := json.MarshalIndent(persistedState{Settings: service.settings, Runs: service.runs}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	temporary := service.statePath + ".tmp"
	if err := os.WriteFile(temporary, content, 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if err := os.Rename(temporary, service.statePath); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}

func validateSettings(settings Settings) error {
	if !filepath.IsAbs(settings.SourceDir) || !filepath.IsAbs(settings.OutputDir) {
		return errors.New("源目录和输出目录必须是绝对路径")
	}
	for label, path := range map[string]string{"源目录": settings.SourceDir, "输出目录": settings.OutputDir} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("%s不存在或不是目录", label)
		}
	}
	if settings.Concurrency < 1 || settings.Concurrency > 8 {
		return errors.New("并发任务数必须在 1 到 8 之间")
	}
	if settings.RetainRuns < 1 || settings.RetainRuns > 1000 {
		return errors.New("运行记录保留量必须在 1 到 1000 之间")
	}
	switch converter.ConflictPolicy(settings.Conflict) {
	case converter.ConflictSkip, converter.ConflictRename, converter.ConflictOverwrite:
		return nil
	default:
		return errors.New("未知的文件冲突策略")
	}
}

func canonicalRoots(roots []string) ([]string, error) {
	canonical := make([]string, 0, len(roots))
	for _, root := range roots {
		if root == "" || !filepath.IsAbs(root) {
			return nil, errors.New("browse roots must be absolute")
		}
		resolved, err := filepath.EvalSymlinks(filepath.Clean(root))
		if err != nil {
			return nil, fmt.Errorf("resolve browse root %s: %w", root, err)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("browse root is not a directory: %s", root)
		}
		canonical = append(canonical, resolved)
	}
	return canonical, nil
}

func isWithin(root, path string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func decodeJSON(request *http.Request, destination any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("请求内容无效: %w", err)
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]string{"error": message})
}
