package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuukiy/javsp-go/internal/config"
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
	"github.com/yuukiy/javsp-go/internal/poster"
	"github.com/yuukiy/javsp-go/internal/translate"
	"github.com/yuukiy/javsp-go/internal/updatecheck"
)

type alwaysFailWriter struct {
	err error
}

func (w alwaysFailWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type failAtWriter struct {
	failAt int
	writes int
	err    error
}

func (w *failAtWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, w.err
	}
	return len(p), nil
}

func TestBuildTranslatorUsesNetworkHTTPClient(t *testing.T) {
	client, err := httpclient.New(httpclient.Options{ProxyURL: "http://proxy.example:8080"})
	if err != nil {
		t.Fatal(err)
	}
	originalBuilder := newEngineFromConfig
	t.Cleanup(func() { newEngineFromConfig = originalBuilder })
	var gotClient *http.Client
	newEngineFromConfig = func(raw any, received *http.Client) (translate.Engine, error) {
		gotClient = received
		return nil, nil
	}

	_, err = buildTranslator(&config.Config{Translator: config.Translator{
		Engine: map[string]any{"name": "google"},
	}}, client)
	if err != nil {
		t.Fatalf("buildTranslator returned error: %v", err)
	}
	if gotClient != client.HTTPClient() {
		t.Fatalf("translator client = %p, want network client %p", gotClient, client.HTTPClient())
	}
}

func TestVersionTextIncludesBuildMetadata(t *testing.T) {
	if appName != "JavSP-Go" || version != "dev" || commit != "unknown" || buildDate != "unknown" {
		t.Fatalf("build metadata = name=%q version=%q commit=%q buildDate=%q", appName, version, commit, buildDate)
	}
	if got, want := versionText(), "JavSP-Go dev (commit unknown, built unknown)"; got != want {
		t.Fatalf("versionText() = %q, want %q", got, want)
	}
}

func TestParseCLIConfigAliases(t *testing.T) {
	for _, name := range []string{"-c", "-config"} {
		t.Run(name, func(t *testing.T) {
			options, err := parseCLI([]string{name, "/tmp/custom.yml"}, io.Discard)
			if err != nil {
				t.Fatalf("parseCLI returned error: %v", err)
			}
			if options.configPath != "/tmp/custom.yml" {
				t.Fatalf("configPath = %q, want /tmp/custom.yml", options.configPath)
			}
		})
	}
}

func TestBuildScanRootRequiresRoot(t *testing.T) {
	_, err := buildScanRoot("", nil)
	if err == nil {
		t.Fatalf("expected error for empty scan root")
	}
}

func TestBuildScanRootPrefersFlag(t *testing.T) {
	configured := "/from/config"
	got, err := buildScanRoot("/from/flag", &configured)
	if err != nil {
		t.Fatalf("buildScanRoot returned error: %v", err)
	}
	if got != "/from/flag" {
		t.Fatalf("root = %q, want /from/flag", got)
	}
}

func TestSelectScanRootPromptsForExistingAbsoluteDirectory(t *testing.T) {
	want := t.TempDir()
	var output strings.Builder
	got, err := selectScanRoot("", nil, true, strings.NewReader(want+"\n"), &output)
	if err != nil {
		t.Fatalf("selectScanRoot returned error: %v", err)
	}
	if got != want {
		t.Fatalf("root = %q, want %q", got, want)
	}
	if !strings.Contains(output.String(), "directory") {
		t.Fatalf("prompt = %q, want directory prompt", output.String())
	}
}

func TestSelectScanRootReturnsContextualPromptWriteError(t *testing.T) {
	wantErr := errors.New("closed output")
	_, err := selectScanRoot("", nil, true, strings.NewReader(t.TempDir()+"\n"), alwaysFailWriter{err: wantErr})
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "write scan input directory prompt") {
		t.Fatalf("error = %v, want contextual prompt-write error wrapping %v", err, wantErr)
	}
}

func TestSelectScanRootRejectsInvalidInteractivePaths(t *testing.T) {
	file := filepath.Join(t.TempDir(), "movie.mp4")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"relative/path\n", file + "\n"} {
		if _, err := selectScanRoot("", nil, true, strings.NewReader(input), io.Discard); err == nil {
			t.Fatalf("selectScanRoot accepted %q", strings.TrimSpace(input))
		}
	}
}

func TestSelectScanRootNonInteractiveRetainsMissingDirectoryError(t *testing.T) {
	_, err := selectScanRoot("", nil, false, strings.NewReader(t.TempDir()+"\n"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "scan input directory is required") {
		t.Fatalf("error = %v, want missing-directory error", err)
	}
}

func TestResolveOutputFolderPatternUsesScanRootForRelativeTemplate(t *testing.T) {
	root := t.TempDir()
	relative := filepath.Join("#整理完成", "{actress}", "[{num}] {title}")

	got, err := resolveOutputFolderPattern(root, relative)
	if err != nil {
		t.Fatalf("resolveOutputFolderPattern returned error: %v", err)
	}
	want := filepath.Join(root, relative)
	if got != want {
		t.Fatalf("resolved pattern = %q, want %q", got, want)
	}

	absolute := filepath.Join(root, "external", "{num}")
	got, err = resolveOutputFolderPattern(filepath.Join(root, "scan"), absolute)
	if err != nil {
		t.Fatalf("resolveOutputFolderPattern absolute returned error: %v", err)
	}
	if got != absolute {
		t.Fatalf("absolute pattern = %q, want unchanged %q", got, absolute)
	}

	got, err = resolveOutputFolderPattern(root, "  "+relative+"  ")
	if err != nil {
		t.Fatalf("resolveOutputFolderPattern spaced relative returned error: %v", err)
	}
	if got != want {
		t.Fatalf("spaced relative pattern = %q, want trimmed %q", got, want)
	}

	got, err = resolveOutputFolderPattern(root, "  "+absolute+"  ")
	if err != nil {
		t.Fatalf("resolveOutputFolderPattern spaced absolute returned error: %v", err)
	}
	if got != absolute {
		t.Fatalf("spaced absolute pattern = %q, want trimmed %q", got, absolute)
	}
}

func TestResolveOutputFolderPatternSupportsRelativeScanRootAndParentSegments(t *testing.T) {
	scanRoot := filepath.Join("relative", "scan")
	pattern := filepath.Join("..", "shared-output", "{num}")
	absoluteRoot, err := filepath.Abs(scanRoot)
	if err != nil {
		t.Fatal(err)
	}

	got, err := resolveOutputFolderPattern(scanRoot, pattern)
	if err != nil {
		t.Fatalf("resolveOutputFolderPattern returned error: %v", err)
	}
	want := filepath.Clean(filepath.Join(absoluteRoot, pattern))
	if got != want {
		t.Fatalf("resolved pattern = %q, want Python-compatible path %q", got, want)
	}
}

func TestShippedOutputPatternResolvesToFinishedFolderInsideScanRoot(t *testing.T) {
	cfg, err := config.Load("../../config.yml")
	if err != nil {
		t.Fatalf("load shipped config: %v", err)
	}
	root := t.TempDir()

	got, err := resolveOutputFolderPattern(root, cfg.Summarizer.Path.OutputFolderPattern)
	if err != nil {
		t.Fatalf("resolveOutputFolderPattern returned error: %v", err)
	}
	wantPrefix := filepath.Join(root, "#整理完成") + string(filepath.Separator)
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("resolved shipped pattern = %q, want prefix %q", got, wantPrefix)
	}
}

