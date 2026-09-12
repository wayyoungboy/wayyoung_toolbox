package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

const maxJavspOutput = 512 << 10

type JavspResult struct {
	ExitCode  int
	Output    string
	Total     int
	Processed int
	Failed    int
	Movies    []JavspMovieOutcome
}

type JavspRunError struct {
	Message  string
	ExitCode *int
	Output   string
	Cause    error
}

func (e *JavspRunError) Error() string { return e.Message }
func (e *JavspRunError) Unwrap() error { return e.Cause }

type tailBuffer struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (b *tailBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(value) >= maxJavspOutput {
		b.data = append(b.data[:0], value[len(value)-maxJavspOutput:]...)
		b.truncated = true
		return len(value), nil
	}
	b.data = append(b.data, value...)
	if len(b.data) > maxJavspOutput {
		b.data = append(b.data[:0], b.data[len(b.data)-maxJavspOutput:]...)
		b.truncated = true
	}
	return len(value), nil
}
func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	prefix := ""
	if b.truncated {
		prefix = "[日志过长，仅保留末尾]\n"
	}
	return prefix + strings.ToValidUTF8(string(b.data), "�")
}

type JavspRunner struct {
	bundleRoot    string
	workRoot      string
	engine        embeddedJavsp
	mu            sync.Mutex
	activeOutput  *tailBuffer
	activeCancel  context.CancelFunc
	lastProgress  JavspProgress
	runPending    bool
	stopRequested bool
}

func NewJavspRunner(bundleRoot, dataRoot string) *JavspRunner {
	return newJavspRunnerWithEngine(bundleRoot, dataRoot, embeddedJavspAdapter{version: bundledJavspVersion(bundleRoot)})
}

func newJavspRunnerWithEngine(bundleRoot, dataRoot string, engine embeddedJavsp) *JavspRunner {
	return &JavspRunner{bundleRoot: bundleRoot, workRoot: filepath.Join(dataRoot, "javsp"), engine: engine}
}
func (j *JavspRunner) ConfigPath() string { return filepath.Join(j.workRoot, "config.yml") }

func (j *JavspRunner) CurrentOutput() string {
	j.mu.Lock()
	output := j.activeOutput
	j.mu.Unlock()
	if output == nil {
		return ""
	}
	return output.String()
}

func (j *JavspRunner) LastProgress() JavspProgress {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.lastProgress
}

func bundledJavspVersion(bundleRoot string) string {
	raw, err := os.ReadFile(filepath.Join(bundleRoot, "build-info.json"))
	if err != nil {
		return "embedded"
	}
	var info map[string]any
	if json.Unmarshal(raw, &info) == nil {
		if described, ok := info["described"].(string); ok && described != "" {
			return described
		}
	}
	return "embedded"
}

