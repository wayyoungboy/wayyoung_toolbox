package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const Version = "0.1.8"

var (
	timePattern = regexp.MustCompile(`^(?:[01]\d|2[0-3]):[0-5]\d$`)
	sizePattern = regexp.MustCompile(`(?i)^\d+(?:\.\d+)?\s*(?:B|KB|KiB|MB|MiB|GB|GiB|TB|TiB)?$`)
)

type Task struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	FeedURL         string   `json:"feed_url"`
	Enabled         bool     `json:"enabled"`
	ScheduleType    string   `json:"schedule_type"`
	ScheduleMinutes int      `json:"schedule_minutes"`
	ScheduleTime    string   `json:"schedule_time"`
	IncludeAll      []string `json:"include_all"`
	IncludeAny      []string `json:"include_any"`
	ExcludeAny      []string `json:"exclude_any"`
	Extractor       string   `json:"extractor"`
	ProxyURL        string   `json:"proxy_url"`
	QBCategory      string   `json:"qb_category"`
	SavePath        string   `json:"save_path"`
	LastRunAt       *string  `json:"last_run_at"`
	NextRunAt       *string  `json:"next_run_at"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
}

type Settings struct {
	PreviewMode         bool   `json:"preview_mode"`
	QBHost              string `json:"qb_host"`
	QBPort              int    `json:"qb_port"`
	QBUsername          string `json:"qb_username"`
	QBPassword          string `json:"qb_password,omitempty"`
	QBPasswordSet       bool   `json:"qb_password_set,omitempty"`
	QBVerifyCertificate bool   `json:"qb_verify_certificate"`
	FeedUseProxy        bool   `json:"feed_use_proxy"`
	GlobalProxyURL      string `json:"global_proxy_url"`
	RequestTimeout      int    `json:"request_timeout"`
	RunRetentionCount   int    `json:"run_retention_count"`
}

type AnimeConfig struct {
	Directory string `json:"directory"`
}

func defaultAnimeConfig() AnimeConfig {
	return AnimeConfig{}
}

type MaintenanceConfig struct {
	RootDirectory           string  `json:"root_directory"`
	CleanupEnabled          bool    `json:"cleanup_enabled"`
	DuplicateCleanupEnabled bool    `json:"duplicate_cleanup_enabled"`
	OrphanCleanupEnabled    bool    `json:"orphan_cleanup_enabled"`
	ScheduleType            string  `json:"schedule_type"`
	ScheduleMinutes         int     `json:"schedule_minutes"`
	ScheduleTime            string  `json:"schedule_time"`
	MinimumAgeHours         int     `json:"minimum_age_hours"`
	LastRunAt               *string `json:"last_run_at"`
	NextRunAt               *string `json:"next_run_at"`
}

type IntakeConfig struct {
	Revision             int64   `json:"revision"`
	Enabled              bool    `json:"enabled"`
	SourceDirectory      string  `json:"source_directory"`
	DestinationDirectory string  `json:"destination_directory"`
	ScheduleMinutes      int     `json:"schedule_minutes"`
	RunJavsp             bool    `json:"run_javsp"`
	LastRunAt            *string `json:"last_run_at"`
	NextRunAt            *string `json:"next_run_at"`
}

func defaultIntakeConfig() IntakeConfig {
	return IntakeConfig{
		ScheduleMinutes: 15,
		RunJavsp:        true,
	}
}

func defaultMaintenanceConfig() MaintenanceConfig {
	return MaintenanceConfig{
		ScheduleType:    "daily",
		ScheduleMinutes: 1440,
		ScheduleTime:    "05:00",
		MinimumAgeHours: 24,
	}
}

type JavspConfig struct {
	Revision                    int64             `json:"revision" yaml:"-"`
	Enabled                     bool              `json:"enabled" yaml:"-"`
	ScheduleType                string            `json:"schedule_type" yaml:"-"`
	ScheduleMinutes             int               `json:"schedule_minutes" yaml:"-"`
	ScheduleTime                string            `json:"schedule_time" yaml:"-"`
	InputDirectory              string            `json:"input_directory" yaml:"-"`
	OutputDirectory             string            `json:"output_directory" yaml:"-"`
	IgnoredIDPatterns           []string          `json:"ignored_id_patterns" yaml:"-"`
	FilenameExtensions          []string          `json:"filename_extensions" yaml:"-"`
	IgnoredFolderPatterns       []string          `json:"ignored_folder_patterns" yaml:"-"`
	MinimumSize                 string            `json:"minimum_size" yaml:"-"`
	SkipNFODir                  bool              `json:"skip_nfo_dir" yaml:"-"`
	ProxyServer                 string            `json:"proxy_server" yaml:"-"`
	ProxyFree                   map[string]string `json:"proxy_free" yaml:"-"`
	NetworkRetry                int               `json:"network_retry" yaml:"-"`
	NetworkTimeout              string            `json:"network_timeout" yaml:"-"`
	NormalCrawlers              []string          `json:"normal_crawlers" yaml:"-"`
	FC2Crawlers                 []string          `json:"fc2_crawlers" yaml:"-"`
	CIDCrawlers                 []string          `json:"cid_crawlers" yaml:"-"`
	GetchuCrawlers              []string          `json:"getchu_crawlers" yaml:"-"`
	GyuttoCrawlers              []string          `json:"gyutto_crawlers" yaml:"-"`
	RequiredKeys                []string          `json:"required_keys" yaml:"-"`
	MovieConcurrency            int               `json:"movie_concurrency" yaml:"-"`
	Hardworking                 bool              `json:"hardworking" yaml:"-"`
	RespectSiteAVID             bool              `json:"respect_site_avid" yaml:"-"`
	FC2FanLocalPath             string            `json:"fc2fan_local_path" yaml:"-"`
	SleepAfterScraping          string            `json:"sleep_after_scraping" yaml:"-"`
	UseJavDBCover               string            `json:"use_javdb_cover" yaml:"-"`
	NormalizeActressName        bool              `json:"normalize_actress_name" yaml:"-"`
	MoveFiles                   bool              `json:"move_files" yaml:"-"`
	OutputFolderPattern         string            `json:"output_folder_pattern" yaml:"-"`
	PathBasenamePattern         string            `json:"path_basename_pattern" yaml:"-"`
	PathLengthMaximum           int               `json:"path_length_maximum" yaml:"-"`
	PathLengthByByte            bool              `json:"path_length_by_byte" yaml:"-"`
	PathMaxActressCount         int               `json:"path_max_actress_count" yaml:"-"`
	PathHardLink                bool              `json:"path_hard_link" yaml:"-"`
	RemoveTrailingActorName     bool              `json:"remove_trailing_actor_name" yaml:"-"`
	DefaultTitle                string            `json:"default_title" yaml:"-"`
	DefaultActress              string            `json:"default_actress" yaml:"-"`
	DefaultSeries               string            `json:"default_series" yaml:"-"`
	DefaultDirector             string            `json:"default_director" yaml:"-"`
	DefaultProducer             string            `json:"default_producer" yaml:"-"`
	DefaultPublisher            string            `json:"default_publisher" yaml:"-"`
	NFOBasenamePattern          string            `json:"nfo_basename_pattern" yaml:"-"`
	NFOTitlePattern             string            `json:"nfo_title_pattern" yaml:"-"`
	NFOCustomGenresFields       []string          `json:"nfo_custom_genres_fields" yaml:"-"`
	NFOCustomTagsFields         []string          `json:"nfo_custom_tags_fields" yaml:"-"`
	CensorOptionsRepresentation []string          `json:"censor_options_representation" yaml:"-"`
	CoverBasenamePattern        string            `json:"cover_basename_pattern" yaml:"-"`
	CoverHighRes                bool              `json:"cover_highres" yaml:"-"`
	CoverAddLabel               bool              `json:"cover_add_label" yaml:"-"`
	CoverCropOnIDPatterns       []string          `json:"cover_crop_on_id_patterns" yaml:"-"`
	CoverCropEngine             string            `json:"cover_crop_engine" yaml:"-"`
	FanartBasenamePattern       string            `json:"fanart_basename_pattern" yaml:"-"`
	ExtraFanarts                bool              `json:"extra_fanarts" yaml:"-"`
	ExtraFanartScrapInterval    string            `json:"extra_fanart_scrap_interval" yaml:"-"`
	TranslatorEngine            string            `json:"translator_engine" yaml:"-"`
	TranslatorURL               string            `json:"translator_url" yaml:"-"`
	TranslatorModel             string            `json:"translator_model" yaml:"-"`
	TranslatorAppID             string            `json:"translator_app_id" yaml:"-"`
	TranslatorAPIKey            string            `json:"translator_api_key,omitempty" yaml:"-"`
	TranslatorAPIKeySet         bool              `json:"translator_api_key_set,omitempty" yaml:"-"`
	TranslateTitle              bool              `json:"translate_title" yaml:"-"`
	TranslatePlot               bool              `json:"translate_plot" yaml:"-"`
	TimeoutSeconds              int               `json:"timeout_seconds" yaml:"-"`
	LastRunAt                   *string           `json:"last_run_at" yaml:"-"`
	NextRunAt                   *string           `json:"next_run_at" yaml:"-"`
}

type FeedEntry struct {
	Title       string `json:"title"`
	EntryID     string `json:"entry_id,omitempty"`
	SourceURL   string `json:"source_url"`
	Published   string `json:"published"`
	Summary     string `json:"summary,omitempty"`
	Matched     bool   `json:"matched"`
	Reason      string `json:"reason"`
	MagnetFound bool   `json:"magnet_found,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Error       string `json:"error,omitempty"`
}