func TestReviewMoviesRejectsClosedInputBeforeConfirmation(t *testing.T) {
	movies := []model.Movie{{DVDID: "ABC-123", Files: []string{"/root/ABC-123.mp4"}}}

	_, err := reviewMovies("/root", movies, strings.NewReader(""), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "input closed before confirmation") {
		t.Fatalf("error = %v, want closed-input confirmation error", err)
	}
}

func TestRepeatedFlagRequiresKeyValue(t *testing.T) {
	var values repeatedFlag
	if err := values.Set("network.retry=5"); err != nil {
		t.Fatalf("Set returned error: %v", err)
	}
	if err := values.Set("bad"); err == nil {
		t.Fatalf("expected malformed override to fail")
	}
	if got := values.String(); got != "network.retry=5" {
		t.Fatalf("String = %q", got)
	}
}

func TestBuildScannerOptionsParsesMiB(t *testing.T) {
	cfg := &config.Config{
		Scanner: config.Scanner{
			FilenameExtensions:       []string{".mp4"},
			IgnoredIDPattern:         []string{"1080[Pp]"},
			IgnoredFolderNamePattern: []string{"^\\."},
			MinimumSize:              "232MiB",
			SkipNFODir:               true,
		},
	}
	opts, err := buildScannerOptions(cfg)
	if err != nil {
		t.Fatalf("buildScannerOptions returned error: %v", err)
	}
	if opts.MinimumSize != 232*1024*1024 {
		t.Fatalf("MinimumSize = %d, want %d", opts.MinimumSize, 232*1024*1024)
	}
	if !opts.SkipNFODir {
		t.Fatalf("SkipNFODir = false, want true")
	}
}

func TestBuildCrawlerDelayParsesISO8601(t *testing.T) {
	cfg := &config.Config{Crawler: config.Crawler{SleepAfterScraping: "PT1.5S"}}
	got, err := buildCrawlerDelay(cfg)
	if err != nil {
		t.Fatalf("buildCrawlerDelay returned error: %v", err)
	}
	if got != 1500*time.Millisecond {
		t.Fatalf("delay = %s, want 1.5s", got)
	}
}

func TestReviewMoviesAllowsManualIDCorrections(t *testing.T) {
	movies := []model.Movie{
		{DVDID: "ABC-123", Files: []string{"/root/ABC-123.mp4"}, DataSource: model.DataSourceNormal},
		{DVDID: "DEF-456", Files: []string{"/root/DEF-456.mp4"}, DataSource: model.DataSourceNormal},
		{DVDID: "GHI-789", Files: []string{"/root/GHI-789.mp4"}, DataSource: model.DataSourceNormal},
	}
	input := strings.NewReader("\ncid:newcid\nFC2-12345\n")
	var output strings.Builder

	got, err := reviewMovies("/root", movies, input, &output)
	if err != nil {
		t.Fatalf("reviewMovies returned error: %v", err)
	}
	if got[0].DVDID != "ABC-123" || got[0].DataSource != model.DataSourceNormal {
		t.Fatalf("first movie = %#v", got[0])
	}
	if got[1].CID != "newcid" || got[1].DVDID != "" || got[1].DataSource != model.DataSourceCID {
		t.Fatalf("second movie = %#v", got[1])
	}
	if got[2].DVDID != "FC2-12345" || got[2].DataSource != model.DataSourceFC2 {
		t.Fatalf("third movie = %#v", got[2])
	}
	if !strings.Contains(output.String(), "[1/3]") || !strings.Contains(output.String(), "ABC-123.mp4") {
		t.Fatalf("output = %s", output.String())
	}
}

func TestReviewMoviesReturnsContextualOutputWriteErrors(t *testing.T) {
	movies := []model.Movie{{
		DVDID: "ABC-123",
		Files: []string{"/root/ABC-123.mp4"},
	}}
	for _, test := range []struct {
		name    string
		failAt  int
		context string
	}{
		{name: "header", failAt: 1, context: "write manual review header"},
		{name: "file", failAt: 2, context: "write manual review file"},
		{name: "prompt", failAt: 3, context: "write manual review prompt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			wantErr := errors.New("closed output")
			output := &failAtWriter{failAt: test.failAt, err: wantErr}
			_, err := reviewMovies("/root", movies, strings.NewReader("\n"), output)
			if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), test.context) {
				t.Fatalf("error = %v, want %q wrapping %v", err, test.context, wantErr)
			}
		})
	}
}