func copyTreeContext(ctx context.Context, source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := copyWithContext(ctx, output, input)
		inputCloseErr := input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputCloseErr != nil {
			return inputCloseErr
		}
		return closeErr
	})
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	buffer := make([]byte, 64*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			count, writeErr := destination.Write(buffer[:read])
			written += int64(count)
			if writeErr != nil {
				return written, writeErr
			}
			if count != read {
				return written, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

func (j *JavspRunner) ensureRuntimeAssetsContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	runtimeRoot := filepath.Join(j.bundleRoot, "runtime")
	template := filepath.Join(runtimeRoot, "config.yml")
	if _, err := os.Stat(template); err != nil {
		return fmt.Errorf("JAVSP 默认配置缺失")
	}
	if err := os.MkdirAll(j.workRoot, 0o700); err != nil {
		return err
	}
	version := "unknown"
	if raw, err := os.ReadFile(filepath.Join(j.bundleRoot, "build-info.json")); err == nil {
		var info map[string]any
		if json.Unmarshal(raw, &info) == nil {
			if value, ok := info["described"].(string); ok {
				version = value
			}
		}
	}
	marker := filepath.Join(j.workRoot, ".bundle-version")
	current, _ := os.ReadFile(marker)
	if strings.TrimSpace(string(current)) != version {
		for _, directory := range []string{"data", "image"} {
			if err := copyTreeContext(ctx, filepath.Join(runtimeRoot, directory), filepath.Join(j.workRoot, directory)); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.WriteFile(marker, []byte(version+"\n"), 0o600); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (j *JavspRunner) ensureRuntime() error {
	if err := j.ensureRuntimeAssetsContext(context.Background()); err != nil {
		return err
	}
	template := filepath.Join(j.bundleRoot, "runtime", "config.yml")
	if _, err := os.Stat(j.ConfigPath()); errors.Is(err, os.ErrNotExist) {
		raw, readErr := os.ReadFile(template)
		if readErr != nil {
			return readErr
		}
		if writeErr := os.WriteFile(j.ConfigPath(), raw, 0o600); writeErr != nil {
			return writeErr
		}
	}
	return os.Chmod(j.ConfigPath(), 0o600)
}

func nestedMap(parent map[string]any, key string) map[string]any {
	if current, ok := parent[key].(map[string]any); ok {
		return current
	}
	created := map[string]any{}
	parent[key] = created
	return created
}

type javspRuntimeConfig struct {
	Scanner struct {
		IgnoredIDPatterns     []string `yaml:"ignored_id_pattern"`
		InputDirectory        *string  `yaml:"input_directory"`
		FilenameExtensions    []string `yaml:"filename_extensions"`
		IgnoredFolderPatterns []string `yaml:"ignored_folder_name_pattern"`
		MinimumSize           string   `yaml:"minimum_size"`
		SkipNFODir            bool     `yaml:"skip_nfo_dir"`
	} `yaml:"scanner"`
	Network struct {
		ProxyServer *string           `yaml:"proxy_server"`
		ProxyFree   map[string]string `yaml:"proxy_free"`
		Retry       int               `yaml:"retry"`
		Timeout     string            `yaml:"timeout"`
	} `yaml:"network"`
	Crawler struct {
		Selection struct {
			Normal []string `yaml:"normal"`
			FC2    []string `yaml:"fc2"`
			CID    []string `yaml:"cid"`
			Getchu []string `yaml:"getchu"`
			Gyutto []string `yaml:"gyutto"`
		} `yaml:"selection"`
		RequiredKeys         []string `yaml:"required_keys"`
		MovieConcurrency     int      `yaml:"movie_concurrency"`
		Hardworking          bool     `yaml:"hardworking"`
		RespectSiteAVID      bool     `yaml:"respect_site_avid"`
		FC2FanLocalPath      *string  `yaml:"fc2fan_local_path"`
		SleepAfterScraping   string   `yaml:"sleep_after_scraping"`
		UseJavDBCover        string   `yaml:"use_javdb_cover"`
		NormalizeActressName bool     `yaml:"normalize_actress_name"`
	} `yaml:"crawler"`
	Summarizer struct {
		MoveFiles bool `yaml:"move_files"`
		Path      struct {
			OutputFolderPattern string `yaml:"output_folder_pattern"`
			BasenamePattern     string `yaml:"basename_pattern"`
			LengthMaximum       int    `yaml:"length_maximum"`
			LengthByByte        bool   `yaml:"length_by_byte"`
			MaxActressCount     int    `yaml:"max_actress_count"`
			HardLink            bool   `yaml:"hard_link"`
		} `yaml:"path"`
		Title struct {
			RemoveTrailingActorName bool `yaml:"remove_trailing_actor_name"`
		} `yaml:"title"`
		Default struct {
			Title     string `yaml:"title"`
			Actress   string `yaml:"actress"`
			Series    string `yaml:"series"`
			Director  string `yaml:"director"`
			Producer  string `yaml:"producer"`
			Publisher string `yaml:"publisher"`
		} `yaml:"default"`
		NFO struct {
			BasenamePattern    string   `yaml:"basename_pattern"`
			TitlePattern       string   `yaml:"title_pattern"`
			CustomGenresFields []string `yaml:"custom_genres_fields"`
			CustomTagsFields   []string `yaml:"custom_tags_fields"`
		} `yaml:"nfo"`
		CensorOptionsRepresentation []string `yaml:"censor_options_representation"`
		Cover                       struct {
			BasenamePattern string `yaml:"basename_pattern"`
			HighRes         bool   `yaml:"highres"`
			AddLabel        bool   `yaml:"add_label"`
			Crop            struct {
				OnIDPatterns []string       `yaml:"on_id_pattern"`
				Engine       map[string]any `yaml:"engine"`
			} `yaml:"crop"`
		} `yaml:"cover"`
		Fanart struct {
			BasenamePattern string `yaml:"basename_pattern"`
		} `yaml:"fanart"`
		ExtraFanarts struct {
			Enabled       bool   `yaml:"enabled"`
			ScrapInterval string `yaml:"scrap_interval"`
		} `yaml:"extra_fanarts"`
	} `yaml:"summarizer"`
	Translator struct {
		Engine map[string]any `yaml:"engine"`
		Fields struct {
			Title bool `yaml:"title"`
			Plot  bool `yaml:"plot"`
		} `yaml:"fields"`
	} `yaml:"translator"`
}

func stringMapValue(values map[string]any, key string) string {
	if value, ok := values[key].(string); ok {
		return value
	}
	return ""
}

func (j *JavspRunner) ReadConfig() (JavspConfig, error) {
	if err := j.ensureRuntime(); err != nil {
		return JavspConfig{}, err
	}
	raw, err := os.ReadFile(j.ConfigPath())
	if err != nil {
		return JavspConfig{}, err
	}
	var document javspRuntimeConfig
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return JavspConfig{}, fmt.Errorf("读取 JAVSP 配置失败：%w", err)
	}
	config := defaultJavspConfig()
	config.IgnoredIDPatterns = document.Scanner.IgnoredIDPatterns
	config.FilenameExtensions = document.Scanner.FilenameExtensions
	config.IgnoredFolderPatterns = document.Scanner.IgnoredFolderPatterns
	config.SkipNFODir = document.Scanner.SkipNFODir
	if document.Network.ProxyServer != nil {
		config.ProxyServer = *document.Network.ProxyServer
	} else {
		config.ProxyServer = ""
	}
	config.ProxyFree = document.Network.ProxyFree
	config.NetworkRetry = document.Network.Retry
	config.NetworkTimeout = document.Network.Timeout
	config.NormalCrawlers = document.Crawler.Selection.Normal
	config.FC2Crawlers = document.Crawler.Selection.FC2
	config.CIDCrawlers = document.Crawler.Selection.CID
	config.GetchuCrawlers = document.Crawler.Selection.Getchu
	config.GyuttoCrawlers = document.Crawler.Selection.Gyutto
	config.RequiredKeys = document.Crawler.RequiredKeys
	config.MovieConcurrency = document.Crawler.MovieConcurrency
	config.Hardworking = document.Crawler.Hardworking
	config.RespectSiteAVID = document.Crawler.RespectSiteAVID
	if document.Crawler.FC2FanLocalPath != nil {
		config.FC2FanLocalPath = *document.Crawler.FC2FanLocalPath
	}
	config.SleepAfterScraping = document.Crawler.SleepAfterScraping
	config.UseJavDBCover = document.Crawler.UseJavDBCover
	config.NormalizeActressName = document.Crawler.NormalizeActressName
	config.PathBasenamePattern = document.Summarizer.Path.BasenamePattern
	config.PathLengthMaximum = document.Summarizer.Path.LengthMaximum
	config.PathLengthByByte = document.Summarizer.Path.LengthByByte
	config.PathMaxActressCount = document.Summarizer.Path.MaxActressCount
	config.PathHardLink = document.Summarizer.Path.HardLink
	config.RemoveTrailingActorName = document.Summarizer.Title.RemoveTrailingActorName
	config.DefaultTitle = document.Summarizer.Default.Title
	config.DefaultActress = document.Summarizer.Default.Actress
	config.DefaultSeries = document.Summarizer.Default.Series
	config.DefaultDirector = document.Summarizer.Default.Director
	config.DefaultProducer = document.Summarizer.Default.Producer
	config.DefaultPublisher = document.Summarizer.Default.Publisher
	config.NFOBasenamePattern = document.Summarizer.NFO.BasenamePattern
	config.NFOTitlePattern = document.Summarizer.NFO.TitlePattern
	config.NFOCustomGenresFields = document.Summarizer.NFO.CustomGenresFields
	config.NFOCustomTagsFields = document.Summarizer.NFO.CustomTagsFields
	config.CensorOptionsRepresentation = document.Summarizer.CensorOptionsRepresentation
	config.CoverBasenamePattern = document.Summarizer.Cover.BasenamePattern
	config.CoverHighRes = document.Summarizer.Cover.HighRes
	config.CoverAddLabel = document.Summarizer.Cover.AddLabel
	config.CoverCropOnIDPatterns = document.Summarizer.Cover.Crop.OnIDPatterns
	config.CoverCropEngine = stringMapValue(document.Summarizer.Cover.Crop.Engine, "name")
	if config.CoverCropEngine == "" {
		config.CoverCropEngine = "none"
	}
	config.FanartBasenamePattern = document.Summarizer.Fanart.BasenamePattern
	config.ExtraFanarts = document.Summarizer.ExtraFanarts.Enabled
	config.ExtraFanartScrapInterval = document.Summarizer.ExtraFanarts.ScrapInterval
	config.TranslatorEngine = stringMapValue(document.Translator.Engine, "name")
	if config.TranslatorEngine == "" {
		config.TranslatorEngine = "none"
	}
	config.TranslatorURL = stringMapValue(document.Translator.Engine, "url")
	config.TranslatorModel = stringMapValue(document.Translator.Engine, "model")
	config.TranslatorAppID = stringMapValue(document.Translator.Engine, "app_id")
	config.TranslatorAPIKey = stringMapValue(document.Translator.Engine, "api_key")
	config.TranslateTitle = document.Translator.Fields.Title
	config.TranslatePlot = document.Translator.Fields.Plot
	return config, nil
}

func (j *JavspRunner) PrepareConfigDataContext(ctx context.Context, config JavspConfig) ([]byte, error) {
	return j.prepareConfigDataContext(ctx, config, true)
}

func (j *JavspRunner) prepareConfigDataContext(ctx context.Context, config JavspConfig, assets bool) ([]byte, error) {
	if assets {
		if err := j.ensureRuntimeAssetsContext(ctx); err != nil {
			return nil, err
		}
	} else if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(j.bundleRoot, "runtime", "config.yml"))
	if err != nil {
		return nil, fmt.Errorf("读取 JAVSP 配置失败：%w", err)
	}
	document := map[string]any{}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("读取 JAVSP 配置失败：%w", err)
	}
	scanner := nestedMap(document, "scanner")
	network := nestedMap(document, "network")
	crawler := nestedMap(document, "crawler")
	selection := nestedMap(crawler, "selection")
	summarizer := nestedMap(document, "summarizer")
	pathConfig := nestedMap(summarizer, "path")
	titleConfig := nestedMap(summarizer, "title")
	defaultConfig := nestedMap(summarizer, "default")
	nfo := nestedMap(summarizer, "nfo")
	cover := nestedMap(summarizer, "cover")
	coverCrop := nestedMap(cover, "crop")
	fanart := nestedMap(summarizer, "fanart")
	fanarts := nestedMap(summarizer, "extra_fanarts")
	translator := nestedMap(document, "translator")
	translatorFields := nestedMap(translator, "fields")
	other := nestedMap(document, "other")
	scanner["input_directory"] = config.InputDirectory
	scanner["ignored_id_pattern"] = config.IgnoredIDPatterns
	scanner["filename_extensions"] = config.FilenameExtensions
	scanner["ignored_folder_name_pattern"] = config.IgnoredFolderPatterns
	scanner["minimum_size"] = config.MinimumSize
	scanner["skip_nfo_dir"] = config.SkipNFODir
	scanner["manual"] = false
	if config.ProxyServer == "" {
		network["proxy_server"] = nil
	} else {
		network["proxy_server"] = config.ProxyServer
	}
	network["proxy_free"] = config.ProxyFree
	network["retry"] = config.NetworkRetry
	network["timeout"] = config.NetworkTimeout
	selection["normal"] = config.NormalCrawlers
	selection["fc2"] = config.FC2Crawlers
	selection["cid"] = config.CIDCrawlers
	selection["getchu"] = config.GetchuCrawlers
	selection["gyutto"] = config.GyuttoCrawlers
	crawler["required_keys"] = config.RequiredKeys
	crawler["movie_concurrency"] = config.MovieConcurrency
	crawler["hardworking"] = config.Hardworking
	crawler["respect_site_avid"] = config.RespectSiteAVID
	if config.FC2FanLocalPath == "" {
		crawler["fc2fan_local_path"] = nil
	} else {
		crawler["fc2fan_local_path"] = config.FC2FanLocalPath
	}
	crawler["sleep_after_scraping"] = config.SleepAfterScraping
	crawler["use_javdb_cover"] = config.UseJavDBCover
	crawler["normalize_actress_name"] = config.NormalizeActressName
	summarizer["move_files"] = config.MoveFiles
	pathConfig["output_folder_pattern"] = config.OutputFolderPattern
	if config.OutputDirectory != "" {
		pathConfig["output_folder_pattern"] = filepath.Join(config.OutputDirectory, config.OutputFolderPattern)
	}
	pathConfig["basename_pattern"] = config.PathBasenamePattern
	pathConfig["length_maximum"] = config.PathLengthMaximum
	pathConfig["length_by_byte"] = config.PathLengthByByte
	pathConfig["max_actress_count"] = config.PathMaxActressCount
	pathConfig["hard_link"] = config.PathHardLink
	titleConfig["remove_trailing_actor_name"] = config.RemoveTrailingActorName
	defaultConfig["title"] = config.DefaultTitle
	defaultConfig["actress"] = config.DefaultActress
	defaultConfig["series"] = config.DefaultSeries
	defaultConfig["director"] = config.DefaultDirector
	defaultConfig["producer"] = config.DefaultProducer
	defaultConfig["publisher"] = config.DefaultPublisher
	nfo["basename_pattern"] = config.NFOBasenamePattern
	nfo["title_pattern"] = config.NFOTitlePattern
	nfo["custom_genres_fields"] = config.NFOCustomGenresFields
	nfo["custom_tags_fields"] = config.NFOCustomTagsFields
	summarizer["censor_options_representation"] = config.CensorOptionsRepresentation
	cover["basename_pattern"] = config.CoverBasenamePattern
	cover["highres"] = config.CoverHighRes
	cover["add_label"] = config.CoverAddLabel
	coverCrop["on_id_pattern"] = config.CoverCropOnIDPatterns
	if config.CoverCropEngine == "none" {
		coverCrop["engine"] = nil
	} else {
		coverCrop["engine"] = map[string]any{"name": config.CoverCropEngine}
	}
	fanart["basename_pattern"] = config.FanartBasenamePattern
	fanarts["enabled"] = config.ExtraFanarts
	fanarts["scrap_interval"] = config.ExtraFanartScrapInterval
	if config.TranslatorEngine == "none" {
		translator["engine"] = nil
	} else {
		engine := map[string]any{"name": config.TranslatorEngine}
		if config.TranslatorURL != "" {
			engine["url"] = config.TranslatorURL
		}
		if config.TranslatorModel != "" {
			engine["model"] = config.TranslatorModel
		}
		if config.TranslatorAppID != "" {
			engine["app_id"] = config.TranslatorAppID
		}
		if config.TranslatorAPIKey != "" {
			engine["api_key"] = config.TranslatorAPIKey
		}
		translator["engine"] = engine
	}
	translatorFields["title"] = config.TranslateTitle
	translatorFields["plot"] = config.TranslatePlot
	other["interactive"] = false
	other["check_update"] = false
	other["auto_update"] = false
	encoded, err := yaml.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("生成 JAVSP 配置失败：%w", err)
	}
	return encoded, nil
}

