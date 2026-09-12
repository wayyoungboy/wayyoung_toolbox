package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestJavspOutputIsBounded(t *testing.T) {
	buffer := &tailBuffer{}
	if _, err := buffer.Write(bytes.Repeat([]byte("x"), maxJavspOutput+1000)); err != nil {
		t.Fatal(err)
	}
	output := buffer.String()
	if len(output) > maxJavspOutput+100 {
		t.Fatalf("output too large: %d", len(output))
	}
	if !strings.HasPrefix(output, "[日志过长") {
		t.Fatal("missing truncation marker")
	}
}

func TestJavspPrepareConfigMapsAdvancedOptions(t *testing.T) {
	bundle := t.TempDir()
	data := t.TempDir()
	runtimeRoot := filepath.Join(bundle, "runtime")
	if err := os.MkdirAll(filepath.Join(runtimeRoot, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runtimeRoot, "image"), 0o700); err != nil {
		t.Fatal(err)
	}
	template, err := os.ReadFile(filepath.Join("..", "..", "resources", "javsp", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeRoot, "config.yml"), template, 0o600); err != nil {
		t.Fatal(err)
	}

	config := defaultJavspConfig()
	config.InputDirectory = t.TempDir()
	config.SkipNFODir = true
	config.NetworkRetry = 5
	config.NetworkTimeout = "20s"
	config.NormalCrawlers = []string{"javdb", "javbus"}
	config.MovieConcurrency = 7
	config.PathBasenamePattern = "[{num}] {title}"
	config.PathHardLink = true
	config.NFOTitlePattern = "[{num}] {title}"
	config.CoverAddLabel = true
	config.CoverCropEngine = "none"
	config.TranslatePlot = false
	config.TranslatorEngine = "openai"
	config.TranslatorURL = "https://translator.example/v1"
	config.TranslatorModel = "example-model"
	config.TranslatorAPIKey = "secret-key"

	runner := NewJavspRunner(bundle, data)
	raw, err := runner.PrepareConfigData(config)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	scanner := document["scanner"].(map[string]any)
	network := document["network"].(map[string]any)
	crawler := document["crawler"].(map[string]any)
	summarizer := document["summarizer"].(map[string]any)
	translator := document["translator"].(map[string]any)
	if scanner["skip_nfo_dir"] != true || network["retry"] != 5 || network["timeout"] != "20s" {
		t.Fatalf("scanner/network options missing: %#v %#v", scanner, network)
	}
	if crawler["movie_concurrency"] != 7 {
		t.Fatalf("crawler options missing: %#v", crawler)
	}
	pathConfig := summarizer["path"].(map[string]any)
	if pathConfig["basename_pattern"] != "[{num}] {title}" || pathConfig["hard_link"] != true {
		t.Fatalf("path options missing: %#v", pathConfig)
	}
	engine := translator["engine"].(map[string]any)
	if engine["name"] != "openai" || engine["api_key"] != "secret-key" || engine["model"] != "example-model" {
		t.Fatalf("translator options missing: %#v", engine)
	}
	if err := os.WriteFile(runner.ConfigPath(), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := runner.ReadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.NetworkRetry != config.NetworkRetry || loaded.NetworkTimeout != config.NetworkTimeout ||
		loaded.MovieConcurrency != config.MovieConcurrency || loaded.PathBasenamePattern != config.PathBasenamePattern ||
		loaded.PathHardLink != config.PathHardLink || loaded.TranslatorEngine != config.TranslatorEngine ||
		loaded.TranslatorAPIKey != config.TranslatorAPIKey {
		t.Fatalf("advanced configuration did not survive a YAML round trip: %#v", loaded)
	}
}

func TestBundledJavspTemplatesUseSameNameNFO(t *testing.T) {
	templates := []string{
		filepath.Join("..", "..", "resources", "javsp", "config.yml"),
	}
	for _, path := range templates {
		t.Run(path, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var document struct {
				Summarizer struct {
					Path struct {
						BasenamePattern string `yaml:"basename_pattern"`
					} `yaml:"path"`
					NFO struct {
						BasenamePattern string `yaml:"basename_pattern"`
					} `yaml:"nfo"`
				} `yaml:"summarizer"`
			}
			if err := yaml.Unmarshal(raw, &document); err != nil {
				t.Fatal(err)
			}
			if document.Summarizer.NFO.BasenamePattern != "{num}" {
				t.Fatalf("NFO basename = %q, want {num}", document.Summarizer.NFO.BasenamePattern)
			}
			if document.Summarizer.NFO.BasenamePattern != document.Summarizer.Path.BasenamePattern {
				t.Fatalf("NFO basename %q differs from media basename %q", document.Summarizer.NFO.BasenamePattern, document.Summarizer.Path.BasenamePattern)
			}
		})
	}
}

