package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuukiy/javsp-go/internal/browsercookies"
	"github.com/yuukiy/javsp-go/internal/config"
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/crawler/sites"
	"github.com/yuukiy/javsp-go/internal/genre"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
	"github.com/yuukiy/javsp-go/internal/nfo"
	"github.com/yuukiy/javsp-go/internal/organizer"
	"github.com/yuukiy/javsp-go/internal/poster"
	"github.com/yuukiy/javsp-go/internal/projectpath"
	"github.com/yuukiy/javsp-go/internal/proxyfree"
	"github.com/yuukiy/javsp-go/internal/scanner"
	"github.com/yuukiy/javsp-go/internal/summary"
	"github.com/yuukiy/javsp-go/internal/translate"
	"github.com/yuukiy/javsp-go/internal/updatecheck"
	"github.com/yuukiy/javsp-go/internal/util"
)

const appName = "JavSP-Go"

var (
	version              = "dev"
	commit               = "unknown"
	buildDate            = "unknown"
	newEngineFromConfig  = translate.NewEngineFromConfig
	normalizeExtraFanart = poster.Normalize
)

func main() {
	cli, err := parseCLI(os.Args[1:], os.Stderr)
	if err != nil {
		os.Exit(2)
	}

	if cli.showVersion {
		fmt.Println(versionText())
		return
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get working directory: %v\n", err)
		os.Exit(2)
	}
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get executable path: %v\n", err)
		os.Exit(2)
	}
	resolvedConfigPath, err := projectpath.ResolveConfig(cli.configPath, cwd, executable)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve config: %v\n", err)
		os.Exit(1)
	}
	resourceRoot, err := projectpath.ResourceRoot(resolvedConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve resource root: %v\n", err)
		os.Exit(1)
	}
	cfg, err := config.LoadWithOverrides(resolvedConfigPath, os.Environ(), cli.configOverrides)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()
	client, err := buildHTTPClient(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build http client: %v\n", err)
		os.Exit(1)
	}
	checkForUpdates(ctx, cfg.Other.CheckUpdate, version, client.HTTPClient(), updatecheck.Check, os.Stdout)
	root, err := selectScanRoot(cli.inputPath, cfg.Scanner.InputDirectory, cfg.Other.Interactive, os.Stdin, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if cfg.Summarizer.MoveFiles {
		cfg.Summarizer.Path.OutputFolderPattern, err = resolveOutputFolderPattern(root, cfg.Summarizer.Path.OutputFolderPattern)
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolve output folder pattern: %v\n", err)
			os.Exit(1)
		}
	}
	opts, err := buildScannerOptions(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build scanner options: %v\n", err)
		os.Exit(1)
	}
	movies, err := scanMoviesWithDiagnostics(root, opts, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan movies: %v\n", err)
		os.Exit(1)
	}
	if err := requireRecognizedMovies(movies); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%s recognized %d movie(s)\n", appName, len(movies))
	movies, err = reviewMoviesForConfig(root, movies, cfg, os.Stdin, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "review movies: %v\n", err)
		os.Exit(1)
	}

	baseURLs := buildSiteBaseURLs(ctx, cfg, defaultProxyFreeResolver)
	registry := crawler.NewRegistry()
	fc2fanLocalPath := ""
	if cfg.Crawler.FC2FanLocalPath != nil {
		fc2fanLocalPath = *cfg.Crawler.FC2FanLocalPath
	}
	if err := sites.RegisterAll(registry, client, sites.RegistryOptions{
		BaseURLs:            baseURLs,
		Hardworking:         cfg.Crawler.Hardworking,
		FC2FanLocalPath:     fc2fanLocalPath,
		JavDBCookieProvider: browsercookies.NewJavDBProvider(),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "register crawlers: %v\n", err)
		os.Exit(1)
	}
	crawlerTimeout, err := buildCrawlerTimeout(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build crawler timeout: %v\n", err)
		os.Exit(1)
	}
	runner := crawler.NewRunner(registry, crawler.Options{Retry: cfg.Network.Retry, Timeout: crawlerTimeout})
	dataRoot := filepath.Join(resourceRoot, "data")
	genreMaps, err := loadGenreMaps(dataRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load genre maps: %v\n", err)
		os.Exit(1)
	}
	actressAliases := map[string][]string{}
	if cfg.Crawler.NormalizeActressName {
		actressAliases, err = loadActressAliases(filepath.Join(dataRoot, "actress_alias.json"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "load actress aliases: %v\n", err)
			os.Exit(1)
		}
	}
	translator, err := buildTranslator(cfg, client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build translator: %v\n", err)
		os.Exit(1)
	}
	sleepAfter, err := buildCrawlerDelay(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse crawler sleep: %v\n", err)
		os.Exit(1)
	}
	processed := 0
	hadError := false
	claims := newOutputClaims()
	results := runMovieJobs(ctx, len(movies), cfg.Crawler.MovieConcurrency, sleepAfter, func(index int) (bool, error) {
		return processMovie(ctx, &movies[index], runner, client, cfg, genreMaps, actressAliases, translator, resourceRoot, claims)
	})
	for result := range results {
		if result.err != nil {
			hadError = true
			fmt.Fprintf(os.Stderr, "%s: %v\n", movieName(&movies[result.index]), result.err)
		} else if result.success {
			processed++
		} else {
			fmt.Fprintf(os.Stderr, "%s: missing required metadata\n", movieName(&movies[result.index]))
		}
	}
	fmt.Printf("%s processed %d/%d movie(s)\n", appName, processed, len(movies))
	if hadError {
		os.Exit(1)
	}
}

