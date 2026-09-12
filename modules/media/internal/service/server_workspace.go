package service

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type pipelineStage struct {
	Key       string  `json:"key"`
	Label     string  `json:"label"`
	Status    string  `json:"status"`
	Summary   string  `json:"summary"`
	Detail    string  `json:"detail"`
	Count     int     `json:"count"`
	NextRunAt *string `json:"next_run_at,omitempty"`
	Error     string  `json:"error,omitempty"`
}

type pipelinePaths struct {
	RSSDownloadPaths   []string `json:"rss_download_paths"`
	IntakeSource       string   `json:"intake_source"`
	IntakeDestination  string   `json:"intake_destination"`
	JavspInput         string   `json:"javsp_input"`
	JavspOutputPattern string   `json:"javsp_output_pattern"`
	Consistent         bool     `json:"consistent"`
	Issues             []string `json:"issues"`
	Warnings           []string `json:"warnings"`
}

type unifiedRun struct {
	ID          string         `json:"id"`
	Module      string         `json:"module"`
	ModuleLabel string         `json:"module_label"`
	TaskName    string         `json:"task_name"`
	TriggerType string         `json:"trigger_type"`
	Status      string         `json:"status"`
	StartedAt   string         `json:"started_at"`
	FinishedAt  any            `json:"finished_at"`
	Summary     string         `json:"summary"`
	Metrics     map[string]any `json:"metrics"`
	Details     any            `json:"details"`
	Error       string         `json:"error"`
	DryRun      *bool          `json:"dry_run,omitempty"`
}

func configuredPathWithin(root, target string) bool {
	root = filepath.Clean(strings.TrimSpace(root))
	target = filepath.Clean(strings.TrimSpace(target))
	if root == "." || target == "." || !filepath.IsAbs(root) || !filepath.IsAbs(target) {
		return false
	}
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func earliestTaskNextRun(tasks []Task) *string {
	values := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if task.Enabled && task.NextRunAt != nil && *task.NextRunAt != "" {
			values = append(values, *task.NextRunAt)
		}
	}
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	return &values[0]
}

func buildPipelinePaths(tasks []Task, intake IntakeConfig, javsp JavspConfig) pipelinePaths {
	paths := pipelinePaths{
		RSSDownloadPaths: []string{}, IntakeSource: intake.SourceDirectory,
		IntakeDestination: intake.DestinationDirectory, JavspInput: javsp.InputDirectory,
		JavspOutputPattern: javsp.OutputFolderPattern, Consistent: true, Issues: []string{}, Warnings: []string{},
	}
	seen := map[string]bool{}
	for _, task := range tasks {
		path := filepath.Clean(rssDownloadPath(task, intake))
		if path == "." || path == "" || seen[path] {
			continue
		}
		seen[path] = true
		paths.RSSDownloadPaths = append(paths.RSSDownloadPaths, path)
		if !configuredPathWithin(intake.SourceDirectory, path) {
			paths.Issues = append(paths.Issues, fmt.Sprintf("RSS 任务“%s”的下载目录不在入库来源目录内", task.Name))
		}
	}
	if filepath.Clean(intake.DestinationDirectory) != filepath.Clean(javsp.InputDirectory) {
		paths.Issues = append(paths.Issues, "入库目标目录与 JAVSP 扫描目录不一致")
	}
	if len(paths.RSSDownloadPaths) == 0 {
		paths.Warnings = append(paths.Warnings, "RSS 任务使用 qBittorrent 默认下载目录，无法自动核对入库来源")
	}
	paths.Consistent = len(paths.Issues) == 0
	return paths
}

func asInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func asString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func normalizeRSSRun(item map[string]any) unifiedRun {
	dry, _ := item["dry_run"].(bool)
	return unifiedRun{
		ID: fmt.Sprintf("rss-%v", item["id"]), Module: "rss", ModuleLabel: "RSS 采集",
		TaskName: asString(item["task_name"]), TriggerType: asString(item["trigger_type"]),
		Status: asString(item["status"]), StartedAt: asString(item["started_at"]), FinishedAt: item["finished_at"],
		Summary: fmt.Sprintf("条目 %d · 匹配 %d · 投递 %d · 失败 %d", asInt(item["entries_count"]), asInt(item["matched_count"]), asInt(item["submitted_count"]), asInt(item["failed_count"])),
		Metrics: map[string]any{"entries": item["entries_count"], "matched": item["matched_count"], "submitted": item["submitted_count"], "skipped": item["skipped_count"], "failed": item["failed_count"]},
		Details: item["details"], Error: asString(item["error"]), DryRun: &dry,
	}
}