type RunDetail struct {
	JobID   int64  `json:"job_id,omitempty"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type RunSummary struct {
	RunID     int64       `json:"run_id"`
	DryRun    bool        `json:"dry_run"`
	Entries   int         `json:"entries"`
	Matched   int         `json:"matched"`
	Submitted int         `json:"submitted"`
	Skipped   int         `json:"skipped"`
	Failed    int         `json:"failed"`
	Details   []RunDetail `json:"details"`
}

func defaultSettings() Settings {
	return Settings{
		PreviewMode:         true,
		QBPort:              8080,
		QBVerifyCertificate: true,
		RequestTimeout:      20,
		RunRetentionCount:   100,
	}
}

func defaultJavspConfig() JavspConfig {
	return JavspConfig{
		ScheduleType:          "daily",
		ScheduleMinutes:       1440,
		ScheduleTime:          "04:00",
		IgnoredIDPatterns:     []string{`(144|240|360|480|720|1080)[Pp]`, `[24][Kk]`, `\w+2048\.com`, `Carib(beancom)?`, `[^a-z\d](f?hd|lt)[^a-z\d]`},
		FilenameExtensions:    []string{".3gp", ".avi", ".f4v", ".flv", ".iso", ".m2ts", ".m4v", ".mkv", ".mov", ".mp4", ".mpeg", ".mpg", ".rm", ".rmvb", ".strm", ".ts", ".vob", ".webm", ".wmv"},
		IgnoredFolderPatterns: []string{`^\.`, `^#recycle$`, `^#整理完成$`, `^#不要扫描$`},
		MinimumSize:           "1GiB",
		ProxyFree: map[string]string{
			"avsox": "https://avsox.click", "javbus": "https://www.seedmm.help",
			"javdb": "https://javdb368.com", "javlib": "https://www.y78k.com",
		},
		NetworkRetry:                3,
		NetworkTimeout:              "PT10S",
		NormalCrawlers:              []string{"faleno", "airav", "avsox", "javbus", "javdb", "javlib", "jav321", "mgstage", "prestige"},
		FC2Crawlers:                 []string{"fc2", "avsox", "javdb", "javmenu", "fc2db", "fc2ppvdb"},
		CIDCrawlers:                 []string{"fanza"},
		GetchuCrawlers:              []string{"dl_getchu"},
		GyuttoCrawlers:              []string{"gyutto"},
		RequiredKeys:                []string{"cover", "title"},
		MovieConcurrency:            3,
		Hardworking:                 true,
		RespectSiteAVID:             true,
		SleepAfterScraping:          "PT1S",
		UseJavDBCover:               "fallback",
		NormalizeActressName:        true,
		MoveFiles:                   true,
		OutputFolderPattern:         "#整理完成/{actress}/[{num}] {title}",
		PathBasenamePattern:         "{num}",
		PathLengthMaximum:           250,
		PathLengthByByte:            true,
		PathMaxActressCount:         10,
		RemoveTrailingActorName:     true,
		DefaultTitle:                "#未知标题",
		DefaultActress:              "#未知女优",
		DefaultSeries:               "#未知系列",
		DefaultDirector:             "#未知导演",
		DefaultProducer:             "#未知制作商",
		DefaultPublisher:            "#未知发行商",
		NFOBasenamePattern:          "{num}",
		NFOTitlePattern:             "{num} {title}",
		NFOCustomGenresFields:       []string{"{genre}", "{censor}"},
		NFOCustomTagsFields:         []string{"{genre}", "{censor}"},
		CensorOptionsRepresentation: []string{"无码", "有码", "打码情况未知"},
		CoverBasenamePattern:        "poster",
		CoverHighRes:                true,
		CoverCropOnIDPatterns:       []string{`^\d{6}[-_]\d{3}$`, `^ARA`, `^SIRO`, `^GANA`, `^MIUM`, `^HHL`},
		CoverCropEngine:             "pigo",
		FanartBasenamePattern:       "fanart",
		ExtraFanarts:                true,
		ExtraFanartScrapInterval:    "PT1.5S",
		TranslatorEngine:            "google",
		TranslateTitle:              true,
		TranslatePlot:               true,
		TimeoutSeconds:              3600,
	}
}

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func calculateNextRun(scheduleType string, scheduleMinutes int, scheduleTime string, now time.Time) (string, error) {
	now = now.UTC()
	if scheduleType != "daily" {
		return now.Add(time.Duration(scheduleMinutes) * time.Minute).Format(time.RFC3339), nil
	}
	if !timePattern.MatchString(scheduleTime) {
		return "", fmt.Errorf("每天执行时间必须是 HH:MM 格式")
	}
	parts := strings.Split(scheduleTime, ":")
	hour, _ := strconv.Atoi(parts[0])
	minute, _ := strconv.Atoi(parts[1])
	localNow := now.In(time.Local)
	candidate := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, time.Local)
	if !candidate.After(localNow) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate.UTC().Format(time.RFC3339), nil
}