type movieJobResult struct {
	index   int
	success bool
	err     error
}

type movieStartPacer struct {
	mu   sync.Mutex
	next time.Time
}

func (p *movieStartPacer) Wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	p.mu.Lock()
	now := time.Now()
	wait := p.next.Sub(now)
	if wait <= 0 {
		wait = 0
		p.next = now.Add(delay)
	} else {
		p.next = p.next.Add(delay)
	}
	p.mu.Unlock()
	if wait == 0 {
		return ctx.Err()
	}
	if sleepWithContext(ctx, wait) {
		return nil
	}
	return ctx.Err()
}

func runMovieJobs(ctx context.Context, total, concurrency int, delay time.Duration, process func(int) (bool, error)) <-chan movieJobResult {
	results := make(chan movieJobResult, total)
	if total == 0 {
		close(results)
		return results
	}
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > total {
		concurrency = total
	}

	jobs := make(chan int, total)
	for index := 0; index < total; index++ {
		jobs <- index
	}
	close(jobs)

	var workers sync.WaitGroup
	var pacer movieStartPacer
	workers.Add(concurrency)
	for range concurrency {
		go func() {
			defer workers.Done()
			for index := range jobs {
				if err := pacer.Wait(ctx, delay); err != nil {
					results <- movieJobResult{index: index, err: err}
					continue
				}
				success, err := func() (success bool, err error) {
					defer func() {
						if recovered := recover(); recovered != nil {
							err = fmt.Errorf("movie processing panic: %v", recovered)
						}
					}()
					return process(index)
				}()
				results <- movieJobResult{index: index, success: success, err: err}
			}
		}()
	}
	go func() {
		workers.Wait()
		close(results)
	}()
	return results
}

type outputClaims struct {
	mu     sync.Mutex
	owners map[string]string
}

func newOutputClaims() *outputClaims {
	return &outputClaims{owners: make(map[string]string)}
}

func (c *outputClaims) Claim(movie *model.Movie, cfg *config.Config) error {
	paths, err := movieOutputPaths(movie, cfg)
	if err != nil {
		return err
	}
	owner := movieName(movie)
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, path := range paths {
		if existing, ok := c.owners[path]; ok {
			return fmt.Errorf("output path collision: %s is already claimed by %s", path, existing)
		}
	}
	for _, path := range paths {
		c.owners[path] = owner
	}
	return nil
}

func movieOutputPaths(movie *model.Movie, cfg *config.Config) ([]string, error) {
	paths := make([]string, 0, len(movie.Files)+4)
	seen := make(map[string]string)
	add := func(path string, label string) error {
		if path == "" {
			return nil
		}
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
		path = strings.ToLower(filepath.Clean(path))
		if previous, ok := seen[path]; ok {
			return fmt.Errorf("output path collision within movie %s: %s is used by %s and %s", movieName(movie), path, previous, label)
		}
		seen[path] = label
		paths = append(paths, path)
		return nil
	}
	for _, artifact := range []struct {
		path  string
		label string
	}{
		{movie.NFOFile, "NFO"},
		{movie.FanartFile, "fanart"},
		{movie.PosterFile, "poster"},
	} {
		if err := add(artifact.path, artifact.label); err != nil {
			return nil, err
		}
	}
	if cfg.Summarizer.ExtraFanarts.Enabled && movie.Info != nil && len(movie.Info.PreviewPics) > 0 {
		if err := add(filepath.Join(movie.SaveDir, "extrafanart"), "extra fanart directory"); err != nil {
			return nil, err
		}
	}
	if cfg.Summarizer.MoveFiles {
		for index, source := range movie.Files {
			base := movie.BaseName
			if len(movie.Files) > 1 {
				base = fmt.Sprintf("%s-CD%d", base, index+1)
			}
			if err := add(filepath.Join(movie.SaveDir, base+filepath.Ext(source)), fmt.Sprintf("media file %d", index+1)); err != nil {
				return nil, err
			}
		}
	}
	return paths, nil
}