func TestReviewMoviesForConfigRejectsManualNonInteractive(t *testing.T) {
	cfg := &config.Config{Scanner: config.Scanner{Manual: true}}
	_, err := reviewMoviesForConfig("/root", []model.Movie{{DVDID: "ABC-123"}}, cfg, strings.NewReader("\n"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "manual mode") || !strings.Contains(err.Error(), "interactive") {
		t.Fatalf("error = %v, want clear manual interactive error", err)
	}
}

func TestReviewMoviesForConfigConsumesRedirectedInput(t *testing.T) {
	cfg := &config.Config{
		Scanner: config.Scanner{Manual: true},
		Other:   config.Other{Interactive: true},
	}
	movies := []model.Movie{{DVDID: "ABC-123", DataSource: model.DataSourceNormal}}
	got, err := reviewMoviesForConfig("/root", movies, cfg, strings.NewReader("cid:SQTE00300\n"), io.Discard)
	if err != nil {
		t.Fatalf("reviewMoviesForConfig returned error: %v", err)
	}
	if got[0].CID != "sqte00300" || got[0].DVDID != "" || got[0].DataSource != model.DataSourceCID {
		t.Fatalf("reviewed movie = %#v", got[0])
	}
}

func TestApplyManualIDLowercasesCID(t *testing.T) {
	got := applyManualID(model.Movie{DVDID: "ABC-123"}, "cid:SQTE00300")
	if got.CID != "sqte00300" || got.DVDID != "" || got.DataSource != model.DataSourceCID {
		t.Fatalf("applyManualID result = %#v", got)
	}
}

func TestRequireRecognizedMoviesRejectsEmptyScan(t *testing.T) {
	err := requireRecognizedMovies(nil)
	if err == nil || !strings.Contains(err.Error(), "no movie files found") {
		t.Fatalf("error = %v, want no-movies error", err)
	}
}

func TestBuildCrawlerTimeoutMultipliesRetryAndNetworkTimeout(t *testing.T) {
	cfg := &config.Config{Network: config.Network{Retry: 3, Timeout: "PT2S"}}
	got, err := buildCrawlerTimeout(cfg)
	if err != nil {
		t.Fatalf("buildCrawlerTimeout returned error: %v", err)
	}
	if got != 6*time.Second {
		t.Fatalf("timeout = %s, want 6s", got)
	}
}

func TestMaybePrintUpdateInfo(t *testing.T) {
	var out strings.Builder
	maybePrintUpdateInfo(updatecheck.Result{
		Status:         updatecheck.StatusNewVersion,
		CurrentVersion: appName,
		LatestVersion:  "v9.9.9",
		ReleaseURL:     "https://example.com/latest",
		ChangeLog:      []string{"- Fix"},
	}, &out)
	if !strings.Contains(out.String(), "v9.9.9") || !strings.Contains(out.String(), "- Fix") {
		t.Fatalf("output = %s", out.String())
	}

	out.Reset()
	maybePrintUpdateInfo(updatecheck.Result{Status: updatecheck.StatusAlreadyLatest}, &out)
	if out.Len() != 0 {
		t.Fatalf("already latest output = %s", out.String())
	}
}

func TestCheckForUpdatesPassesBuildVersion(t *testing.T) {
	var got updatecheck.Options
	client := &http.Client{}
	checkForUpdates(context.Background(), true, "v1.2.3", client, func(_ context.Context, opts updatecheck.Options) (updatecheck.Result, error) {
		got = opts
		return updatecheck.Result{}, nil
	}, io.Discard)

	if got.CurrentVersion != "v1.2.3" {
		t.Fatalf("CurrentVersion = %q, want build version", got.CurrentVersion)
	}
	if got.Client != client {
		t.Fatalf("Client = %p, want shared proxy-aware client %p", got.Client, client)
	}
}

func TestCheckForUpdatesSkipsDevelopmentBuild(t *testing.T) {
	called := false
	checkForUpdates(context.Background(), true, "dev", nil, func(context.Context, updatecheck.Options) (updatecheck.Result, error) {
		called = true
		return updatecheck.Result{}, nil
	}, io.Discard)

	if called {
		t.Fatal("development build should not invoke the update checker")
	}
}

func TestCoverCandidatesExhaustsHighResolutionBeforeNormalCovers(t *testing.T) {
	info := &model.MovieInfo{
		BigCovers: []string{"https://example.com/big-1.jpg", "https://example.com/big-2.jpg"},
		Covers:    []string{"https://example.com/normal-1.jpg", "https://example.com/normal-2.jpg"},
	}

	got := coverCandidates(info, true)
	want := []string{
		"https://example.com/big-1.jpg",
		"https://example.com/big-2.jpg",
		"https://example.com/normal-1.jpg",
		"https://example.com/normal-2.jpg",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("coverCandidates = %#v, want %#v", got, want)
	}
}

func TestCoverCandidatesAddsDMMFallbackForStandardDVDID(t *testing.T) {
	info := &model.MovieInfo{
		DVDID: "SKYHD-086",
		Cover: "https://imgs02.aventertainments.com/new/bigcover/dvd1skyhd-086.webp",
	}

	got := coverCandidates(info, true)
	want := []string{
		"https://imgs02.aventertainments.com/new/bigcover/dvd1skyhd-086.webp",
		"https://pics.dmm.co.jp/digital/video/1skyhd00086/1skyhd00086pl.jpg",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("coverCandidates = %#v, want %#v", got, want)
	}
}

func TestBuildSiteBaseURLsRefreshesProxyFreeWithoutProxy(t *testing.T) {
	cfg := &config.Config{
		Network: config.Network{
			ProxyFree: map[string]string{
				"avsox":  "https://configured.avsox",
				"javbus": "https://configured.bus",
				"javdb":  "https://configured.db",
				"javlib": "https://configured.lib",
			},
		},
	}
	calls := map[string]string{}
	got := buildSiteBaseURLs(context.Background(), cfg, func(_ context.Context, site string, preferURL string) string {
		calls[site] = preferURL
		if site == "javbus" {
			return "https://fresh.bus"
		}
		return ""
	})

	if got["javbus"] != "https://fresh.bus" {
		t.Fatalf("javbus baseURL = %q, want fresh mirror", got["javbus"])
	}
	if got["javdb"] != "https://configured.db" {
		t.Fatalf("javdb baseURL = %q, want configured mirror", got["javdb"])
	}
	if cfg.Network.ProxyFree["javbus"] != "https://configured.bus" {
		t.Fatalf("buildSiteBaseURLs mutated config map")
	}
	if !reflect.DeepEqual(calls, map[string]string{
		"avsox":  "https://configured.avsox",
		"javbus": "https://configured.bus",
		"javdb":  "https://configured.db",
		"javlib": "https://configured.lib",
	}) {
		t.Fatalf("resolver calls = %#v", calls)
	}
}

func TestBuildSiteBaseURLsUsesPermanentJavBusAndJavDBWithProxy(t *testing.T) {
	proxyServer := "http://127.0.0.1:1080"
	cfg := &config.Config{
		Network: config.Network{
			ProxyServer: &proxyServer,
			ProxyFree: map[string]string{
				"avsox":  "https://configured.avsox",
				"javbus": "https://configured.bus",
				"javdb":  "https://configured.db",
				"javlib": "https://configured.lib",
			},
		},
	}
	called := false
	got := buildSiteBaseURLs(context.Background(), cfg, func(context.Context, string, string) string {
		called = true
		return ""
	})

	if called {
		t.Fatalf("resolver should not be called when a proxy server is configured")
	}
	if _, ok := got["javbus"]; ok {
		t.Fatalf("javbus proxy-free URL should be disabled when proxy server is configured")
	}
	if _, ok := got["javdb"]; ok {
		t.Fatalf("javdb proxy-free URL should be disabled when proxy server is configured")
	}
	if got["avsox"] != "https://configured.avsox" || got["javlib"] != "https://configured.lib" {
		t.Fatalf("baseURLs = %#v", got)
	}
}

func TestShouldUsePosterFaceCrop(t *testing.T) {
	cfg := &config.Config{
		Summarizer: config.Summarizer{
			Cover: config.CoverSummarizer{
				Crop: config.CoverCrop{
					Engine:      map[string]any{"name": "slimeface"},
					OnIDPattern: []string{"^ABC"},
				},
			},
		},
	}
	movie := &model.Movie{
		DVDID:      "ABC-123",
		DataSource: model.DataSourceNormal,
		Info:       &model.MovieInfo{Label: "ABC"},
	}
	if !shouldUsePosterFaceCrop(movie, cfg) {
		t.Fatalf("expected label pattern to enable face crop")
	}
	movie.Info.Label = "XYZ"
	uncensored := true
	movie.Info.Uncensored = &uncensored
	if !shouldUsePosterFaceCrop(movie, cfg) {
		t.Fatalf("expected uncensored movie to enable face crop")
	}
	movie.Info.Uncensored = nil
	movie.DataSource = model.DataSourceFC2
	if !shouldUsePosterFaceCrop(movie, cfg) {
		t.Fatalf("expected fc2 movie to enable face crop")
	}
	cfg.Summarizer.Cover.Crop.Engine = nil
	if shouldUsePosterFaceCrop(movie, cfg) {
		t.Fatalf("nil crop engine should disable face crop")
	}
}

func TestShouldUsePosterFaceCropMatchesOnlyAtLabelStart(t *testing.T) {
	cfg := &config.Config{
		Summarizer: config.Summarizer{
			Cover: config.CoverSummarizer{
				Crop: config.CoverCrop{
					Engine:      map[string]any{"name": "pigo"},
					OnIDPattern: []string{"ARA"},
				},
			},
		},
	}
	movie := &model.Movie{
		DataSource: model.DataSourceNormal,
		Info:       &model.MovieInfo{Label: "XARA"},
	}

	if shouldUsePosterFaceCrop(movie, cfg) {
		t.Fatal("pattern matched in the middle of the label; Python re.match semantics require a start match")
	}
	movie.Info.Label = "ARA"
	if !shouldUsePosterFaceCrop(movie, cfg) {
		t.Fatal("pattern did not match at the start of the label")
	}
}

func TestShouldUsePosterFaceCropIgnoresInvalidPattern(t *testing.T) {
	cfg := &config.Config{
		Summarizer: config.Summarizer{
			Cover: config.CoverSummarizer{
				Crop: config.CoverCrop{
					Engine:      map[string]any{"name": "pigo"},
					OnIDPattern: []string{"[", "^ABC"},
				},
			},
		},
	}
	movie := &model.Movie{
		DataSource: model.DataSourceNormal,
		Info:       &model.MovieInfo{Label: "ABC"},
	}

	if !shouldUsePosterFaceCrop(movie, cfg) {
		t.Fatal("invalid pattern prevented a later valid pattern from matching")
	}
}

func TestBuildPosterOptionsUsesResourceRoot(t *testing.T) {
	resourceRoot := t.TempDir()
	opts, err := buildPosterOptions(&model.Movie{}, &config.Config{}, resourceRoot)
	if err != nil {
		t.Fatalf("buildPosterOptions returned error: %v", err)
	}

	if want := filepath.Join(resourceRoot, "image", "sub_mark.png"); opts.SubMarkPath != want {
		t.Fatalf("SubMarkPath = %q, want %q", opts.SubMarkPath, want)
	}
	if want := filepath.Join(resourceRoot, "image", "unc_mark.png"); opts.UncMarkPath != want {
		t.Fatalf("UncMarkPath = %q, want %q", opts.UncMarkPath, want)
	}
}

func TestBuildPosterOptionsUsesPigoForNativeAndLegacyEngineNames(t *testing.T) {
	uncensored := true
	movie := &model.Movie{Info: &model.MovieInfo{Uncensored: &uncensored}}
	for _, name := range []string{"pigo", "slimeface"} {
		t.Run(name, func(t *testing.T) {
			cfg := &config.Config{Summarizer: config.Summarizer{Cover: config.CoverSummarizer{
				Crop: config.CoverCrop{Engine: map[string]any{"name": name}},
			}}}
			opts, err := buildPosterOptions(movie, cfg, filepath.Join("..", ".."))
			if err != nil {
				t.Fatalf("buildPosterOptions returned error: %v", err)
			}
			if _, ok := opts.FaceDetector.(*poster.PigoDetector); !ok {
				t.Fatalf("FaceDetector = %T, want *poster.PigoDetector", opts.FaceDetector)
			}
		})
	}
}

func TestBuildPosterOptionsReportsInvalidCascade(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "facefinder"), []byte("invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	uncensored := true
	movie := &model.Movie{Info: &model.MovieInfo{Uncensored: &uncensored}}
	cfg := &config.Config{Summarizer: config.Summarizer{Cover: config.CoverSummarizer{
		Crop: config.CoverCrop{Engine: map[string]any{"name": "pigo"}},
	}}}
	if _, err := buildPosterOptions(movie, cfg, root); err == nil {
		t.Fatal("buildPosterOptions accepted invalid facefinder cascade")
	}
}

func TestSelectCrawlerIDsUsesMovieDataSource(t *testing.T) {
	cfg := &config.Config{
		Crawler: config.Crawler{
			Selection: config.CrawlerSelection{
				Normal: []string{"javbus", "javdb"},
				FC2:    []string{"fc2"},
				CID:    []string{"fanza"},
				Getchu: []string{"dl_getchu"},
				Gyutto: []string{"gyutto"},
			},
		},
	}
	cases := []struct {
		src  model.DataSource
		want []model.CrawlerID
	}{
		{model.DataSourceNormal, []model.CrawlerID{"javbus", "javdb"}},
		{model.DataSourceFC2, []model.CrawlerID{"fc2"}},
		{model.DataSourceCID, []model.CrawlerID{"fanza"}},
		{model.DataSourceGetchu, []model.CrawlerID{"dl_getchu"}},
		{model.DataSourceGyutto, []model.CrawlerID{"gyutto"}},
	}
	for _, tc := range cases {
		movie := model.Movie{DataSource: tc.src}
		if got := selectCrawlerIDs(&movie, cfg); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("selectCrawlerIDs(%s) = %#v, want %#v", tc.src, got, tc.want)
		}
	}

	cidWithDVDID := model.Movie{DataSource: model.DataSourceCID, CID: "cid001", DVDID: "ABC-123"}
	if got := selectCrawlerIDs(&cidWithDVDID, cfg); !reflect.DeepEqual(got, []model.CrawlerID{"fanza", "javbus", "javdb"}) {
		t.Fatalf("selectCrawlerIDs(cid+dvdid) = %#v", got)
	}
}