func validateHTTPURL(value, label string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s必须是有效的 HTTP(S) 地址", label)
	}
	return nil
}

func validateTask(task Task) (Task, error) {
	task.Name = strings.TrimSpace(task.Name)
	task.FeedURL = strings.TrimSpace(task.FeedURL)
	if task.Name == "" {
		return Task{}, fmt.Errorf("任务名称不能为空")
	}
	if err := validateHTTPURL(task.FeedURL, "RSS 地址"); err != nil {
		return Task{}, err
	}
	if task.ScheduleMinutes < 5 || task.ScheduleMinutes > 10080 {
		return Task{}, fmt.Errorf("执行间隔必须在 5 到 10080 分钟之间")
	}
	if task.ScheduleType != "interval" && task.ScheduleType != "daily" {
		return Task{}, fmt.Errorf("不支持的定时方式")
	}
	if !timePattern.MatchString(task.ScheduleTime) {
		return Task{}, fmt.Errorf("每天执行时间必须是 HH:MM 格式")
	}
	if task.ProxyURL != "" {
		if err := validateHTTPURL(task.ProxyURL, "代理地址"); err != nil {
			return Task{}, err
		}
	}
	if task.Extractor != "direct" && task.Extractor != "rmdown" {
		task.Extractor = "rmdown"
	}
	task.ProxyURL = strings.TrimSpace(task.ProxyURL)
	task.QBCategory = strings.TrimSpace(task.QBCategory)
	task.SavePath = strings.TrimSpace(task.SavePath)
	return task, nil
}