func (j *JavspRunner) PrepareConfigData(config JavspConfig) ([]byte, error) {
	return j.PrepareConfigDataContext(context.Background(), config)
}

func (j *JavspRunner) Component(_ context.Context) map[string]any {
	return map[string]any{"available": true, "version": j.engine.Version(), "mode": "embedded", "error": ""}
}

func (j *JavspRunner) Probe(ctx context.Context) map[string]any {
	if err := j.ensureRuntimeAssetsContext(ctx); err != nil {
		return map[string]any{"available": false, "version": j.engine.Version(), "mode": "embedded", "error": err.Error()}
	}
	return map[string]any{"available": true, "version": j.engine.Version(), "mode": "embedded", "error": ""}
}

func (j *JavspRunner) Run(ctx context.Context, config JavspConfig) (JavspResult, error) {
	return j.runEmbedded(ctx, config)
}

// RunSelected preserves naming/config roots, and leaves global library
// maintenance to its explicit jobs. The queue finalizes only its own outputs.
func (j *JavspRunner) RunSelected(ctx context.Context, config JavspConfig, files []string, hooks JavspExecutionHooks) (JavspResult, error) {
	if len(files) == 0 {
		return JavspResult{}, fmt.Errorf("未指定需要整理的影片；不会回退到全量扫描")
	}
	return j.runEmbeddedSelection(ctx, config, append([]string{}, files...), hooks)
}