func TestProcessMovieWritesNFOAndPoster(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "ABC-123.mp4")
	cover := filepath.Join(root, "cover.jpg")
	preview := filepath.Join(root, "preview.jpg")
	if err := os.WriteFile(source, []byte("movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestPNG(t, cover, image.Rect(0, 0, 40, 20), color.RGBA{R: 10, G: 20, B: 30, A: 255})
	writeTestPNG(t, preview, image.Rect(0, 0, 12, 8), color.RGBA{R: 80, G: 60, B: 40, A: 255})
	cfg := &config.Config{
		Network: config.Network{Retry: 1},
		Crawler: config.Crawler{
			Selection:       config.CrawlerSelection{Normal: []string{"javbus", "javdb"}},
			RequiredKeys:    []string{"cover", "title"},
			RespectSiteAvid: true,
			UseJavDBCover:   "fallback",
		},
		Summarizer: config.Summarizer{
			MoveFiles: false,
			Path: config.PathSummarizer{
				OutputFolderPattern: filepath.Join(root, "out", "{num}"),
				BasenamePattern:     "{num}",
				MaxActressCount:     10,
			},
			NFO: config.NFOSummarizer{
				BasenamePattern:    "movie",
				TitlePattern:       "{num} {title}",
				CustomGenresFields: []string{"{genre}"},
				CustomTagsFields:   []string{"{censor}"},
			},
			Fanart: config.FanartSummarizer{BasenamePattern: "fanart"},
			Cover:  config.CoverSummarizer{BasenamePattern: "poster"},
			ExtraFanarts: config.ExtraFanartSummarizer{
				Enabled: true,
			},
			Default: config.DefaultValues{
				Title:     "#Unknown Title",
				Actress:   "#Unknown Actress",
				Series:    "#Unknown Series",
				Director:  "#Unknown Director",
				Producer:  "#Unknown Producer",
				Publisher: "#Unknown Publisher",
			},
			Title:                       config.TitleSummarizer{RemoveTrailingActorName: true},
			CensorOptionsRepresentation: []string{"无码", "有码", "打码情况未知"},
		},
		Translator: config.Translator{
			Fields: config.TranslateFields{Title: true},
		},
	}
	runner := &fakeMovieRunner{
		results: []crawler.Result{
			{
				CrawlerID: "javbus",
				Info: &model.MovieInfo{
					DVDID: "ABC-123",
					Cover: cover,
					Title: "Title",
					Genre: []string{"Drama"},
					PreviewPics: []string{
						preview,
					},
				},
			},
		},
	}
	movie := &model.Movie{
		DVDID:      "ABC-123",
		Files:      []string{source},
		DataSource: model.DataSourceNormal,
	}
	ok, err := processMovie(context.Background(), movie, runner, nil, cfg, nil, nil, fakeTranslator{}, root, nil)
	if err != nil {
		t.Fatalf("processMovie returned error: %v", err)
	}
	if !ok {
		t.Fatalf("processMovie ok = false")
	}
	if !reflect.DeepEqual(runner.ids, []model.CrawlerID{"javbus", "javdb"}) {
		t.Fatalf("runner ids = %#v", runner.ids)
	}
	if _, err := os.Stat(filepath.Join(root, "movie.nfo")); err != nil {
		t.Fatalf("movie.nfo missing: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "movie.nfo")); err != nil {
		t.Fatalf("read movie.nfo: %v", err)
	} else if !strings.Contains(string(data), "ABC-123 Translated Title") || !strings.Contains(string(data), "<originaltitle>Title</originaltitle>") {
		t.Fatalf("movie.nfo = %s", string(data))
	}
	if _, err := os.Stat(filepath.Join(root, "poster.jpg")); err != nil {
		t.Fatalf("poster missing: %v", err)
	}
	if got := pngBounds(t, filepath.Join(root, "extrafanart", "0.png")); got.Dx() != 12 || got.Dy() != 8 {
		t.Fatalf("extrafanart bounds = %v, want 12x8", got)
	}
}

func TestWriteMovieArtifactsFallsBackToBigCoverAndCreatesPoster(t *testing.T) {
	root := t.TempDir()
	bigCover := filepath.Join(root, "big.png")
	writeTestPNG(t, bigCover, image.Rect(0, 0, 400, 200), color.RGBA{R: 10, G: 20, B: 30, A: 255})
	movie := &model.Movie{
		SaveDir:    root,
		NFOFile:    filepath.Join(root, "movie.nfo"),
		FanartFile: filepath.Join(root, "fanart.png"),
		PosterFile: filepath.Join(root, "poster.png"),
		Info: &model.MovieInfo{
			DVDID:     "ABC-123",
			Title:     "Title",
			Cover:     filepath.Join(root, "missing.jpg"),
			Covers:    []string{filepath.Join(root, "missing.jpg"), filepath.Join(root, "cover2.jpg")},
			BigCovers: []string{bigCover},
		},
	}
	cfg := &config.Config{
		Network: config.Network{Retry: 1},
		Summarizer: config.Summarizer{
			Cover: config.CoverSummarizer{HighRes: true},
		},
	}

	if err := writeMovieArtifacts(context.Background(), movie, nil, cfg, root); err != nil {
		t.Fatalf("writeMovieArtifacts returned error: %v", err)
	}
	if got := pngBounds(t, movie.FanartFile); got.Dx() != 400 || got.Dy() != 200 {
		t.Fatalf("fanart bounds = %v, want 400x200", got)
	}
	if got := pngBounds(t, movie.PosterFile); got.Dx() != 140 || got.Dy() != 200 {
		t.Fatalf("poster bounds = %v, want 140x200", got)
	}
	if movie.Info.Cover != bigCover {
		t.Fatalf("Info.Cover = %q, want big cover source", movie.Info.Cover)
	}
}

func TestWriteMovieArtifactsInvalidCoverLeavesNoNFO(t *testing.T) {
	root := t.TempDir()
	invalidCover := filepath.Join(root, "invalid-cover.jpg")
	if err := os.WriteFile(invalidCover, []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	movie := &model.Movie{
		SaveDir:    root,
		NFOFile:    filepath.Join(root, "movie.nfo"),
		FanartFile: filepath.Join(root, "fanart.jpg"),
		PosterFile: filepath.Join(root, "poster.jpg"),
		Info: &model.MovieInfo{
			DVDID: "ABC-123",
			Title: "Title",
			Cover: invalidCover,
		},
	}

	if err := writeMovieArtifacts(context.Background(), movie, nil, &config.Config{Network: config.Network{Retry: 1}}, root); err == nil {
		t.Fatal("writeMovieArtifacts returned nil error for invalid cover")
	}
	if _, err := os.Stat(movie.NFOFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("NFO exists after cover failure or stat failed unexpectedly: %v", err)
	}
}

func TestDownloadCoverArtifactsFailsWithoutCoverCandidates(t *testing.T) {
	root := t.TempDir()
	movie := &model.Movie{
		FanartFile: filepath.Join(root, "fanart.jpg"),
		PosterFile: filepath.Join(root, "poster.jpg"),
		Info:       &model.MovieInfo{DVDID: "ABC-123", Title: "Title"},
	}

	err := downloadCoverArtifacts(context.Background(), nil, movie, &config.Config{Network: config.Network{Retry: 1}}, root)
	if err == nil || !strings.Contains(err.Error(), "no cover candidates") {
		t.Fatalf("error = %v, want no-cover-candidates error", err)
	}
}

func TestRunMovieJobsHonorsConcurrency(t *testing.T) {
	const total = 6
	started := make(chan struct{}, total)
	release := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	process := func(int) (bool, error) {
		current := active.Add(1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return true, nil
	}

	results := runMovieJobs(context.Background(), total, 3, 0, process)
	for range 3 {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("three workers did not start")
		}
	}
	select {
	case <-started:
		close(release)
		t.Fatal("more than three jobs started concurrently")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)

	count := 0
	for result := range results {
		if result.err != nil || !result.success {
			t.Fatalf("result = %#v", result)
		}
		count++
	}
	if count != total || maximum.Load() != 3 {
		t.Fatalf("count = %d, max concurrency = %d; want %d and 3", count, maximum.Load(), total)
	}
}

func TestRunMovieJobsSpacesStartsGlobally(t *testing.T) {
	const delay = 50 * time.Millisecond
	started := make(chan time.Time, 2)
	results := runMovieJobs(context.Background(), 2, 2, delay, func(int) (bool, error) {
		started <- time.Now()
		return true, nil
	})

	first := <-started
	second := <-started
	for result := range results {
		if result.err != nil || !result.success {
			t.Fatalf("result = %#v", result)
		}
	}
	if spacing := second.Sub(first); spacing < 40*time.Millisecond {
		t.Fatalf("movie starts spaced by %s, want approximately %s", spacing, delay)
	}
}

func TestOutputClaimsRejectConflictingArtifactsBeforeWrites(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{Summarizer: config.Summarizer{MoveFiles: true}}
	first := &model.Movie{
		DVDID:      "ABC-123",
		Files:      []string{filepath.Join(root, "first.mp4")},
		SaveDir:    filepath.Join(root, "output"),
		BaseName:   "movie",
		NFOFile:    filepath.Join(root, "output", "movie.nfo"),
		FanartFile: filepath.Join(root, "output", "fanart.jpg"),
		PosterFile: filepath.Join(root, "output", "poster.jpg"),
	}
	second := *first
	second.DVDID = "XYZ-999"
	second.Files = []string{filepath.Join(root, "second.mp4")}
	claims := newOutputClaims()
	if err := claims.Claim(first, cfg); err != nil {
		t.Fatalf("first claim returned error: %v", err)
	}
	if err := claims.Claim(&second, cfg); err == nil || !strings.Contains(err.Error(), "output path collision") {
		t.Fatalf("second claim error = %v, want output path collision", err)
	}
}

func TestOutputClaimsTreatPathsCaseInsensitively(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{}
	first := &model.Movie{
		DVDID:   "ABC-123",
		NFOFile: filepath.Join(root, "Output", "Movie.nfo"),
	}
	second := &model.Movie{
		DVDID:   "XYZ-999",
		NFOFile: filepath.Join(root, "output", "movie.nfo"),
	}
	claims := newOutputClaims()
	if err := claims.Claim(first, cfg); err != nil {
		t.Fatal(err)
	}
	if err := claims.Claim(second, cfg); err == nil || !strings.Contains(err.Error(), "output path collision") {
		t.Fatalf("case-folded claim error = %v, want output path collision", err)
	}
}

func TestOutputClaimsRejectConflictsWithinMovie(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "artwork.jpg")
	movie := &model.Movie{
		DVDID:      "ABC-123",
		FanartFile: shared,
		PosterFile: shared,
	}
	err := newOutputClaims().Claim(movie, &config.Config{})
	if err == nil || !strings.Contains(err.Error(), "within movie") {
		t.Fatalf("claim error = %v, want within movie collision", err)
	}
}

func TestBuildNFOOptionsIncludesConfiguredDefaults(t *testing.T) {
	cfg := &config.Config{Summarizer: config.Summarizer{Default: config.DefaultValues{
		Title:     "title fallback",
		Actress:   "actress fallback",
		Series:    "series fallback",
		Director:  "director fallback",
		Producer:  "producer fallback",
		Publisher: "publisher fallback",
	}}}

	got := buildNFOOptions(cfg)
	if got.DefaultTitle != cfg.Summarizer.Default.Title ||
		got.DefaultActress != cfg.Summarizer.Default.Actress ||
		got.DefaultSeries != cfg.Summarizer.Default.Series ||
		got.DefaultDirector != cfg.Summarizer.Default.Director ||
		got.DefaultProducer != cfg.Summarizer.Default.Producer ||
		got.DefaultPublisher != cfg.Summarizer.Default.Publisher {
		t.Fatalf("buildNFOOptions defaults = %#v, want %#v", got, cfg.Summarizer.Default)
	}
}

func TestDownloadCoverArtifactsRejectsInvalidCandidateAndNormalizesFormats(t *testing.T) {
	root := t.TempDir()
	invalid := filepath.Join(root, "invalid.jpg")
	valid := filepath.Join(root, "valid.image")
	if err := os.WriteFile(invalid, []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestPNG(t, valid, image.Rect(0, 0, 80, 40), color.RGBA{R: 10, G: 20, B: 30, A: 255})
	movie := &model.Movie{
		FanartFile: filepath.Join(root, "fanart.jpg"),
		PosterFile: filepath.Join(root, "poster.png"),
		Info:       &model.MovieInfo{Covers: []string{invalid, valid}},
	}
	cfg := &config.Config{Network: config.Network{Retry: 1}}

	if err := downloadCoverArtifacts(context.Background(), nil, movie, cfg, root); err != nil {
		t.Fatalf("downloadCoverArtifacts returned error: %v", err)
	}
	if got := decodedImageFormat(t, movie.FanartFile); got != "jpeg" {
		t.Fatalf("fanart format = %q, want jpeg", got)
	}
	if got := decodedImageFormat(t, movie.PosterFile); got != "png" {
		t.Fatalf("poster format = %q, want png", got)
	}
	if movie.Info.Cover != valid {
		t.Fatalf("Info.Cover = %q, want %q", movie.Info.Cover, valid)
	}
	if matches, err := filepath.Glob(filepath.Join(root, ".javsp-cover-*")); err != nil {
		t.Fatal(err)
	} else if len(matches) != 0 {
		t.Fatalf("cover temporary files remain: %v", matches)
	}
}

func TestDownloadCoverArtifactsRetriesTransientCandidateFailure(t *testing.T) {
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 80, 40))); err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageBytes.Bytes())
	}))
	defer server.Close()

	root := t.TempDir()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	movie := &model.Movie{
		FanartFile: filepath.Join(root, "fanart.jpg"),
		PosterFile: filepath.Join(root, "poster.jpg"),
		Info:       &model.MovieInfo{Covers: []string{server.URL + "/cover"}},
	}
	cfg := &config.Config{Network: config.Network{Retry: 3}}

	if err := downloadCoverArtifacts(context.Background(), client, movie, cfg, root); err != nil {
		t.Fatalf("downloadCoverArtifacts returned error: %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
	if got := decodedImageFormat(t, movie.FanartFile); got != "jpeg" {
		t.Fatalf("fanart format = %q, want jpeg", got)
	}
}

func TestDownloadExtraFanartsRejectsInvalidPreservesExistingAndContinues(t *testing.T) {
	root := t.TempDir()
	invalid := filepath.Join(root, "invalid.preview")
	valid := filepath.Join(root, "valid.preview")
	if err := os.WriteFile(invalid, []byte("truncated"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestJPEG(t, valid, image.Rect(0, 0, 18, 10), color.RGBA{R: 50, G: 70, B: 90, A: 255})
	extraDir := filepath.Join(root, "extrafanart")
	if err := os.MkdirAll(extraDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existingOutput := filepath.Join(extraDir, "0.png")
	writeTestPNG(t, existingOutput, image.Rect(0, 0, 9, 7), color.RGBA{R: 120, G: 30, B: 60, A: 255})
	wantExisting, err := os.ReadFile(existingOutput)
	if err != nil {
		t.Fatal(err)
	}
	movie := &model.Movie{
		SaveDir: root,
		Info:    &model.MovieInfo{PreviewPics: []string{invalid, valid}},
	}

	if err := downloadExtraFanarts(context.Background(), nil, movie, ""); err != nil {
		t.Fatalf("downloadExtraFanarts returned error: %v", err)
	}
	gotExisting, err := os.ReadFile(existingOutput)
	if err != nil {
		t.Fatalf("read existing preview after rejected replacement: %v", err)
	}
	if !bytes.Equal(gotExisting, wantExisting) {
		t.Fatal("rejected preview changed existing extrafanart/0.png bytes")
	}
	validOutput := filepath.Join(root, "extrafanart", "1.png")
	if got := decodedImageFormat(t, validOutput); got != "png" {
		t.Fatalf("valid preview format = %q, want png", got)
	}
	if got := pngBounds(t, validOutput); got.Dx() != 18 || got.Dy() != 10 {
		t.Fatalf("valid preview bounds = %v, want 18x10", got)
	}
	if matches, err := filepath.Glob(filepath.Join(root, "extrafanart", ".javsp-preview-*")); err != nil {
		t.Fatal(err)
	} else if len(matches) != 0 {
		t.Fatalf("preview temporary files remain: %v", matches)
	}
}

func TestDownloadExtraFanartsReturnsContextForNormalizeFailure(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "valid.preview")
	writeTestJPEG(t, source, image.Rect(0, 0, 18, 10), color.RGBA{R: 50, G: 70, B: 90, A: 255})
	movie := &model.Movie{
		SaveDir: root,
		Info:    &model.MovieInfo{PreviewPics: []string{source}},
	}
	originalNormalize := normalizeExtraFanart
	t.Cleanup(func() { normalizeExtraFanart = originalNormalize })
	normalizeExtraFanart = func(string, string) error {
		return errors.New("publish failed")
	}

	err := downloadExtraFanarts(context.Background(), nil, movie, "")
	if err == nil {
		t.Fatal("downloadExtraFanarts returned nil error for normalize failure")
	}
	if message := err.Error(); !strings.Contains(message, "normalize extra fanart") || !strings.Contains(message, source) || !strings.Contains(message, "publish failed") {
		t.Fatalf("normalize error = %q, want operation, source, and cause", message)
	}
	if matches, globErr := filepath.Glob(filepath.Join(root, "extrafanart", ".javsp-preview-*")); globErr != nil {
		t.Fatal(globErr)
	} else if len(matches) != 0 {
		t.Fatalf("preview temporary files remain after normalize failure: %v", matches)
	}
}

func TestProcessMovieCIDFallsBackToNormalWhenCIDCrawlerHasNoTitle(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "cid001.mp4")
	cover := filepath.Join(root, "cover.png")
	if err := os.WriteFile(source, []byte("movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestPNG(t, cover, image.Rect(0, 0, 40, 20), color.RGBA{R: 10, G: 20, B: 30, A: 255})
	cfg := testProcessConfig(root)
	cfg.Crawler.Selection = config.CrawlerSelection{
		CID:    []string{"fanza"},
		Normal: []string{"javbus"},
	}
	runner := &fakeMovieRunner{
		results: []crawler.Result{
			{CrawlerID: "fanza", Err: crawler.ErrMovieNotFound},
			{CrawlerID: "javbus", Info: &model.MovieInfo{DVDID: "ABC-123", Title: "Normal Title", Cover: cover}},
		},
	}
	movie := &model.Movie{
		DVDID:      "ABC-123",
		CID:        "cid001",
		Files:      []string{source},
		DataSource: model.DataSourceCID,
	}
	ok, err := processMovie(context.Background(), movie, runner, nil, cfg, nil, nil, nil, root, nil)
	if err != nil {
		t.Fatalf("processMovie returned error: %v", err)
	}
	if !ok {
		t.Fatalf("processMovie ok=false")
	}
	if !reflect.DeepEqual(runner.ids, []model.CrawlerID{"fanza", "javbus"}) {
		t.Fatalf("runner ids = %#v", runner.ids)
	}
	if movie.DataSource != model.DataSourceNormal || movie.CID != "" {
		t.Fatalf("movie source/cid = %s / %q", movie.DataSource, movie.CID)
	}
	if movie.Info.DVDID != "ABC-123" || movie.Info.CID != "" || movie.Info.Title != "Normal Title" {
		t.Fatalf("movie info = %#v", movie.Info)
	}
}

func TestLoadGenreMapsLoadsKnownFilesAndSkipsMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "genre_javdb.csv"), []byte("id,translate\nfoo,Foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	maps, err := loadGenreMaps(root)
	if err != nil {
		t.Fatalf("loadGenreMaps returned error: %v", err)
	}
	if maps["javdb"] == nil {
		t.Fatalf("javdb genre map missing")
	}
	if got := maps["javdb"].Map([]string{"foo"}); len(got) != 1 || got[0] != "Foo" {
		t.Fatalf("javdb map result = %#v", got)
	}
	if _, ok := maps["javbus"]; ok {
		t.Fatalf("missing javbus csv should be skipped")
	}
}

func TestLoadActressAliases(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "actress_alias.json")
	if err := os.WriteFile(path, []byte(`{"Fixed Name":["Alias A","Alias B"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	aliases, err := loadActressAliases(path)
	if err != nil {
		t.Fatalf("loadActressAliases returned error: %v", err)
	}
	if got := aliases["Fixed Name"]; len(got) != 2 || got[0] != "Alias A" || got[1] != "Alias B" {
		t.Fatalf("aliases = %#v", aliases)
	}

	missing, err := loadActressAliases(filepath.Join(root, "missing.json"))
	if err != nil {
		t.Fatalf("missing loadActressAliases returned error: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing aliases = %#v, want empty", missing)
	}
}

type fakeMovieRunner struct {
	ids     []model.CrawlerID
	input   crawler.MovieRef
	results []crawler.Result
}

func (f *fakeMovieRunner) Run(ctx context.Context, ids []model.CrawlerID, input crawler.MovieRef) []crawler.Result {
	f.ids = append([]model.CrawlerID(nil), ids...)
	f.input = input
	return f.results
}

type fakeTranslator struct{}

func (fakeTranslator) Translate(ctx context.Context, text string, actress []string) (translate.Result, error) {
	if text == "Title" {
		return translate.Result{Text: "Translated Title"}, nil
	}
	return translate.Result{Text: text}, nil
}

func writeTestPNG(t *testing.T, path string, rect image.Rectangle, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(rect)
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}

func writeTestJPEG(t *testing.T, path string, rect image.Rectangle, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(rect)
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := jpeg.Encode(file, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
}

func decodedImageFormat(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	_, format, err := image.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	return format
}

func pngBounds(t *testing.T, path string) image.Rectangle {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	return img.Bounds()
}

func testProcessConfig(root string) *config.Config {
	return &config.Config{
		Network: config.Network{Retry: 1},
		Crawler: config.Crawler{
			RequiredKeys:    []string{"cover", "title"},
			RespectSiteAvid: true,
			UseJavDBCover:   "fallback",
		},
		Summarizer: config.Summarizer{
			MoveFiles: false,
			Path: config.PathSummarizer{
				OutputFolderPattern: filepath.Join(root, "out", "{num}"),
				BasenamePattern:     "{num}",
				MaxActressCount:     10,
			},
			NFO: config.NFOSummarizer{
				BasenamePattern:    "movie",
				TitlePattern:       "{num} {title}",
				CustomGenresFields: []string{"{genre}"},
				CustomTagsFields:   []string{"{censor}"},
			},
			Fanart: config.FanartSummarizer{BasenamePattern: "fanart"},
			Cover:  config.CoverSummarizer{BasenamePattern: "poster"},
			Default: config.DefaultValues{
				Title:     "#Unknown Title",
				Actress:   "#Unknown Actress",
				Series:    "#Unknown Series",
				Director:  "#Unknown Director",
				Producer:  "#Unknown Producer",
				Publisher: "#Unknown Publisher",
			},
			Title:                       config.TitleSummarizer{RemoveTrailingActorName: true},
			CensorOptionsRepresentation: []string{"无码", "有码", "打码情况未知"},
		},
	}
}