func TestCleanupOnlyNewEmptyJavspOutputDirectories(t *testing.T) {
	input := t.TempDir()
	outputRoot := filepath.Join(input, "#整理完成")
	existingEmpty := filepath.Join(outputRoot, "existing-empty")
	if err := os.MkdirAll(existingEmpty, 0o700); err != nil {
		t.Fatal(err)
	}
	config := defaultJavspConfig()
	config.InputDirectory = input
	config.OutputFolderPattern = "#整理完成/{actress}/[{num}] {title}"
	root, before, err := snapshotJavspOutputDirectories(config)
	if err != nil {
		t.Fatal(err)
	}
	newEmpty := filepath.Join(outputRoot, "new-parent", "new-child")
	if err := os.MkdirAll(newEmpty, 0o700); err != nil {
		t.Fatal(err)
	}
	newPopulated := filepath.Join(outputRoot, "populated")
	if err := os.MkdirAll(newPopulated, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newPopulated, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := cleanupNewEmptyJavspDirectories(root, before)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed=%d, want 2", removed)
	}
	if _, err := os.Stat(newEmpty); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new empty directory still exists or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(existingEmpty); err != nil {
		t.Fatalf("pre-existing empty directory was removed: %v", err)
	}
	if _, err := os.Stat(newPopulated); err != nil {
		t.Fatalf("populated directory was removed: %v", err)
	}
	if _, err := os.Stat(outputRoot); err != nil {
		t.Fatalf("output root was removed: %v", err)
	}
}