// BeginRun marks the short interval after a service job has been accepted but
// before its worker goroutine enters Run. A cancellation in that interval is
// latched for this run only.
func (j *JavspRunner) BeginRun() {
	j.mu.Lock()
	j.runPending = true
	j.stopRequested = false
	j.mu.Unlock()
}

func (j *JavspRunner) runEmbedded(parent context.Context, config JavspConfig) (JavspResult, error) {
	return j.runEmbeddedSelection(parent, config, nil, JavspExecutionHooks{})
}

func (j *JavspRunner) runEmbeddedSelection(parent context.Context, config JavspConfig, files []string, hooks JavspExecutionHooks) (JavspResult, error) {
	timeout := time.Duration(config.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(parent, timeout)
	buffer := &tailBuffer{}
	j.mu.Lock()
	j.activeOutput = buffer
	j.activeCancel = cancel
	j.runPending = false
	j.lastProgress = JavspProgress{Phase: "preparing"}
	stopRequested := j.stopRequested
	j.mu.Unlock()
	if stopRequested {
		cancel()
	}
	defer func() {
		cancel()
		j.mu.Lock()
		j.activeOutput = nil
		j.activeCancel = nil
		j.runPending = false
		j.stopRequested = false
		j.mu.Unlock()
	}()

	var engineResult embeddedJavspResult
	engineErr := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		configData, err := j.PrepareConfigDataContext(ctx, config)
		if err != nil {
			return err
		}
		defer func() {
			for index := range configData {
				configData[index] = 0
			}
		}()

		var outputRoot, nfoRoot string
		var directoriesBefore, videosBefore map[string]struct{}
		var snapshotErr, nfoSnapshotErr error
		if files == nil {
			outputRoot, directoriesBefore, snapshotErr = snapshotJavspOutputDirectoriesContext(ctx, config)
			if snapshotErr != nil && !errors.Is(snapshotErr, context.Canceled) && !errors.Is(snapshotErr, context.DeadlineExceeded) {
				slog.Warn("snapshot JAVSP output directories", "error", snapshotErr)
			}
			nfoRoot, videosBefore, nfoSnapshotErr = snapshotJavspOutputVideosContext(ctx, config)
			if nfoSnapshotErr != nil && !errors.Is(nfoSnapshotErr, context.Canceled) && !errors.Is(nfoSnapshotErr, context.DeadlineExceeded) {
				slog.Warn("snapshot JAVSP output videos", "error", nfoSnapshotErr)
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		engineResult, err = invokeEmbeddedJavsp(j.engine, ctx, embeddedJavspOptions{
			ConfigData: configData, ResourceRoot: j.workRoot, InputDirectory: config.InputDirectory, InputFiles: files, Hooks: hooks, Output: buffer,
			Progress: func(progress JavspProgress) {
				j.mu.Lock()
				j.lastProgress = progress
				j.mu.Unlock()
			},
		})
		if ctx.Err() == nil && files == nil {
			finalizeErr := j.finalizeEmbeddedOutput(ctx, config, outputRoot, directoriesBefore, snapshotErr, nfoRoot, videosBefore, nfoSnapshotErr)
			if err == nil && finalizeErr != nil {
				err = finalizeErr
			}
		}
		if err == nil {
			err = ctx.Err()
		}
		return err
	}()
	output := buffer.String()
	result := JavspResult{Output: output, Total: engineResult.Total, Processed: engineResult.Processed, Failed: engineResult.Failed, Movies: engineResult.Movies}
	if engineErr == nil {
		j.mu.Lock()
		j.lastProgress = JavspProgress{Phase: "completed", Current: engineResult.Total, Total: engineResult.Total, Processed: engineResult.Processed}
		j.mu.Unlock()
		return result, nil
	}

	message := "JAVSP 执行失败：" + engineErr.Error()
	j.mu.Lock()
	progress := j.lastProgress
	stopped := j.stopRequested
	j.mu.Unlock()
	if errors.Is(engineErr, context.DeadlineExceeded) {
		message = fmt.Sprintf("JAVSP 超过 %d 秒未完成", int(timeout.Seconds()))
	} else if errors.Is(engineErr, context.Canceled) || stopped {
		message = "JAVSP 已因应用停止而终止"
	}
	progress.Phase = "failed"
	if errors.Is(engineErr, context.Canceled) || errors.Is(engineErr, context.DeadlineExceeded) {
		progress.Phase = "canceled"
	}
	progress.Message = message
	j.mu.Lock()
	j.lastProgress = progress
	j.mu.Unlock()
	return result, &JavspRunError{Message: message, Output: output, Cause: engineErr}
}

func invokeEmbeddedJavsp(engine embeddedJavsp, ctx context.Context, options embeddedJavspOptions) (result embeddedJavspResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("JAVSP engine panic", "panic_type", fmt.Sprintf("%T", recovered), "stack", string(debug.Stack()))
			result = embeddedJavspResult{}
			err = errors.New("JAVSP 引擎内部错误")
		}
	}()
	return engine.Run(ctx, options)
}

