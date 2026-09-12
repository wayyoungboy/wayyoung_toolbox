package integrity

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	statusHealthy = "healthy"
	statusWarning = "warning"
	statusDamaged = "damaged"
	statusSkipped = "skipped"
)

type Options struct {
	DataDir     string
	PublicDir   string
	Prefix      string
	BrowseRoots []string
}

type Result struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	Method     string `json:"method"`
	Checksum   string `json:"checksum,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

type Status struct {
	Running    bool      `json:"running"`
	Phase      string    `json:"phase"`
	Mode       string    `json:"mode,omitempty"`
	Root       string    `json:"root,omitempty"`
	Total      int       `json:"total"`
	Checked    int       `json:"checked"`
	Healthy    int       `json:"healthy"`
	Warning    int       `json:"warning"`
	Damaged    int       `json:"damaged"`
	Skipped    int       `json:"skipped"`
	Current    string    `json:"current,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
	ReportJSON string    `json:"report_json,omitempty"`
	ReportCSV  string    `json:"report_csv,omitempty"`
}

type Module struct {
	options Options
	handler http.Handler

	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.RWMutex
	status     Status
	results    []Result
	scanCancel context.CancelFunc
	wg         sync.WaitGroup
}

func New(options Options) (*Module, error) {
	if !filepath.IsAbs(options.DataDir) || !filepath.IsAbs(options.PublicDir) {
		return nil, errors.New("模块目录必须是绝对路径")
	}
	if !strings.HasPrefix(options.Prefix, "/app/") {
		return nil, errors.New("模块前缀无效")
	}
	if info, err := os.Stat(filepath.Join(options.PublicDir, "index.html")); err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("文件完整性模块页面不可用")
	}
	if err := os.MkdirAll(filepath.Join(options.DataDir, "reports"), 0o700); err != nil {
		return nil, err
	}
	roots := make([]string, 0, len(options.BrowseRoots))
	for _, root := range options.BrowseRoots {
		canonical, err := filepath.EvalSymlinks(filepath.Clean(root))
		if err == nil {
			if info, statErr := os.Stat(canonical); statErr == nil && info.IsDir() {
				roots = append(roots, canonical)
			}
		}
	}
	if len(roots) == 0 {
		return nil, errors.New("没有可用于检查的存储卷")
	}
	options.BrowseRoots = roots
	ctx, cancel := context.WithCancel(context.Background())
	module := &Module{options: options, ctx: ctx, cancel: cancel, status: Status{Phase: "idle"}}
	module.handler = module.routes()
	return module, nil
}

func (module *Module) Handler() http.Handler { return module.handler }

func (module *Module) Close(ctx context.Context) error {
	module.cancel()
	module.mu.Lock()
	if module.scanCancel != nil {
		module.scanCancel()
	}
	module.mu.Unlock()
	done := make(chan struct{})
	go func() { module.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (module *Module) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", module.getConfig)
	mux.HandleFunc("GET /api/status", module.getStatus)
	mux.HandleFunc("GET /api/results", module.getResults)
	mux.HandleFunc("GET /api/report", module.getReport)
	mux.HandleFunc("POST /api/scans", module.startScan)
	mux.HandleFunc("DELETE /api/scans/current", module.cancelScan)
	mux.Handle("/", http.FileServer(http.Dir(module.options.PublicDir)))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != module.options.Prefix && !strings.HasPrefix(request.URL.Path, module.options.Prefix+"/") {
			http.NotFound(writer, request)
			return
		}
		copy := request.Clone(request.Context())
		copy.URL.Path = strings.TrimPrefix(request.URL.Path, module.options.Prefix)
		if copy.URL.Path == "" {
			copy.URL.Path = "/"
		}
		copy.URL.RawPath = ""
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(writer, copy)
	})
}

func (module *Module) getConfig(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"roots": module.options.BrowseRoots,
		"modes": []map[string]string{
			{"id": "quick", "name": "快速检查", "description": "读取文件头尾，并按格式检查基础结构。"},
			{"id": "deep", "name": "深度检查", "description": "完整读取每个文件，并调用可用的格式校验器；耗时较长。"},
		},
	})
}

func (module *Module) getStatus(writer http.ResponseWriter, _ *http.Request) {
	module.mu.RLock()
	status := module.status
	module.mu.RUnlock()
	writeJSON(writer, http.StatusOK, status)
}