func validateMaintenanceConfig(config MaintenanceConfig) (MaintenanceConfig, error) {
	root, err := canonicalDirectory(strings.TrimSpace(config.RootDirectory))
	if err != nil {
		return MaintenanceConfig{}, fmt.Errorf("媒体维护目录必须是已存在的非根绝对目录")
	}
	config.RootDirectory = root
	if config.ScheduleType != "interval" && config.ScheduleType != "daily" {
		return MaintenanceConfig{}, fmt.Errorf("媒体维护定时方式不受支持")
	}
	if config.ScheduleMinutes < 15 || config.ScheduleMinutes > 10080 {
		return MaintenanceConfig{}, fmt.Errorf("媒体维护执行间隔必须在 15 到 10080 分钟之间")
	}
	if !timePattern.MatchString(config.ScheduleTime) {
		return MaintenanceConfig{}, fmt.Errorf("媒体维护每天执行时间必须是 HH:MM 格式")
	}
	if config.MinimumAgeHours < 1 || config.MinimumAgeHours > 8760 {
		return MaintenanceConfig{}, fmt.Errorf("空目录最短保留时间必须在 1 到 8760 小时之间")
	}
	return config, nil
}

func validateIntakeConfig(config IntakeConfig) (IntakeConfig, error) {
	source, err := canonicalDirectory(strings.TrimSpace(config.SourceDirectory))
	if err != nil {
		return IntakeConfig{}, fmt.Errorf("下载完成来源目录必须是已存在的非根绝对目录")
	}
	destination, err := canonicalDirectory(strings.TrimSpace(config.DestinationDirectory))
	if err != nil {
		return IntakeConfig{}, fmt.Errorf("未处理目标目录必须是已存在的非根绝对目录")
	}
	if source == destination || pathWithinRoot(source, destination) || pathWithinRoot(destination, source) {
		return IntakeConfig{}, fmt.Errorf("来源目录与目标目录不能相同或互相包含")
	}
	if config.ScheduleMinutes < 5 || config.ScheduleMinutes > 10080 {
		return IntakeConfig{}, fmt.Errorf("下载入库执行间隔必须在 5 到 10080 分钟之间")
	}
	config.SourceDirectory = source
	config.DestinationDirectory = destination
	return config, nil
}