type cliOptions struct {
	configPath      string
	inputPath       string
	showVersion     bool
	configOverrides repeatedFlag
}

func parseCLI(args []string, errorOutput io.Writer) (cliOptions, error) {
	var options cliOptions
	flags := flag.NewFlagSet(appName, flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	flags.StringVar(&options.configPath, "c", "", "path to config.yml")
	flags.StringVar(&options.configPath, "config", "", "path to config.yml")
	flags.StringVar(&options.inputPath, "input", "", "directory to scan; overrides config scanner.input_directory")
	flags.BoolVar(&options.showVersion, "version", false, "print version and exit")
	flags.Var(&options.configOverrides, "o", "override config value as key=value; repeatable")
	if err := flags.Parse(args); err != nil {
		return cliOptions{}, err
	}
	return options, nil
}

func versionText() string {
	return fmt.Sprintf("%s %s (commit %s, built %s)", appName, version, commit, buildDate)
}

type repeatedFlag []string

func (r *repeatedFlag) String() string {
	return strings.Join(*r, ",")
}

func (r *repeatedFlag) Set(value string) error {
	if !strings.Contains(value, "=") {
		return fmt.Errorf("override must be key=value")
	}
	*r = append(*r, value)
	return nil
}

func buildScanRoot(flagValue string, configured *string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if configured != nil && *configured != "" {
		return *configured, nil
	}
	return "", errors.New("scan input directory is required; pass -input or set scanner.input_directory")
}

func selectScanRoot(flagValue string, configured *string, interactive bool, input io.Reader, output io.Writer) (string, error) {
	root, err := buildScanRoot(flagValue, configured)
	if err == nil || !interactive {
		return root, err
	}
	if _, err := fmt.Fprint(output, "Enter an absolute directory to scan: "); err != nil {
		return "", fmt.Errorf("write scan input directory prompt: %w", err)
	}
	line, readErr := bufio.NewReader(input).ReadString('\n')
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "", fmt.Errorf("read scan input directory: %w", readErr)
	}
	root = strings.TrimSpace(line)
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("scan input directory must be an absolute path: %q", root)
	}
	info, statErr := os.Stat(root)
	if statErr != nil {
		return "", fmt.Errorf("scan input directory %q: %w", root, statErr)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("scan input path is not a directory: %q", root)
	}
	return root, nil
}

func resolveOutputFolderPattern(scanRoot, pattern string) (string, error) {
	pattern = strings.TrimSpace(pattern)
	if filepath.IsAbs(pattern) {
		return filepath.Clean(pattern), nil
	}
	absoluteRoot, err := filepath.Abs(scanRoot)
	if err != nil {
		return "", fmt.Errorf("resolve scan root %q: %w", scanRoot, err)
	}
	return filepath.Clean(filepath.Join(absoluteRoot, pattern)), nil
}

func requireRecognizedMovies(movies []model.Movie) error {
	if len(movies) == 0 {
		return errors.New("no movie files found in scan input directory")
	}
	return nil
}

func buildScannerOptions(cfg *config.Config) (scanner.Options, error) {
	minimumSize, err := parseByteSize(cfg.Scanner.MinimumSize)
	if err != nil {
		return scanner.Options{}, err
	}
	return scanner.Options{
		FilenameExtensions:       cfg.Scanner.FilenameExtensions,
		MinimumSize:              minimumSize,
		IgnoredIDPattern:         cfg.Scanner.IgnoredIDPattern,
		IgnoredFolderNamePattern: cfg.Scanner.IgnoredFolderNamePattern,
		SkipNFODir:               cfg.Scanner.SkipNFODir,
	}, nil
}