func (j *JavspRunner) finalizeEmbeddedOutput(ctx context.Context, config JavspConfig, outputRoot string, directoriesBefore map[string]struct{}, snapshotErr error, nfoRoot string, videosBefore map[string]struct{}, nfoSnapshotErr error) error {
	if snapshotErr == nil && outputRoot != "" {
		removed, cleanupErr := cleanupNewEmptyJavspDirectoriesContext(ctx, outputRoot, directoriesBefore)
		if cleanupErr != nil {
			if errors.Is(cleanupErr, context.Canceled) || errors.Is(cleanupErr, context.DeadlineExceeded) {
				return cleanupErr
			}
			slog.Warn("cleanup new empty JAVSP directories", "error", cleanupErr)
		} else if removed > 0 {
			slog.Info("cleaned new empty JAVSP directories", "count", removed)
		}
	}
	if nfoSnapshotErr == nil && nfoRoot != "" {
		created, nfoErr := createFnosNFOsForNewVideosContext(ctx, config, nfoRoot, videosBefore)
		if nfoErr != nil {
			if errors.Is(nfoErr, context.Canceled) || errors.Is(nfoErr, context.DeadlineExceeded) {
				return nfoErr
			}
			slog.Warn("create fnOS companion NFO files", "error", nfoErr)
		} else if created > 0 {
			slog.Info("created fnOS companion NFO files", "count", created)
		}
	}
	return nil
}