func normalizeJavspRun(item map[string]any) unifiedRun {
	summary := "JAVSP 已完成"
	if item["exit_code"] != nil {
		summary = fmt.Sprintf("退出码 %v", item["exit_code"])
	}
	return unifiedRun{
		ID: fmt.Sprintf("javsp-%v", item["id"]), Module: "javsp", ModuleLabel: "JAVSP 整理", TaskName: "影片整理",
		TriggerType: asString(item["trigger_type"]), Status: asString(item["status"]), StartedAt: asString(item["started_at"]), FinishedAt: item["finished_at"],
		Summary: summary, Metrics: map[string]any{"exit_code": item["exit_code"]}, Details: item["output"], Error: asString(item["error"]),
	}
}

func maintenanceModule(kind string) (string, string, string) {
	switch kind {
	case "intake":
		return "intake", "下载入库", "完成下载入库"
	case "anime_titles":
		return "anime", "番剧助手", "更新 AniDB 标题库"
	case "anime_scan":
		return "anime", "番剧助手", "扫描番剧"
	case "anime_nfo":
		return "anime", "番剧助手", "生成番剧 NFO"
	case "duplicates":
		return "maintenance", "媒体维护", "重复视频扫描"
	case "duplicate_delete":
		return "maintenance", "媒体维护", "删除重复视频"
	case "empty_directories":
		return "maintenance", "媒体维护", "空目录清理"
	default:
		return "maintenance", "媒体维护", kind
	}
}

func normalizeMaintenanceRun(item map[string]any) unifiedRun {
	kind := asString(item["kind"])
	module, label, task := maintenanceModule(kind)
	return unifiedRun{
		ID: fmt.Sprintf("%s-%v", kind, item["id"]), Module: module, ModuleLabel: label, TaskName: task,
		TriggerType: asString(item["trigger_type"]), Status: asString(item["status"]), StartedAt: asString(item["started_at"]), FinishedAt: item["finished_at"],
		Summary: fmt.Sprintf("扫描 %d · 发现 %d · 处理 %d", asInt(item["scanned_count"]), asInt(item["found_count"]), asInt(item["action_count"])),
		Metrics: map[string]any{"scanned": item["scanned_count"], "found": item["found_count"], "action": item["action_count"]},
		Details: item["details"], Error: asString(item["error"]),
	}
}

func (s *Service) unifiedRuns(limit int) ([]unifiedRun, error) {
	items, err := s.allUnifiedRuns()
	if err != nil {
		return nil, err
	}
	limit = clamp(limit, 1, 200)
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *Service) allUnifiedRuns() ([]unifiedRun, error) {
	rss, err := s.db.ListRuns(100)
	if err != nil {
		return nil, err
	}
	javsp, err := s.db.ListJavspRuns(200)
	if err != nil {
		return nil, err
	}
	maintenance, err := s.db.ListMaintenanceRuns(1000)
	if err != nil {
		return nil, err
	}
	items := make([]unifiedRun, 0, len(rss)+len(javsp)+len(maintenance))
	for _, item := range rss {
		items = append(items, normalizeRSSRun(item))
	}
	for _, item := range javsp {
		items = append(items, normalizeJavspRun(item))
	}
	for _, item := range maintenance {
		items = append(items, normalizeMaintenanceRun(item))
	}
	sort.SliceStable(items, func(left, right int) bool { return items[left].StartedAt > items[right].StartedAt })
	return items, nil
}