func parseByteSize(s string) (int64, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, nil
	}
	units := []struct {
		suffix string
		scale  int64
	}{
		{"KiB", 1024},
		{"MiB", 1024 * 1024},
		{"GiB", 1024 * 1024 * 1024},
		{"TiB", 1024 * 1024 * 1024 * 1024},
		{"KB", 1000},
		{"MB", 1000 * 1000},
		{"GB", 1000 * 1000 * 1000},
		{"TB", 1000 * 1000 * 1000 * 1000},
		{"B", 1},
	}
	for _, unit := range units {
		if strings.HasSuffix(trimmed, unit.suffix) {
			value := strings.TrimSpace(strings.TrimSuffix(trimmed, unit.suffix))
			parsed, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return 0, err
			}
			return int64(parsed * float64(unit.scale)), nil
		}
	}
	return strconv.ParseInt(trimmed, 10, 64)
}

func buildHTTPClient(cfg *config.Config) (*httpclient.Client, error) {
	timeout, err := config.ParseISODurationSeconds(cfg.Network.Timeout)
	if err != nil {
		return nil, err
	}
	opts := httpclient.Options{Timeout: timeout}
	if cfg.Network.ProxyServer != nil {
		opts.ProxyURL = *cfg.Network.ProxyServer
	}
	return httpclient.New(opts)
}

func buildTranslator(cfg *config.Config, client *httpclient.Client) (translate.Engine, error) {
	return newEngineFromConfig(cfg.Translator.Engine, client.HTTPClient())
}

type proxyFreeResolver func(context.Context, string, string) string

func buildSiteBaseURLs(ctx context.Context, cfg *config.Config, resolver proxyFreeResolver) map[string]string {
	baseURLs := make(map[string]string, len(cfg.Network.ProxyFree))
	for id, rawURL := range cfg.Network.ProxyFree {
		if rawURL != "" {
			baseURLs[id] = rawURL
		}
	}
	if cfg.Network.ProxyServer != nil {
		delete(baseURLs, "javbus")
		delete(baseURLs, "javdb")
		return baseURLs
	}
	if resolver == nil {
		return baseURLs
	}
	for _, site := range []string{"avsox", "javbus", "javdb", "javlib"} {
		if rawURL := resolver(ctx, site, baseURLs[site]); rawURL != "" {
			baseURLs[site] = rawURL
		}
	}
	return baseURLs
}

func defaultProxyFreeResolver(ctx context.Context, site string, preferURL string) string {
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return proxyfree.Get(lookupCtx, nil, site, preferURL)
}

func buildCrawlerDelay(cfg *config.Config) (time.Duration, error) {
	return config.ParseISODurationSeconds(cfg.Crawler.SleepAfterScraping)
}

func buildCrawlerTimeout(cfg *config.Config) (time.Duration, error) {
	timeout, err := config.ParseISODurationSeconds(cfg.Network.Timeout)
	if err != nil {
		return 0, err
	}
	return time.Duration(cfg.Network.Retry) * timeout, nil
}

func maybePrintUpdateInfo(result updatecheck.Result, output io.Writer) {
	if result.Status == updatecheck.StatusNewVersion || result.Status == updatecheck.StatusFailed {
		_, _ = fmt.Fprint(output, updatecheck.Format(result))
	}
}

type updateChecker func(context.Context, updatecheck.Options) (updatecheck.Result, error)

func checkForUpdates(ctx context.Context, enabled bool, currentVersion string, client *http.Client, check updateChecker, output io.Writer) {
	if !enabled || !shouldCheckForUpdates(currentVersion) || check == nil {
		return
	}
	result, err := check(ctx, updatecheck.Options{CurrentVersion: currentVersion, Client: client})
	if err == nil {
		maybePrintUpdateInfo(result, output)
	}
}

func shouldCheckForUpdates(currentVersion string) bool {
	return currentVersion != "dev"
}

func reviewMovies(root string, movies []model.Movie, input io.Reader, output io.Writer) ([]model.Movie, error) {
	reader := bufio.NewReader(input)
	reviewed := make([]model.Movie, len(movies))
	for i, movie := range movies {
		if _, err := fmt.Fprintf(output, "[%d/%d]\t%s\n", i+1, len(movies), movieName(&movie)); err != nil {
			return nil, fmt.Errorf("write manual review header: %w", err)
		}
		for _, file := range movie.Files {
			display := file
			if rel, err := filepath.Rel(root, file); err == nil {
				display = rel
			}
			if _, err := fmt.Fprintf(output, "  %s\n", display); err != nil {
				return nil, fmt.Errorf("write manual review file: %w", err)
			}
		}
		if _, err := fmt.Fprint(output, "回车确认当前番号，或输入更正后的番号（如 ABC-123 或 cid:sqte00300）: "); err != nil {
			return nil, fmt.Errorf("write manual review prompt: %w", err)
		}
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if errors.Is(err, io.EOF) && len(line) == 0 {
			return nil, errors.New("manual review input closed before confirmation")
		}
		line = strings.TrimSpace(line)
		reviewed[i] = applyManualID(movie, line)
	}
	return reviewed, nil
}