func (module *Module) getResults(writer http.ResponseWriter, request *http.Request) {
	limit := queryInt(request, "limit", 200, 1, 1000)
	offset := queryInt(request, "offset", 0, 0, 1<<30)
	filter := request.URL.Query().Get("status")
	module.mu.RLock()
	filtered := make([]Result, 0, min(limit, len(module.results)))
	total := 0
	for _, result := range module.results {
		if filter != "" && result.Status != filter {
			continue
		}
		if total >= offset && len(filtered) < limit {
			filtered = append(filtered, result)
		}
		total++
	}
	module.mu.RUnlock()
	writeJSON(writer, http.StatusOK, map[string]any{"results": filtered, "total": total, "offset": offset, "limit": limit})
}

func (module *Module) getReport(writer http.ResponseWriter, request *http.Request) {
	format := request.URL.Query().Get("format")
	module.mu.RLock()
	path := module.status.ReportJSON
	if format == "csv" {
		path = module.status.ReportCSV
	}
	module.mu.RUnlock()
	if path == "" {
		writeError(writer, http.StatusNotFound, "还没有可下载的检查报告")
		return
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		writeError(writer, http.StatusNotFound, "检查报告不存在")
		return
	}
	if format == "csv" {
		writer.Header().Set("Content-Type", "text/csv; charset=utf-8")
	} else {
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	writer.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filepath.Base(path)))
	http.ServeFile(writer, request, path)
}

func (module *Module) startScan(writer http.ResponseWriter, request *http.Request) {
	if !isAdmin(request) {
		writeError(writer, http.StatusForbidden, "需要飞牛管理员权限")
		return
	}
	var payload struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	if payload.Mode == "" {
		payload.Mode = "quick"
	}
	if payload.Mode != "quick" && payload.Mode != "deep" {
		writeError(writer, http.StatusBadRequest, "检查模式无效")
		return
	}
	target, err := resolveTarget(payload.Path, module.options.BrowseRoots)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}

	module.mu.Lock()
	if module.status.Running {
		module.mu.Unlock()
		writeError(writer, http.StatusConflict, "已有检查任务正在运行")
		return
	}
	scanCtx, cancel := context.WithCancel(module.ctx)
	module.scanCancel = cancel
	module.results = nil
	module.status = Status{Running: true, Phase: "discovering", Mode: payload.Mode, Root: target, StartedAt: time.Now()}
	module.wg.Add(1)
	module.mu.Unlock()
	go module.runScan(scanCtx, target, payload.Mode)
	writeJSON(writer, http.StatusAccepted, map[string]string{"message": "检查任务已启动"})
}

func (module *Module) cancelScan(writer http.ResponseWriter, request *http.Request) {
	if !isAdmin(request) {
		writeError(writer, http.StatusForbidden, "需要飞牛管理员权限")
		return
	}
	module.mu.Lock()
	if !module.status.Running || module.scanCancel == nil {
		module.mu.Unlock()
		writeError(writer, http.StatusConflict, "当前没有运行中的检查任务")
		return
	}
	module.scanCancel()
	module.mu.Unlock()
	writeJSON(writer, http.StatusAccepted, map[string]string{"message": "正在停止检查任务"})
}

func (module *Module) runScan(ctx context.Context, target, mode string) {
	defer module.wg.Done()
	files, discoveryResults, discoveryErr := discoverFiles(ctx, target)
	module.mu.Lock()
	module.results = append(module.results, discoveryResults...)
	for _, result := range discoveryResults {
		module.addCountLocked(result.Status)
	}
	module.status.Total = len(files) + len(discoveryResults)
	module.status.Checked = len(discoveryResults)
	module.status.Phase = "checking"
	module.mu.Unlock()

	if discoveryErr == nil {
		for _, path := range files {
			select {
			case <-ctx.Done():
				discoveryErr = ctx.Err()
			default:
			}
			if discoveryErr != nil {
				break
			}
			module.mu.Lock()
			module.status.Current = path
			module.mu.Unlock()
			result := checkFile(ctx, path, mode)
			module.mu.Lock()
			module.results = append(module.results, result)
			module.status.Checked++
			module.addCountLocked(result.Status)
			module.mu.Unlock()
		}
	}
	if discoveryErr == nil && ctx.Err() != nil {
		discoveryErr = ctx.Err()
	}

	module.mu.Lock()
	finalStatus := module.status
	finalStatus.Running = false
	finalStatus.Current = ""
	finalStatus.FinishedAt = time.Now()
	if errors.Is(discoveryErr, context.Canceled) {
		finalStatus.Phase = "cancelled"
		finalStatus.LastError = "检查已取消"
	} else if discoveryErr != nil {
		finalStatus.Phase = "failed"
		finalStatus.LastError = discoveryErr.Error()
	} else {
		finalStatus.Phase = "completed"
	}
	results := append([]Result(nil), module.results...)
	module.mu.Unlock()

	jsonPath, csvPath, err := module.persistReport(finalStatus, results)
	module.mu.Lock()
	module.status = finalStatus
	module.scanCancel = nil
	if err != nil {
		if module.status.LastError == "" {
			module.status.LastError = "保存报告失败：" + err.Error()
		}
	} else {
		module.status.ReportJSON, module.status.ReportCSV = jsonPath, csvPath
	}
	module.mu.Unlock()
}