func validateJavspConfig(config JavspConfig) (JavspConfig, error) {
	input := strings.TrimSpace(config.InputDirectory)
	if input == "" || strings.ContainsRune(input, 0) || !filepath.IsAbs(input) {
		return JavspConfig{}, fmt.Errorf("JAVSP 扫描目录必须是非根目录的绝对路径")
	}
	for _, part := range strings.Split(filepath.ToSlash(input), "/") {
		if part == ".." {
			return JavspConfig{}, fmt.Errorf("JAVSP 扫描目录必须是非根目录的绝对路径")
		}
	}
	resolvedInput, err := filepath.EvalSymlinks(filepath.Clean(input))
	if err != nil {
		return JavspConfig{}, fmt.Errorf("JAVSP 扫描目录不存在或无法访问")
	}
	info, err := filepath.Abs(resolvedInput)
	stat, statErr := os.Stat(info)
	if err != nil || statErr != nil || !stat.IsDir() || info == string(filepath.Separator) {
		return JavspConfig{}, fmt.Errorf("JAVSP 扫描目录必须是已存在的非根目录")
	}
	config.InputDirectory = info
	outputBase := info
	config.OutputDirectory = strings.TrimSpace(config.OutputDirectory)
	if config.OutputDirectory != "" {
		outputBase, err = canonicalDirectory(config.OutputDirectory)
		if err != nil {
			return JavspConfig{}, fmt.Errorf("JAVSP 整理完成目录必须是已存在的非根绝对目录")
		}
		if outputBase == info || pathWithinRoot(outputBase, info) {
			return JavspConfig{}, fmt.Errorf("JAVSP 整理完成目录不能等于或包含扫描目录")
		}
		config.OutputDirectory = outputBase
	}

	pattern := strings.TrimSpace(config.OutputFolderPattern)
	if pattern == "" || strings.ContainsRune(pattern, 0) || filepath.IsAbs(pattern) {
		return JavspConfig{}, fmt.Errorf("JAVSP 输出目录模板无效")
	}
	for _, part := range strings.Split(filepath.ToSlash(pattern), "/") {
		if part == ".." {
			return JavspConfig{}, fmt.Errorf("JAVSP 输出目录模板无效")
		}
	}
	resolvedOutput, err := filepath.Abs(filepath.Join(outputBase, pattern))
	if err != nil {
		return JavspConfig{}, fmt.Errorf("JAVSP 输出目录模板无效")
	}
	prefix := existingPathPrefix(resolvedOutput)
	if evaluated, evalErr := filepath.EvalSymlinks(prefix); evalErr == nil {
		if remainder, relErr := filepath.Rel(prefix, resolvedOutput); relErr == nil {
			resolvedOutput = filepath.Join(evaluated, remainder)
		}
	}
	relative, err := filepath.Rel(outputBase, resolvedOutput)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return JavspConfig{}, fmt.Errorf("JAVSP 输出模板不能超出选定的输出根目录")
	}
	config.OutputFolderPattern = pattern

	if config.ProxyServer != "" {
		parsed, parseErr := url.Parse(config.ProxyServer)
		allowed := parsed != nil && (parsed.Scheme == "http" || parsed.Scheme == "https" || parsed.Scheme == "socks5" || parsed.Scheme == "socks5h")
		if parseErr != nil || !allowed || parsed.Host == "" {
			return JavspConfig{}, fmt.Errorf("JAVSP 代理必须是有效的 HTTP(S) 或 SOCKS5 地址")
		}
	}
	if !sizePattern.MatchString(strings.TrimSpace(config.MinimumSize)) {
		return JavspConfig{}, fmt.Errorf("JAVSP 最小文件大小格式无效")
	}
	config.MinimumSize = strings.TrimSpace(config.MinimumSize)
	var listErr error
	config.IgnoredIDPatterns, listErr = normalizeJavspList(config.IgnoredIDPatterns, "忽略番号正则", 64, 512)
	if listErr != nil {
		return JavspConfig{}, listErr
	}
	for _, pattern := range config.IgnoredIDPatterns {
		if _, err := regexp.Compile(pattern); err != nil {
			return JavspConfig{}, fmt.Errorf("JAVSP 忽略番号正则无效：%s", pattern)
		}
	}
	config.FilenameExtensions, listErr = normalizeJavspList(config.FilenameExtensions, "影片扩展名", 128, 32)
	if listErr != nil || len(config.FilenameExtensions) == 0 {
		return JavspConfig{}, fmt.Errorf("JAVSP 影片扩展名至少需要一项")
	}
	for _, extension := range config.FilenameExtensions {
		if !regexp.MustCompile(`^\.[A-Za-z0-9]{1,16}$`).MatchString(extension) {
			return JavspConfig{}, fmt.Errorf("JAVSP 影片扩展名无效：%s", extension)
		}
	}
	config.IgnoredFolderPatterns, listErr = normalizeJavspList(config.IgnoredFolderPatterns, "忽略目录正则", 64, 512)
	if listErr != nil {
		return JavspConfig{}, listErr
	}
	for _, pattern := range config.IgnoredFolderPatterns {
		if _, err := regexp.Compile(pattern); err != nil {
			return JavspConfig{}, fmt.Errorf("JAVSP 忽略目录正则无效：%s", pattern)
		}
	}
	if len(config.ProxyFree) > 32 {
		return JavspConfig{}, fmt.Errorf("JAVSP 免代理站点不能超过 32 项")
	}
	normalizedProxyFree := make(map[string]string, len(config.ProxyFree))
	for rawKey, rawValue := range config.ProxyFree {
		key, value := strings.TrimSpace(rawKey), strings.TrimSpace(rawValue)
		if !regexp.MustCompile(`^[a-z0-9_]{1,32}$`).MatchString(key) {
			return JavspConfig{}, fmt.Errorf("JAVSP 免代理站点标识无效：%s", key)
		}
		if err := validateHTTPURL(value, "JAVSP 免代理地址"); err != nil {
			return JavspConfig{}, err
		}
		normalizedProxyFree[key] = value
	}
	config.ProxyFree = normalizedProxyFree
	if config.NetworkRetry < 0 || config.NetworkRetry > 20 {
		return JavspConfig{}, fmt.Errorf("JAVSP 网络重试次数必须在 0 到 20 之间")
	}
	if err := validateJavspDuration(config.NetworkTimeout, "网络超时", time.Second, 10*time.Minute); err != nil {
		return JavspConfig{}, err
	}
	if err := validateJavspDuration(config.SleepAfterScraping, "抓取启动间隔", 0, time.Minute); err != nil {
		return JavspConfig{}, err
	}
	if err := validateJavspDuration(config.ExtraFanartScrapInterval, "剧照请求间隔", 0, time.Minute); err != nil {
		return JavspConfig{}, err
	}
	for label, values := range map[string][]string{
		"普通影片爬虫": config.NormalCrawlers, "FC2 爬虫": config.FC2Crawlers,
		"CID 爬虫": config.CIDCrawlers, "Getchu 爬虫": config.GetchuCrawlers, "Gyutto 爬虫": config.GyuttoCrawlers,
	} {
		normalized, err := normalizeJavspList(values, label, 32, 32)
		if err != nil {
			return JavspConfig{}, err
		}
		for _, crawler := range normalized {
			if !validJavspCrawlerIDs[crawler] {
				return JavspConfig{}, fmt.Errorf("JAVSP %s包含不支持的爬虫：%s", label, crawler)
			}
		}
		switch label {
		case "普通影片爬虫":
			config.NormalCrawlers = normalized
		case "FC2 爬虫":
			config.FC2Crawlers = normalized
		case "CID 爬虫":
			config.CIDCrawlers = normalized
		case "Getchu 爬虫":
			config.GetchuCrawlers = normalized
		case "Gyutto 爬虫":
			config.GyuttoCrawlers = normalized
		}
	}
	config.RequiredKeys, listErr = normalizeJavspList(config.RequiredKeys, "抓取必需字段", 32, 32)
	if listErr != nil || len(config.RequiredKeys) == 0 {
		return JavspConfig{}, fmt.Errorf("JAVSP 抓取必需字段至少需要一项")
	}
	for _, key := range config.RequiredKeys {
		if !validJavspMovieInfoFields[key] {
			return JavspConfig{}, fmt.Errorf("JAVSP 抓取必需字段不受支持：%s", key)
		}
	}
	if config.MovieConcurrency < 1 || config.MovieConcurrency > 64 {
		return JavspConfig{}, fmt.Errorf("JAVSP 影片并发数必须在 1 到 64 之间")
	}
	config.FC2FanLocalPath = strings.TrimSpace(config.FC2FanLocalPath)
	if config.FC2FanLocalPath != "" {
		path, err := canonicalDirectory(config.FC2FanLocalPath)
		if err != nil {
			return JavspConfig{}, fmt.Errorf("JAVSP fc2fan 本地目录不存在或无法访问")
		}
		config.FC2FanLocalPath = path
	}
	if config.UseJavDBCover != "fallback" && config.UseJavDBCover != "yes" && config.UseJavDBCover != "no" {
		return JavspConfig{}, fmt.Errorf("JAVSP javdb 封面策略必须是 fallback、yes 或 no")
	}
	for _, field := range []struct {
		label string
		value *string
		max   int
	}{
		{"媒体文件名模板", &config.PathBasenamePattern, 512}, {"NFO 文件名模板", &config.NFOBasenamePattern, 256},
		{"封面文件名模板", &config.CoverBasenamePattern, 256}, {"剧照文件名模板", &config.FanartBasenamePattern, 256},
	} {
		*field.value = strings.TrimSpace(*field.value)
		if !validJavspBasename(*field.value, field.max) {
			return JavspConfig{}, fmt.Errorf("JAVSP %s无效", field.label)
		}
	}
	if config.PathLengthMaximum < 1 || config.PathLengthMaximum > 4096 {
		return JavspConfig{}, fmt.Errorf("JAVSP 最大路径长度必须在 1 到 4096 之间")
	}
	if config.PathMaxActressCount < 1 || config.PathMaxActressCount > 100 {
		return JavspConfig{}, fmt.Errorf("JAVSP 最大女优人数必须在 1 到 100 之间")
	}
	for _, field := range []struct {
		label string
		value *string
		max   int
	}{
		{"默认标题", &config.DefaultTitle, 256}, {"默认女优", &config.DefaultActress, 256},
		{"默认系列", &config.DefaultSeries, 256}, {"默认导演", &config.DefaultDirector, 256},
		{"默认制作商", &config.DefaultProducer, 256}, {"默认发行商", &config.DefaultPublisher, 256},
		{"NFO 标题模板", &config.NFOTitlePattern, 1024},
	} {
		*field.value = strings.TrimSpace(*field.value)
		if *field.value == "" || len(*field.value) > field.max || strings.ContainsRune(*field.value, 0) {
			return JavspConfig{}, fmt.Errorf("JAVSP %s无效", field.label)
		}
	}
	for label, target := range map[string]*[]string{
		"NFO 类型字段": &config.NFOCustomGenresFields, "NFO 标签字段": &config.NFOCustomTagsFields,
	} {
		normalized, err := normalizeJavspList(*target, label, 64, 128)
		if err != nil {
			return JavspConfig{}, err
		}
		*target = normalized
	}
	config.CensorOptionsRepresentation, listErr = normalizeJavspList(config.CensorOptionsRepresentation, "打码状态文案", 3, 128)
	if listErr != nil || len(config.CensorOptionsRepresentation) != 3 {
		return JavspConfig{}, fmt.Errorf("JAVSP 打码状态文案必须正好包含 3 项")
	}
	config.CoverCropOnIDPatterns, listErr = normalizeJavspList(config.CoverCropOnIDPatterns, "封面裁剪番号正则", 64, 512)
	if listErr != nil {
		return JavspConfig{}, listErr
	}
	for _, pattern := range config.CoverCropOnIDPatterns {
		if _, err := regexp.Compile(pattern); err != nil {
			return JavspConfig{}, fmt.Errorf("JAVSP 封面裁剪番号正则无效：%s", pattern)
		}
	}
	if config.CoverCropEngine != "none" && config.CoverCropEngine != "pigo" && config.CoverCropEngine != "slimeface" {
		return JavspConfig{}, fmt.Errorf("JAVSP 封面裁剪引擎不受支持")
	}
	if config.ScheduleType != "interval" && config.ScheduleType != "daily" {
		return JavspConfig{}, fmt.Errorf("JAVSP 不支持的定时方式")
	}
	if config.ScheduleMinutes < 15 || config.ScheduleMinutes > 10080 {
		return JavspConfig{}, fmt.Errorf("JAVSP 执行间隔必须在 15 到 10080 分钟之间")
	}
	if !timePattern.MatchString(config.ScheduleTime) {
		return JavspConfig{}, fmt.Errorf("JAVSP 每天执行时间必须是 HH:MM 格式")
	}
	if config.TimeoutSeconds < 60 || config.TimeoutSeconds > 21600 {
		return JavspConfig{}, fmt.Errorf("JAVSP 超时时间必须在 60 到 21600 秒之间")
	}
	config.TranslatorEngine = strings.ToLower(strings.TrimSpace(config.TranslatorEngine))
	config.TranslatorURL = strings.TrimSpace(config.TranslatorURL)
	config.TranslatorModel = strings.TrimSpace(config.TranslatorModel)
	config.TranslatorAppID = strings.TrimSpace(config.TranslatorAppID)
	config.TranslatorAPIKey = strings.TrimSpace(config.TranslatorAPIKey)
	switch config.TranslatorEngine {
	case "none", "google":
	case "bing", "claude", "deepl":
		if config.TranslatorAPIKey == "" {
			return JavspConfig{}, fmt.Errorf("JAVSP 翻译引擎需要 API Key")
		}
	case "baidu":
		if config.TranslatorAppID == "" || config.TranslatorAPIKey == "" {
			return JavspConfig{}, fmt.Errorf("JAVSP 百度翻译需要 App ID 和 API Key")
		}
	case "openai":
		if err := validateHTTPURL(config.TranslatorURL, "JAVSP OpenAI 兼容地址"); err != nil {
			return JavspConfig{}, err
		}
		if config.TranslatorModel == "" || config.TranslatorAPIKey == "" {
			return JavspConfig{}, fmt.Errorf("JAVSP OpenAI 兼容翻译需要模型和 API Key")
		}
	default:
		return JavspConfig{}, fmt.Errorf("JAVSP 翻译引擎不受支持")
	}
	return config, nil
}