func reviewMoviesForConfig(root string, movies []model.Movie, cfg *config.Config, input io.Reader, output io.Writer) ([]model.Movie, error) {
	if !cfg.Scanner.Manual {
		return movies, nil
	}
	if !cfg.Other.Interactive {
		return nil, errors.New("manual mode requires other.interactive to be true")
	}
	return reviewMovies(root, movies, input, output)
}

func applyManualID(movie model.Movie, value string) model.Movie {
	if value == "" {
		return movie
	}
	lower := strings.ToLower(value)
	out := movie
	out.DVDID = ""
	out.CID = ""
	switch {
	case strings.HasPrefix(lower, "cid:") || strings.HasPrefix(lower, "cid="):
		out.CID = lower[4:]
		out.DataSource = model.DataSourceCID
	case strings.HasPrefix(lower, "fc2"):
		out.DVDID = value
		out.DataSource = model.DataSourceFC2
	default:
		out.DVDID = value
		out.DataSource = model.DataSourceNormal
	}
	return out
}

type movieRunner interface {
	Run(ctx context.Context, ids []model.CrawlerID, input crawler.MovieRef) []crawler.Result
}

func processMovie(ctx context.Context, movie *model.Movie, runner movieRunner, client *httpclient.Client, cfg *config.Config, genreMaps map[string]genre.Map, actressAliases map[string][]string, translator translate.Engine, resourceRoot string, claims *outputClaims) (bool, error) {
	ids := selectCrawlerIDs(movie, cfg)
	results := runner.Run(ctx, ids, crawler.MovieRef{DVDID: movie.DVDID, CID: movie.CID})
	sourceDiagnostics := crawler.Diagnostics(results)
	results = resolveCIDResults(movie, cfg, results)
	mergedResults := make([]summary.Result, 0, len(results))
	for _, result := range results {
		if result.Err != nil || result.Info == nil {
			continue
		}
		mergedResults = append(mergedResults, summary.Result{
			CrawlerID: string(result.CrawlerID),
			Info:      result.Info,
		})
	}

	ok, err := summary.Merge(movie, mergedResults, buildSummaryOptions(cfg, genreMaps, actressAliases))
	if err != nil {
		return ok, err
	}
	if !ok {
		return false, fmt.Errorf("missing required metadata (required: %s); sources: %s", strings.Join(cfg.Crawler.RequiredKeys, ","), sourceDiagnostics)
	}
	if translator != nil {
		ok, err := translate.Apply(ctx, movie.Info, translator, translate.Fields{
			Title: cfg.Translator.Fields.Title,
			Plot:  cfg.Translator.Fields.Plot,
		})
		if err != nil || !ok {
			return ok, err
		}
	}
	if err := organizer.GenerateNames(movie, buildOrganizerOptions(cfg)); err != nil {
		return false, err
	}
	if claims != nil {
		if err := claims.Claim(movie, cfg); err != nil {
			return false, err
		}
	}
	if err := writeMovieArtifacts(ctx, movie, client, cfg, resourceRoot); err != nil {
		return false, err
	}
	return true, nil
}

func selectCrawlerIDs(movie *model.Movie, cfg *config.Config) []model.CrawlerID {
	switch movie.DataSource {
	case model.DataSourceFC2:
		return crawlerIDs(cfg.Crawler.Selection.FC2)
	case model.DataSourceCID:
		ids := crawlerIDs(cfg.Crawler.Selection.CID)
		if movie.DVDID != "" {
			ids = append(ids, crawlerIDs(cfg.Crawler.Selection.Normal)...)
		}
		return ids
	case model.DataSourceGetchu:
		return crawlerIDs(cfg.Crawler.Selection.Getchu)
	case model.DataSourceGyutto:
		return crawlerIDs(cfg.Crawler.Selection.Gyutto)
	default:
		return crawlerIDs(cfg.Crawler.Selection.Normal)
	}
}

func crawlerIDs(values []string) []model.CrawlerID {
	out := make([]model.CrawlerID, 0, len(values))
	for _, value := range values {
		out = append(out, model.CrawlerID(value))
	}
	return out
}