func (module *Module) addCountLocked(status string) {
	switch status {
	case statusHealthy:
		module.status.Healthy++
	case statusWarning:
		module.status.Warning++
	case statusDamaged:
		module.status.Damaged++
	case statusSkipped:
		module.status.Skipped++
	}
}

func (module *Module) persistReport(status Status, results []Result) (string, string, error) {
	reports := filepath.Join(module.options.DataDir, "reports")
	stamp := status.StartedAt.Format("20060102_150405")
	base := filepath.Join(reports, "integrity_"+stamp)
	jsonPath, csvPath := base+".json", base+".csv"
	payload := struct {
		Summary Status   `json:"summary"`
		Results []Result `json:"results"`
	}{status, results}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", "", err
	}
	data = append(data, '\n')
	if err := atomicWrite(jsonPath, data); err != nil {
		return "", "", err
	}

	temporary, err := os.CreateTemp(reports, ".integrity-*.csv")
	if err != nil {
		return "", "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	writer := csv.NewWriter(temporary)
	_ = writer.Write([]string{"状态", "类型", "大小", "耗时毫秒", "路径", "说明", "检查方式", "SHA-256"})
	for _, result := range results {
		_ = writer.Write([]string{result.Status, result.Kind, strconv.FormatInt(result.Size, 10), strconv.FormatInt(result.DurationMS, 10), result.Path, result.Message, result.Method, result.Checksum})
	}
	writer.Flush()
	if err := writer.Error(); err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", "", err
	}
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return "", "", err
	}
	if err := os.Rename(temporaryPath, csvPath); err != nil {
		return "", "", err
	}
	return jsonPath, csvPath, nil
}

func atomicWrite(path string, data []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".integrity-*.json")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func resolveTarget(requested string, roots []string) (string, error) {
	requested = strings.TrimSpace(requested)
	if !filepath.IsAbs(requested) {
		return "", errors.New("请输入 NAS 绝对路径")
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(requested))
	if err != nil {
		return "", fmt.Errorf("路径不可访问：%w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("路径不可访问：%w", err)
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return "", errors.New("只能检查普通文件或文件夹")
	}
	for _, root := range roots {
		canonicalRoot, rootErr := filepath.EvalSymlinks(filepath.Clean(root))
		if rootErr != nil {
			continue
		}
		relative, relErr := filepath.Rel(canonicalRoot, canonical)
		if relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return canonical, nil
		}
	}
	return "", errors.New("路径不在应用可访问的存储卷内")
}

func discoverFiles(ctx context.Context, target string) ([]string, []Result, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, nil, err
	}
	if info.Mode().IsRegular() {
		return []string{target}, nil, nil
	}
	files := make([]string, 0, 256)
	issues := make([]Result, 0)
	err = filepath.WalkDir(target, func(path string, entry os.DirEntry, walkErr error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if walkErr != nil {
			issues = append(issues, Result{Path: path, Kind: "filesystem", Status: statusDamaged, Message: walkErr.Error(), Method: "目录遍历"})
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			issues = append(issues, Result{Path: path, Kind: "symlink", Status: statusSkipped, Message: "符号链接未跟随", Method: "安全路径检查"})
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() {
			files = append(files, path)
			return nil
		}
		if !entry.IsDir() {
			issues = append(issues, Result{Path: path, Kind: "special", Status: statusSkipped, Message: "不是普通文件", Method: "文件类型检查"})
		}
		return nil
	})
	return files, issues, err
}

func queryInt(request *http.Request, name string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(request.URL.Query().Get(name))
	if err != nil || value < minimum {
		return fallback
	}
	return min(value, maximum)
}

func isAdmin(request *http.Request) bool {
	return strings.EqualFold(request.Header.Get("X-Trim-Isadmin"), "true")
}

func decodeJSON(request *http.Request, target any) error {
	defer request.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("请求内容无效")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("请求内容无效")
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