func (s *Service) handleUnifiedRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 {
		limit = 100
	}
	items, err := s.allUnifiedRuns()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	module := strings.TrimSpace(r.URL.Query().Get("module"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	trigger := strings.TrimSpace(r.URL.Query().Get("trigger"))
	filtered := make([]unifiedRun, 0, min(limit, len(items)))
	for _, item := range items {
		if module != "" && item.Module != module || status != "" && item.Status != status || trigger != "" && item.TriggerType != trigger {
			continue
		}
		filtered = append(filtered, item)
		if len(filtered) == clamp(limit, 1, 200) {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": filtered})
}

func (s *Service) handlePipeline(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.db.Tasks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	intake, err := s.db.IntakeConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	javsp, err := s.db.JavspConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	settings, err := s.db.Settings(true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	recent, err := s.unifiedRuns(8)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	enabledRSS := 0
	for _, task := range tasks {
		if task.Enabled {
			enabledRSS++
		}
	}
	rssStatus := "ok"
	if enabledRSS == 0 {
		rssStatus = "idle"
	}
	stages := []pipelineStage{{Key: "rss", Label: "RSS 发现", Status: rssStatus, Summary: fmt.Sprintf("%d 个订阅，%d 个启用", len(tasks), enabledRSS), Detail: "按筛选规则发现影片并投递下载", Count: enabledRSS, NextRunAt: earliestTaskNextRun(tasks)}}

	qbStage := pipelineStage{Key: "qb", Label: "qBittorrent 下载", Status: "warning", Summary: "尚未连接", Detail: "等待检测下载状态"}
	intakeStage := pipelineStage{Key: "intake", Label: "完成入库", Status: "idle", Summary: "0 个待入库", Detail: "只处理 100% 完成任务", NextRunAt: intake.NextRunAt}
	qbCtx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if session, openErr := s.collector.OpenQBSession(qbCtx, settings); openErr != nil {
		qbStage.Error = openErr.Error()
		qbStage.Detail = "请在系统设置中检查 qBittorrent 连接"
		intakeStage.Status = "warning"
		intakeStage.Detail = "qBittorrent 不可用，无法检查候选"
	} else {
		defer session.Close()
		torrents, torrentErr := session.Torrents(qbCtx)
		if torrentErr != nil {
			qbStage.Status = "error"
			qbStage.Error = torrentErr.Error()
			intakeStage.Status = "warning"
			intakeStage.Detail = "读取下载任务失败"
		} else {
			completed := 0
			errorsCount := 0
			for _, torrent := range torrents {
				if torrentCompleted(torrent) {
					completed++
				}
				switch strings.ToLower(torrent.State) {
				case "error", "missingfiles", "unknown":
					errorsCount++
				}
			}
			qbStage.Status = "ok"
			if errorsCount > 0 {
				qbStage.Status = "warning"
			}
			qbStage.Count = len(torrents)
			qbStage.Summary = fmt.Sprintf("%d 个任务，%d 个已完成", len(torrents), completed)
			qbStage.Detail = fmt.Sprintf("下载中 %d · 异常 %d", len(torrents)-completed-errorsCount, errorsCount)
			candidates, candidateErr := buildIntakeCandidates(torrents, intake.SourceDirectory, intake.DestinationDirectory)
			if candidateErr != nil {
				intakeStage.Status = "error"
				intakeStage.Error = candidateErr.Error()
			} else {
				intakeStage.Count = len(candidates)
				intakeStage.Status = "ok"
				if len(candidates) == 0 {
					intakeStage.Status = "idle"
				}
				intakeStage.Summary = fmt.Sprintf("%d 个待入库", len(candidates))
				intakeStage.Detail = fmt.Sprintf("自动入库%s", map[bool]string{true: "已启用", false: "已关闭"}[intake.Enabled])
			}
		}
	}
	stages = append(stages, qbStage, intakeStage)

	javspStatus := "ok"
	javspRunning := !s.javspLock.TryLock()
	if !javspRunning {
		s.javspLock.Unlock()
	}
	component := s.javsp.Component(r.Context())
	if available, _ := component["available"].(bool); !available {
		javspStatus = "error"
	}
	if javspRunning {
		javspStatus = "running"
	}
	javspSummary := "等待整理"
	if javspRunning {
		javspSummary = "正在整理"
	}
	stages = append(stages, pipelineStage{Key: "javsp", Label: "JAVSP 整理", Status: javspStatus, Summary: javspSummary, Detail: fmt.Sprintf("自动整理%s", map[bool]string{true: "已启用", false: "已关闭"}[javsp.Enabled]), NextRunAt: javsp.NextRunAt})

	writeJSON(w, http.StatusOK, map[string]any{
		"stages": stages, "paths": buildPipelinePaths(tasks, intake, javsp), "recent_runs": recent,
		"service": map[string]any{"ok": true, "version": Version},
	})
}