func resolveCIDResults(movie *model.Movie, cfg *config.Config, results []crawler.Result) []crawler.Result {
	if movie.DataSource != model.DataSourceCID || movie.DVDID == "" {
		return results
	}
	cidIDs := make(map[model.CrawlerID]bool)
	for _, id := range crawlerIDs(cfg.Crawler.Selection.CID) {
		cidIDs[id] = true
	}
	cidSucceeded := false
	for _, result := range results {
		if cidIDs[result.CrawlerID] && result.Info != nil && result.Info.Title != "" {
			cidSucceeded = true
			break
		}
	}
	filtered := make([]crawler.Result, 0, len(results))
	if cidSucceeded {
		movie.DVDID = ""
		for _, result := range results {
			if cidIDs[result.CrawlerID] {
				filtered = append(filtered, result)
			}
		}
		return filtered
	}

	movie.DataSource = model.DataSourceNormal
	movie.CID = ""
	for _, result := range results {
		if !cidIDs[result.CrawlerID] {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

func buildSummaryOptions(cfg *config.Config, genreMaps map[string]genre.Map, actressAliases map[string][]string) summary.Options {
	return summary.Options{
		RequiredKeys:            cfg.Crawler.RequiredKeys,
		UseJavDBCover:           cfg.Crawler.UseJavDBCover,
		RespectSiteAvid:         cfg.Crawler.RespectSiteAvid,
		RemoveTrailingActorName: cfg.Summarizer.Title.RemoveTrailingActorName,
		ActressAliases:          actressAliases,
		GenreMaps:               genreMaps,
	}
}

func buildOrganizerOptions(cfg *config.Config) organizer.Options {
	return organizer.Options{
		MoveFiles:             cfg.Summarizer.MoveFiles,
		OutputFolderPattern:   cfg.Summarizer.Path.OutputFolderPattern,
		BasenamePattern:       cfg.Summarizer.Path.BasenamePattern,
		NFOBasenamePattern:    cfg.Summarizer.NFO.BasenamePattern,
		NFOTitlePattern:       cfg.Summarizer.NFO.TitlePattern,
		FanartBasenamePattern: cfg.Summarizer.Fanart.BasenamePattern,
		PosterBasenamePattern: cfg.Summarizer.Cover.BasenamePattern,
		LengthMaximum:         cfg.Summarizer.Path.LengthMaximum,
		LengthByByte:          cfg.Summarizer.Path.LengthByByte,
		MaxActressCount:       cfg.Summarizer.Path.MaxActressCount,
		DefaultTitle:          cfg.Summarizer.Default.Title,
		DefaultActress:        cfg.Summarizer.Default.Actress,
		DefaultSeries:         cfg.Summarizer.Default.Series,
		DefaultDirector:       cfg.Summarizer.Default.Director,
		DefaultProducer:       cfg.Summarizer.Default.Producer,
		DefaultPublisher:      cfg.Summarizer.Default.Publisher,
		CensorText:            cfg.Summarizer.CensorOptionsRepresentation,
	}
}

func buildNFOOptions(cfg *config.Config) nfo.Options {
	return nfo.Options{
		CustomGenresFields: cfg.Summarizer.NFO.CustomGenresFields,
		CustomTagsFields:   cfg.Summarizer.NFO.CustomTagsFields,
		CensorText:         cfg.Summarizer.CensorOptionsRepresentation,
		DefaultTitle:       cfg.Summarizer.Default.Title,
		DefaultActress:     cfg.Summarizer.Default.Actress,
		DefaultSeries:      cfg.Summarizer.Default.Series,
		DefaultDirector:    cfg.Summarizer.Default.Director,
		DefaultProducer:    cfg.Summarizer.Default.Producer,
		DefaultPublisher:   cfg.Summarizer.Default.Publisher,
	}
}

func writeMovieArtifacts(ctx context.Context, movie *model.Movie, client *httpclient.Client, cfg *config.Config, resourceRoot string) error {
	if err := os.MkdirAll(movie.SaveDir, 0o755); err != nil {
		return err
	}
	if err := downloadCoverArtifacts(ctx, client, movie, cfg, resourceRoot); err != nil {
		return err
	}
	if cfg.Summarizer.ExtraFanarts.Enabled {
		if err := downloadExtraFanarts(ctx, client, movie, cfg.Summarizer.ExtraFanarts.ScrapInterval); err != nil {
			return err
		}
	}
	if err := nfo.Write(movie.Info, movie.NFOFile, buildNFOOptions(cfg)); err != nil {
		return err
	}
	if cfg.Summarizer.MoveFiles {
		return organizer.MoveFiles(movie, cfg.Summarizer.Path.HardLink)
	}
	return nil
}

func downloadCoverArtifacts(ctx context.Context, client *httpclient.Client, movie *model.Movie, cfg *config.Config, resourceRoot string) error {
	candidates := coverCandidates(movie.Info, cfg.Summarizer.Cover.HighRes)
	if len(candidates) == 0 {
		return errors.New("download cover: no cover candidates")
	}
	destination := movie.FanartFile
	if destination == "" {
		destination = movie.PosterFile
	}
	if destination == "" {
		return nil
	}

	var lastErr error
	attempts := cfg.Network.Retry
	if attempts <= 0 {
		return errors.New("download cover: network.retry is zero; no download attempts configured")
	}
	for _, source := range candidates {
		downloaded := false
		for attempt := 0; attempt < attempts; attempt++ {
			if err := downloadAndNormalizeCover(ctx, client, source, destination); err != nil {
				lastErr = err
				continue
			}
			downloaded = true
			break
		}
		if !downloaded {
			continue
		}
		if movie.PosterFile != "" {
			opts, err := buildPosterOptions(movie, cfg, resourceRoot)
			if err != nil {
				return fmt.Errorf("configure poster: %w", err)
			}
			if err := poster.Process(destination, movie.PosterFile, opts); err != nil {
				return fmt.Errorf("create poster: %w", err)
			}
		}
		movie.Info.Cover = source
		return nil
	}
	return fmt.Errorf("download cover: %w", lastErr)
}

func downloadAndNormalizeCover(ctx context.Context, client *httpclient.Client, source, destination string) error {
	temp, err := os.CreateTemp(filepath.Dir(destination), ".javsp-cover-*")
	if err != nil {
		return fmt.Errorf("create cover temporary file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close cover temporary file: %w", err)
	}
	if _, err := httpclient.Download(ctx, client, source, tempPath); err != nil {
		return err
	}
	if err := poster.Validate(tempPath); err != nil {
		return fmt.Errorf("validate cover candidate %q: %w", source, err)
	}
	if err := poster.Normalize(tempPath, destination); err != nil {
		return fmt.Errorf("normalize cover candidate %q: %w", source, err)
	}
	return nil
}

func buildPosterOptions(movie *model.Movie, cfg *config.Config, resourceRoot string) (poster.Options, error) {
	attr := ""
	if len(movie.Files) == 1 {
		attr = util.DetectSpecialAttr(movie.Files[0], movie.DVDID)
	}
	imageRoot := filepath.Join(resourceRoot, "image")
	opts := poster.Options{
		AddLabel:     cfg.Summarizer.Cover.AddLabel,
		SubMarkPath:  filepath.Join(imageRoot, "sub_mark.png"),
		UncMarkPath:  filepath.Join(imageRoot, "unc_mark.png"),
		HardSub:      strings.Contains(attr, "C"),
		UncensoredUC: strings.Contains(attr, "U"),
	}
	engine := cropEngineName(cfg.Summarizer.Cover.Crop.Engine)
	if shouldUsePosterFaceCrop(movie, cfg) && (engine == "pigo" || engine == "slimeface") {
		detector, err := poster.NewPigoDetector(filepath.Join(resourceRoot, "data", "facefinder"))
		if err != nil {
			return poster.Options{}, err
		}
		opts.FaceDetector = detector
	}
	return opts, nil
}

func shouldUsePosterFaceCrop(movie *model.Movie, cfg *config.Config) bool {
	if movie == nil || movie.Info == nil || cfg == nil || cfg.Summarizer.Cover.Crop.Engine == nil {
		return false
	}
	if movie.Info.Uncensored != nil && *movie.Info.Uncensored {
		return true
	}
	if movie.DataSource == model.DataSourceFC2 {
		return true
	}
	label := strings.ToUpper(movie.Info.Label)
	for _, pattern := range cfg.Summarizer.Cover.Crop.OnIDPattern {
		anchored, err := regexp.Compile("^(?:" + pattern + ")")
		if err == nil && anchored.MatchString(label) {
			return true
		}
	}
	return false
}

func cropEngineName(raw any) string {
	if raw == nil {
		return ""
	}
	if values, ok := raw.(map[string]any); ok {
		return strings.ToLower(fmt.Sprint(values["name"]))
	}
	if values, ok := raw.(map[interface{}]interface{}); ok {
		if name, ok := values["name"]; ok {
			return strings.ToLower(fmt.Sprint(name))
		}
	}
	return ""
}

func coverCandidates(info *model.MovieInfo, highres bool) []string {
	var candidates []string
	covers := append([]string(nil), info.Covers...)
	if len(covers) == 0 && info.Cover != "" {
		covers = append(covers, info.Cover)
	}
	bigCovers := append([]string(nil), info.BigCovers...)
	if len(bigCovers) == 0 && info.BigCover != "" {
		bigCovers = append(bigCovers, info.BigCover)
	}
	if highres {
		for _, bigCover := range bigCovers {
			if bigCover != "" {
				candidates = appendUniqueString(candidates, bigCover)
			}
		}
	}
	for _, cover := range covers {
		if cover != "" {
			candidates = appendUniqueString(candidates, cover)
		}
	}
	if hasAVEntertainmentCover(candidates) {
		if fallback := dmmCoverFallback(info.DVDID); fallback != "" {
			candidates = appendUniqueString(candidates, fallback)
		}
	}
	return candidates
}

func hasAVEntertainmentCover(candidates []string) bool {
	for _, candidate := range candidates {
		parsed, err := url.Parse(candidate)
		if err != nil {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if host == "aventertainments.com" || strings.HasSuffix(host, ".aventertainments.com") {
			return true
		}
	}
	return false
}

func dmmCoverFallback(dvdID string) string {
	match := regexp.MustCompile(`(?i)^([a-z]+)-(\d+)$`).FindStringSubmatch(strings.TrimSpace(dvdID))
	if match == nil {
		return ""
	}
	number, err := strconv.Atoi(match[2])
	if err != nil {
		return ""
	}
	cid := "1" + strings.ToLower(match[1]) + fmt.Sprintf("%05d", number)
	return "https://pics.dmm.co.jp/digital/video/" + cid + "/" + cid + "pl.jpg"
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func downloadExtraFanarts(ctx context.Context, client *httpclient.Client, movie *model.Movie, intervalText string) error {
	if len(movie.Info.PreviewPics) == 0 {
		return nil
	}
	interval, err := config.ParseISODurationSeconds(intervalText)
	if err != nil {
		return err
	}
	dir := filepath.Join(movie.SaveDir, "extrafanart")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for i, source := range movie.Info.PreviewPics {
		destination := filepath.Join(dir, fmt.Sprintf("%d.png", i))
		temp, err := os.CreateTemp(dir, ".javsp-preview-*")
		if err != nil {
			return fmt.Errorf("create preview temporary file in %s: %w", dir, err)
		}
		tempPath := temp.Name()
		if err := temp.Close(); err != nil {
			_ = os.Remove(tempPath)
			return fmt.Errorf("close preview temporary file %s: %w", tempPath, err)
		}
		if _, err := httpclient.Download(ctx, client, source, tempPath); err == nil {
			if err := poster.Validate(tempPath); err == nil {
				if err := normalizeExtraFanart(tempPath, destination); err != nil {
					_ = os.Remove(tempPath)
					return fmt.Errorf("normalize extra fanart %q to %s: %w", source, destination, err)
				}
			}
		}
		_ = os.Remove(tempPath)
		if interval > 0 && i < len(movie.Info.PreviewPics)-1 {
			if !sleepWithContext(ctx, interval) {
				return ctx.Err()
			}
		}
	}
	return nil
}

func sleepWithContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func movieName(movie *model.Movie) string {
	if movie.DVDID != "" {
		return movie.DVDID
	}
	if movie.CID != "" {
		return movie.CID
	}
	return "<unknown>"
}

var genreMapFiles = map[string]string{
	"avsox":  "genre_avsox.csv",
	"javbus": "genre_javbus.csv",
	"javdb":  "genre_javdb.csv",
	"javlib": "genre_javlib.csv",
}

func loadGenreMaps(root string) (map[string]genre.Map, error) {
	out := make(map[string]genre.Map)
	for crawlerID, filename := range genreMapFiles {
		path := filepath.Join(root, filename)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		mapping, err := genre.Load(path)
		if err != nil {
			return nil, err
		}
		out[crawlerID] = mapping
	}
	return out, nil
}

func loadActressAliases(path string) (map[string][]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string][]string{}, nil
		}
		return nil, err
	}
	var aliases map[string][]string
	if err := json.Unmarshal(data, &aliases); err != nil {
		return nil, err
	}
	if aliases == nil {
		return map[string][]string{}, nil
	}
	return aliases, nil
}