func snapshotJavspOutputDirectoriesContext(ctx context.Context, config JavspConfig) (string, map[string]struct{}, error) {
	directories := map[string]struct{}{}
	root, available, err := plannedJavspOutputRoot(config)
	if err != nil || !available {
		return "", directories, err
	}
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return root, directories, nil
	}
	if err != nil {
		return "", directories, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", directories, fmt.Errorf("JAVSP 整理完成路径不是普通目录")
	}
	visited := 0
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > maxCompletedMediaEntries {
			return fmt.Errorf("JAVSP 整理完成目录超过 %d 个条目", maxCompletedMediaEntries)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			directories[path] = struct{}{}
		}
		return nil
	})
	return root, directories, err
}

func snapshotJavspOutputDirectories(config JavspConfig) (string, map[string]struct{}, error) {
	return snapshotJavspOutputDirectoriesContext(context.Background(), config)
}

func cleanupNewEmptyJavspDirectoriesContext(ctx context.Context, root string, before map[string]struct{}) (int, error) {
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return 0, nil
	}
	candidates := []string{}
	visited := 0
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > maxCompletedMediaEntries {
			return fmt.Errorf("JAVSP 整理完成目录超过 %d 个条目", maxCompletedMediaEntries)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path != root && entry.IsDir() {
			if _, existed := before[path]; !existed {
				candidates = append(candidates, path)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	sort.Slice(candidates, func(left, right int) bool {
		return strings.Count(candidates[left], string(filepath.Separator)) > strings.Count(candidates[right], string(filepath.Separator))
	})
	removed := 0
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if err := os.Remove(candidate); err == nil {
			removed++
		} else if !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTEMPTY) && !errors.Is(err, syscall.EEXIST) {
			return removed, err
		}
	}
	return removed, nil
}

func cleanupNewEmptyJavspDirectories(root string, before map[string]struct{}) (int, error) {
	return cleanupNewEmptyJavspDirectoriesContext(context.Background(), root, before)
}

func (j *JavspRunner) StopCurrent() bool {
	j.mu.Lock()
	if j.activeCancel == nil && !j.runPending {
		j.mu.Unlock()
		return false
	}
	j.stopRequested = true
	cancel := j.activeCancel
	j.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return true
}
