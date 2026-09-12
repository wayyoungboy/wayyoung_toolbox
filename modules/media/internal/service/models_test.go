package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTitleMatches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name               string
		title              string
		all, any, excluded []string
		want               bool
	}{
		{"matches all rules", "HD 中出 SAMPLE", []string{"HD"}, []string{"中出", "内射"}, nil, true},
		{"missing required", "中出 SAMPLE", []string{"HD"}, []string{"中出"}, nil, false},
		{"missing optional", "HD SAMPLE", []string{"HD"}, []string{"中出"}, nil, false},
		{"excluded", "HD 中出 VR", []string{"HD"}, []string{"中出"}, []string{"VR"}, false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, _ := titleMatches(test.title, test.all, test.any, test.excluded)
			if got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestCalculateNextRunDaily(t *testing.T) {
	original := time.Local
	time.Local = time.FixedZone("CST", 8*60*60)
	defer func() { time.Local = original }()
	now := time.Date(2026, 8, 9, 0, 30, 0, 0, time.UTC)
	got, err := calculateNextRun("daily", 60, "04:00", now)
	if err != nil {
		t.Fatal(err)
	}
	if got != "2026-08-09T20:00:00Z" {
		t.Fatalf("got %s", got)
	}
}

func TestValidateJavspConfigContainment(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	base := defaultJavspConfig()
	base.InputDirectory = root
	base.OutputFolderPattern = "organized/{title}"
	if _, err := validateJavspConfig(base); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	for _, pattern := range []string{"../outside", "/tmp/out", "escape/{title}"} {
		candidate := base
		candidate.OutputFolderPattern = pattern
		if _, err := validateJavspConfig(candidate); err == nil {
			t.Errorf("pattern %q should fail", pattern)
		}
	}
}

func TestValidateJavspAdvancedConfig(t *testing.T) {
	base := defaultJavspConfig()
	base.InputDirectory = t.TempDir()
	base.OutputFolderPattern = "organized/{title}"
	if _, err := validateJavspConfig(base); err != nil {
		t.Fatalf("default advanced config should be valid: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*JavspConfig)
	}{
		{"invalid ignored-id regex", func(config *JavspConfig) { config.IgnoredIDPatterns = []string{"["} }},
		{"invalid proxy-free URL", func(config *JavspConfig) { config.ProxyFree = map[string]string{"javdb": "file:///etc/passwd"} }},
		{"invalid network timeout", func(config *JavspConfig) { config.NetworkTimeout = "tomorrow" }},
		{"excessive crawler concurrency", func(config *JavspConfig) { config.MovieConcurrency = 65 }},
		{"unsupported crop engine", func(config *JavspConfig) { config.CoverCropEngine = "shell" }},
		{"missing translator secret", func(config *JavspConfig) { config.TranslatorEngine = "openai"; config.TranslatorAPIKey = "" }},
		{"missing DeepL secret", func(config *JavspConfig) { config.TranslatorEngine = "deepl"; config.TranslatorAPIKey = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			test.mutate(&candidate)
			if _, err := validateJavspConfig(candidate); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestValidateJavspConfigAcceptsDeepL(t *testing.T) {
	config := defaultJavspConfig()
	config.InputDirectory = t.TempDir()
	config.OutputFolderPattern = "#整理完成/{actress}/[{num}] {title}"
	config.TranslatorEngine = "deepl"
	config.TranslatorAPIKey = "test-key:fx"
	validated, err := validateJavspConfig(config)
	if err != nil {
		t.Fatalf("DeepL config rejected: %v", err)
	}
	if validated.TranslatorEngine != "deepl" || validated.TranslatorAPIKey != "test-key:fx" {
		t.Fatalf("validated DeepL config = %#v", validated)
	}
}

func TestJavspDefaultsPublishSameNameNFO(t *testing.T) {
	config := defaultJavspConfig()
	if config.NFOBasenamePattern != "{num}" {
		t.Fatalf("default NFO basename = %q, want {num}", config.NFOBasenamePattern)
	}
	if config.NFOBasenamePattern != config.PathBasenamePattern {
		t.Fatalf("NFO basename %q must match video basename %q", config.NFOBasenamePattern, config.PathBasenamePattern)
	}
}
