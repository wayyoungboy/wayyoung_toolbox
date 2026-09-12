package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const localConfigPath = "../../config.yml"

func TestLoadRepositoryConfig(t *testing.T) {
	cfg, err := Load(localConfigPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got := cfg.Network.Retry; got != 3 {
		t.Fatalf("Network.Retry = %d, want 3", got)
	}
	if len(cfg.Crawler.Selection.Normal) == 0 {
		t.Fatalf("Crawler.Selection.Normal is empty")
	}
	if cfg.Crawler.Selection.Normal[0] != "faleno" {
		t.Fatalf("Crawler.Selection.Normal[0] = %q, want faleno", cfg.Crawler.Selection.Normal[0])
	}
	if cfg.Crawler.MovieConcurrency != 3 {
		t.Fatalf("Crawler.MovieConcurrency = %d, want 3", cfg.Crawler.MovieConcurrency)
	}
	if cfg.Scanner.MinimumSize != "1GiB" {
		t.Fatalf("Scanner.MinimumSize = %q, want 1GiB", cfg.Scanner.MinimumSize)
	}
	if cfg.Scanner.Manual {
		t.Fatal("Scanner.Manual = true, want shipped default false")
	}
	if cfg.Scanner.SkipNFODir {
		t.Fatal("Scanner.SkipNFODir = true, want shipped default false")
	}
	if cfg.Network.ProxyServer != nil {
		t.Fatalf("Network.ProxyServer = %#v, want no shipped proxy", cfg.Network.ProxyServer)
	}
	translator, err := engineMap("translator.engine", cfg.Translator.Engine)
	if err != nil {
		t.Fatalf("translator engine: %v", err)
	}
	if translator["name"] != "google" {
		t.Fatalf("Translator.Engine.name = %#v, want google", translator["name"])
	}
	if cfg.Summarizer.Path.OutputFolderPattern == "" {
		t.Fatalf("Summarizer.Path.OutputFolderPattern is empty")
	}
}

func TestLoadDataWithOverridesDoesNotRequireConfigFile(t *testing.T) {
	data, err := os.ReadFile(localConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDataWithOverrides(data, nil, []string{"network.retry=9"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Network.Retry != 9 {
		t.Fatalf("network retry = %d, want 9", cfg.Network.Retry)
	}
}

func TestLoadLegacyConfigDefaultsMovieConcurrencyToOne(t *testing.T) {
	data, err := os.ReadFile(localConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.Replace(string(data), "  movie_concurrency: 3\n", "", 1)
	if legacy == string(data) {
		t.Fatal("shipped config no longer contains movie_concurrency fixture")
	}
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load legacy config returned error: %v", err)
	}
	if cfg.Crawler.MovieConcurrency != 1 {
		t.Fatalf("MovieConcurrency = %d, want compatibility default 1", cfg.Crawler.MovieConcurrency)
	}
}

func TestShippedConfigRegexesRetainSingleQuotedYAMLSemantics(t *testing.T) {
	cfg, err := Load(localConfigPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	cases := []struct {
		name    string
		pattern string
		value   string
	}{
		{name: "ID noise", pattern: cfg.Scanner.IgnoredIDPattern[2], value: "example2048.com"},
		{name: "HD marker", pattern: cfg.Scanner.IgnoredIDPattern[4], value: "-fhd-"},
		{name: "hidden folder", pattern: cfg.Scanner.IgnoredFolderNamePattern[0], value: ".hidden"},
		{name: "numeric crop ID", pattern: cfg.Summarizer.Cover.Crop.OnIDPattern[0], value: "123456-123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matched, err := regexp.MatchString(tc.pattern, tc.value)
			if err != nil {
				t.Fatalf("regexp %q is invalid: %v", tc.pattern, err)
			}
			if !matched {
				t.Fatalf("regexp %q did not match %q", tc.pattern, tc.value)
			}
		})
	}
}

func TestLocalRuntimeAssetsExistRelativeToConfig(t *testing.T) {
	configPath, err := filepath.Abs(localConfigPath)
	if err != nil {
		t.Fatalf("resolve local config path: %v", err)
	}
	resourceRoot := filepath.Dir(configPath)
	assets := []string{
		"data/actress_alias.json",
		"data/facefinder",
		"data/genre_avsox.csv",
		"data/genre_javbus.csv",
		"data/genre_javdb.csv",
		"data/genre_javlib.csv",
		"image/JavSP.svg",
		"image/sub_mark.png",
		"image/unc_mark.png",
	}
	for _, asset := range assets {
		info, err := os.Stat(filepath.Join(resourceRoot, filepath.FromSlash(asset)))
		if err != nil {
			t.Errorf("local runtime asset %s: %v", asset, err)
			continue
		}
		if info.IsDir() || info.Size() == 0 {
			t.Errorf("local runtime asset %s is not a non-empty file", asset)
		}
	}

	modelPath := filepath.Join(resourceRoot, "data", "facefinder")
	model, err := os.ReadFile(modelPath)
	if err != nil {
		t.Fatalf("read Pigo model %s: %v", modelPath, err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(model)); got != "d8014993e7298c7b1865d1f8b855d6dbf4ec5c808bf879e2091ab6837abf90cd" {
		t.Errorf("facefinder SHA-256 = %s, want d8014993e7298c7b1865d1f8b855d6dbf4ec5c808bf879e2091ab6837abf90cd", got)
	}
}

func TestLoadWithOverridesAppliesEnvironmentAndCLI(t *testing.T) {
	cfg, err := LoadWithOverrides(
		localConfigPath,
		[]string{
			"JAVSP_NETWORK_TIMEOUT=PT2M",
			"JAVSP_NETWORK_PROXY_FREE_JAVBUS=https://env.example",
			"JAVSP_CRAWLER_SELECTION_NORMAL=[javbus]",
			"JAVSP_TRANSLATOR_ENGINE_NAME=google",
			"JAVSP_SUMMARIZER__DEFAULT__TITLE='#自定义标题'",
		},
		[]string{
			"network.retry=5",
			"translator.fields.plot=false",
		},
	)
	if err != nil {
		t.Fatalf("LoadWithOverrides returned error: %v", err)
	}
	if cfg.Network.Timeout != "PT2M" || cfg.Network.Retry != 5 {
		t.Fatalf("network = %#v", cfg.Network)
	}
	if cfg.Network.ProxyFree["javbus"] != "https://env.example" {
		t.Fatalf("proxy_free.javbus = %q", cfg.Network.ProxyFree["javbus"])
	}
	if len(cfg.Crawler.Selection.Normal) != 1 || cfg.Crawler.Selection.Normal[0] != "javbus" {
		t.Fatalf("crawler.selection.normal = %#v", cfg.Crawler.Selection.Normal)
	}
	engine, ok := cfg.Translator.Engine.(map[string]any)
	if !ok || engine["name"] != "google" {
		t.Fatalf("translator engine = %#v", cfg.Translator.Engine)
	}
	if cfg.Translator.Fields.Plot {
		t.Fatalf("translator.fields.plot = true, want false")
	}
	if cfg.Summarizer.Default.Title != "#自定义标题" {
		t.Fatalf("summarizer.default.title = %q, want %q", cfg.Summarizer.Default.Title, "#自定义标题")
	}
}

func TestParseISODurationSecondsSupportsMinutesHoursDaysAndWeeks(t *testing.T) {
	cases := map[string]time.Duration{
		"PT1M":       time.Minute,
		"PT1H2M3.5S": time.Hour + 2*time.Minute + 3500*time.Millisecond,
		"P1DT2H":     26 * time.Hour,
		"P2W":        14 * 24 * time.Hour,
		"10s":        10 * time.Second,
	}
	for input, want := range cases {
		got, err := ParseISODurationSeconds(input)
		if err != nil {
			t.Fatalf("ParseISODurationSeconds(%q) returned error: %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseISODurationSeconds(%q) = %s, want %s", input, got, want)
		}
	}
}

func TestValidateRejectsInvalidConfigValues(t *testing.T) {
	cfg, err := LoadWithOverrides(
		localConfigPath,
		nil,
		[]string{"crawler.selection.normal=[not_a_site]"},
	)
	if err == nil {
		t.Fatalf("expected invalid crawler selection to fail, cfg=%#v", cfg)
	}

	cfg, err = LoadWithOverrides(
		localConfigPath,
		nil,
		[]string{"translator.engine.name=bad_engine"},
	)
	if err == nil {
		t.Fatalf("expected invalid translator engine to fail, cfg=%#v", cfg)
	}

	cfg, err = LoadWithOverrides(
		localConfigPath,
		nil,
		[]string{"crawler.movie_concurrency=0"},
	)
	if err == nil {
		t.Fatalf("expected explicit zero movie concurrency to fail, cfg=%#v", cfg)
	}
}

func TestValidateCropEngineAcceptsPigoAndLegacySlimeface(t *testing.T) {
	for _, name := range []string{"pigo", "slimeface"} {
		t.Run(name, func(t *testing.T) {
			cfg, err := LoadWithOverrides(
				localConfigPath,
				nil,
				[]string{"summarizer.cover.crop.engine.name=" + name},
			)
			if err != nil {
				t.Fatalf("crop engine %q rejected: %v", name, err)
			}
			if cfg == nil {
				t.Fatal("LoadWithOverrides returned nil config")
			}
		})
	}
}

func TestValidateCropEngineRejectsUnsupportedName(t *testing.T) {
	cfg, err := LoadWithOverrides(
		localConfigPath,
		nil,
		[]string{"summarizer.cover.crop.engine.name=opencv"},
	)
	if err == nil {
		t.Fatalf("unsupported crop engine accepted, cfg=%#v", cfg)
	}
}

func TestValidateTranslatorEngineRequiresOriginalModelFields(t *testing.T) {
	tests := []struct {
		name     string
		override string
		want     string
	}{
		{name: "scalar engine", override: "translator.engine=google", want: "must be a map"},
		{name: "bing api key", override: "translator.engine={name: bing}", want: "api_key"},
		{name: "baidu app id", override: "translator.engine={name: baidu, api_key: key}", want: "app_id"},
		{name: "claude api key", override: "translator.engine={name: claude}", want: "api_key"},
		{name: "openai url", override: "translator.engine={name: openai, api_key: key, model: model}", want: "url"},
		{name: "openai absolute url", override: "translator.engine={name: openai, url: relative, api_key: key, model: model}", want: "absolute URL"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := LoadWithOverrides(localConfigPath, nil, []string{test.override})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("cfg=%#v error=%v, want error containing %q", cfg, err, test.want)
			}
		})
	}
}

func TestValidateTranslatorEngineAcceptsOriginalModelFields(t *testing.T) {
	engines := []string{
		"{name: google}",
		"{name: bing, api_key: key}",
		"{name: baidu, app_id: id, api_key: key}",
		"{name: claude, api_key: key}",
		"{name: openai, url: 'https://example.com/v1/chat/completions', api_key: key, model: model}",
	}
	for _, engine := range engines {
		if _, err := LoadWithOverrides(localConfigPath, nil, []string{"translator.engine=" + engine}); err != nil {
			t.Fatalf("engine %s rejected: %v", engine, err)
		}
	}
}