func normalizeJavspList(values []string, label string, maxItems, maxLength int) ([]string, error) {
	if len(values) > maxItems {
		return nil, fmt.Errorf("JAVSP %s不能超过 %d 项", label, maxItems)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" || len(value) > maxLength || strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("JAVSP %s包含无效内容", label)
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result, nil
}

func validJavspBasename(value string, maxLength int) bool {
	return value != "" && len(value) <= maxLength && !strings.ContainsRune(value, 0) &&
		!strings.ContainsAny(value, `/\\`) && value != "." && value != ".."
}

func parseJavspDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "P") {
		return time.ParseDuration(value)
	}
	match := regexp.MustCompile(`^P(?:(\d+(?:\.\d+)?)W)?(?:(\d+(?:\.\d+)?)D)?(?:T(?:(\d+(?:\.\d+)?)H)?(?:(\d+(?:\.\d+)?)M)?(?:(\d+(?:\.\d+)?)S)?)?$`).FindStringSubmatch(value)
	if match == nil {
		return 0, fmt.Errorf("无效的 ISO 8601 时长")
	}
	units := []time.Duration{7 * 24 * time.Hour, 24 * time.Hour, time.Hour, time.Minute, time.Second}
	var total time.Duration
	used := false
	for index, unit := range units {
		if match[index+1] == "" {
			continue
		}
		number, err := strconv.ParseFloat(match[index+1], 64)
		if err != nil {
			return 0, err
		}
		total += time.Duration(number * float64(unit))
		used = true
	}
	if !used {
		return 0, fmt.Errorf("时长不能为空")
	}
	return total, nil
}