func TestCanceledJavspFinalizationDoesNotCreateNFO(t *testing.T) {
	input := t.TempDir()
	config := defaultJavspConfig()
	config.InputDirectory = input
	config.OutputFolderPattern = "#整理完成/{num}"
	output := filepath.Join(input, "#整理完成", "TEST-001")
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "movie.nfo"), []byte("<movie/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(output, "TEST-001.mp4")
	if err := os.WriteFile(video, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := createFnosNFOsForNewVideosContext(ctx, config, filepath.Join(input, "#整理完成"), map[string]struct{}{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("finalization error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(filepath.Join(output, "TEST-001.nfo")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("same-name NFO was created after cancellation: %v", err)
	}
}

func TestCreateFnosNFOsOnlyForNewJavspVideos(t *testing.T) {
	input := t.TempDir()
	outputRoot := filepath.Join(input, "#整理完成")
	existingDir := filepath.Join(outputRoot, "actor", "[WANZ-490] existing")
	if err := os.MkdirAll(existingDir, 0o700); err != nil {
		t.Fatal(err)
	}
	existingVideo := filepath.Join(existingDir, "WANZ-490-C.mp4")
	if err := os.WriteFile(existingVideo, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existingDir, "movie.nfo"), []byte("<movie><title>existing</title></movie>"), 0o600); err != nil {
		t.Fatal(err)
	}

	config := defaultJavspConfig()
	config.InputDirectory = input
	config.OutputFolderPattern = "#整理完成/{actress}/[{num}] {title}"
	root, videosBefore, err := snapshotJavspOutputVideos(config)
	if err != nil {
		t.Fatal(err)
	}

	newDir := filepath.Join(outputRoot, "actor", "[ROYD-203] new")
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		t.Fatal(err)
	}
	newVideo := filepath.Join(newDir, "ROYD-203-C.mp4")
	if err := os.WriteFile(newVideo, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantNFO := []byte("<movie><title>new title</title></movie>")
	if err := os.WriteFile(filepath.Join(newDir, "movie.nfo"), wantNFO, 0o640); err != nil {
		t.Fatal(err)
	}

	created, err := createFnosNFOsForNewVideos(config, root, videosBefore)
	if err != nil {
		t.Fatal(err)
	}
	if created != 1 {
		t.Fatalf("created=%d, want 1", created)
	}
	gotNFO, err := os.ReadFile(filepath.Join(newDir, "ROYD-203-C.nfo"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotNFO) != string(wantNFO) {
		t.Fatalf("same-name NFO content=%q, want %q", gotNFO, wantNFO)
	}
	if _, err := os.Stat(filepath.Join(existingDir, "WANZ-490-C.nfo")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-existing video was unexpectedly changed: %v", err)
	}
}

func TestCreateFnosNFOsNeverOverwritesExistingCompanion(t *testing.T) {
	input := t.TempDir()
	outputRoot := filepath.Join(input, "#整理完成")
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	config := defaultJavspConfig()
	config.InputDirectory = input
	config.OutputFolderPattern = "#整理完成/{actress}/[{num}] {title}"
	root, videosBefore, err := snapshotJavspOutputVideos(config)
	if err != nil {
		t.Fatal(err)
	}

	directory := filepath.Join(outputRoot, "actor", "[MIKR-069] existing-nfo")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "MIKR-069-C.mp4"), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "movie.nfo"), []byte("generic"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantExisting := []byte("keep me")
	companion := filepath.Join(directory, "MIKR-069-C.nfo")
	if err := os.WriteFile(companion, wantExisting, 0o600); err != nil {
		t.Fatal(err)
	}

	created, err := createFnosNFOsForNewVideos(config, root, videosBefore)
	if err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatalf("created=%d, want 0", created)
	}
	got, err := os.ReadFile(companion)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(wantExisting) {
		t.Fatalf("existing NFO was overwritten: %q", got)
	}
}

func TestFnosNFOSnapshotHandlesMissingAndInvalidOutputRoots(t *testing.T) {
	input := t.TempDir()
	config := defaultJavspConfig()
	config.InputDirectory = input
	config.OutputFolderPattern = "#整理完成/{actress}/[{num}] {title}"

	root, videos, err := snapshotJavspOutputVideos(config)
	if err != nil {
		t.Fatal(err)
	}
	if root != filepath.Join(input, "#整理完成") || len(videos) != 0 {
		t.Fatalf("missing output root snapshot=%q, %#v", root, videos)
	}
	if err := os.WriteFile(root, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := snapshotJavspOutputVideos(config); err == nil {
		t.Fatal("non-directory output root was accepted")
	}
}

func TestCreateFnosNFOsSkipsVideosWithoutGenericNFO(t *testing.T) {
	input := t.TempDir()
	outputRoot := filepath.Join(input, "#整理完成")
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	config := defaultJavspConfig()
	config.InputDirectory = input
	config.OutputFolderPattern = "#整理完成/{actress}/[{num}] {title}"
	root, videosBefore, err := snapshotJavspOutputVideos(config)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(outputRoot, "actor", "[VENX-345] no-nfo")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "VENX-345-C.mp4"), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "ignored.txt"), []byte("not a video"), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err := createFnosNFOsForNewVideos(config, root, videosBefore)
	if err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatalf("created=%d, want 0", created)
	}
	if _, err := os.Stat(filepath.Join(directory, "VENX-345-C.nfo")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("NFO unexpectedly created without movie.nfo: %v", err)
	}
}

func TestCreateFnosNFOsRejectsMismatchedOutputRoot(t *testing.T) {
	input := t.TempDir()
	config := defaultJavspConfig()
	config.InputDirectory = input
	config.OutputFolderPattern = "#整理完成/{actress}/[{num}] {title}"
	if _, err := createFnosNFOsForNewVideos(config, filepath.Join(input, "other"), map[string]struct{}{}); err == nil {
		t.Fatal("mismatched output root was accepted")
	}
}

func TestCopyFileExclusiveDoesNotOverwrite(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.nfo")
	destination := filepath.Join(directory, "destination.nfo")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	copied, err := copyFileExclusive(source, destination, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if copied {
		t.Fatal("existing destination was reported as copied")
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "existing" {
		t.Fatalf("existing destination changed: %q", got)
	}
	if _, err := copyFileExclusive(filepath.Join(directory, "missing.nfo"), filepath.Join(directory, "new.nfo"), 0o600); err == nil {
		t.Fatal("missing source did not return an error")
	}
}