func validateJavspDuration(value, label string, minimum, maximum time.Duration) error {
	duration, err := parseJavspDuration(value)
	if err != nil || duration < minimum || duration > maximum {
		return fmt.Errorf("JAVSP %s格式或范围无效", label)
	}
	return nil
}

var validJavspCrawlerIDs = map[string]bool{
	"airav": true, "avsox": true, "avwiki": true, "dl_getchu": true, "fanza": true, "faleno": true,
	"fc2": true, "fc2db": true, "fc2fan": true, "fc2ppvdb": true, "gyutto": true, "jav321": true,
	"javbus": true, "javdb": true, "javlib": true, "javmenu": true, "mgstage": true,
	"njav": true, "prestige": true, "arzon": true, "arzon_iv": true,
}

var validJavspMovieInfoFields = map[string]bool{
	"dvdid": true, "cid": true, "url": true, "plot": true, "cover": true, "big_cover": true,
	"genre": true, "genre_id": true, "genre_norm": true, "score": true, "title": true,
	"ori_title": true, "magnet": true, "serial": true, "actress": true, "actress_pics": true,
	"director": true, "duration": true, "producer": true, "publisher": true, "uncensored": true,
	"publish_date": true, "preview_pics": true, "preview_video": true,
}

func existingPathPrefix(target string) string {
	current := target
	for {
		if _, err := filepath.EvalSymlinks(current); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return current
		}
		current = parent
	}
}

func fingerprint(identity string) string {
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:])
}

func encodeJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
