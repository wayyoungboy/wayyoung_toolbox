package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yuukiy/javsp-go/internal/config"
	"gopkg.in/yaml.v3"
)

func TestDiscoveryOnlyPreservesScannerRulesAndMultipartFilms(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"TEST-001.1.mp4": "first", "TEST-001.2.mp4": "second", "[未识别].mp4": "unknown",
		"SMALL-003.mp4": "x", "ignored/TEST-002.mp4": "ignored", "with-nfo/TEST-004.mp4": "existing", "with-nfo/movie.nfo": "<movie/>",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(filepath.Join("..", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Scanner.MinimumSize = "3B"
	cfg.Scanner.FilenameExtensions = []string{".mp4"}
	cfg.Scanner.IgnoredFolderNamePattern = []string{"^ignored$"}
	cfg.Scanner.SkipNFODir = true
	// Discovery does not need a functioning network transport.
	unreachableProxy := "http://127.0.0.1:1"
	cfg.Network.ProxyServer = &unreachableProxy
	cfg.Crawler.Selection.Normal = []string{"javbus"}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Discover(context.Background(), Options{ConfigData: data, ResourceRoot: "..", InputDirectory: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Movies) != 1 || result.Movies[0].MediaID != "TEST-001" || len(result.Movies[0].Files) != 2 {
		t.Fatalf("discovery lost grouped scanner result: %+v", result)
	}
	unknownPath, err := filepath.EvalSymlinks(filepath.Join(root, "[未识别].mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Unrecognized) != 1 || result.Unrecognized[0].Path != unknownPath {
		t.Fatalf("unknown input disappeared: %+v", result)
	}
	for _, path := range result.Movies[0].Files {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("discovery changed a source file:", err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "TEST-001.nfo")); !os.IsNotExist(err) {
		t.Fatal("discovery generated metadata")
	}
}

func TestDiscoveryEmptyInputAndCancellation(t *testing.T) {
	options := Options{ConfigPath: filepath.Join("..", "config.yml"), InputDirectory: t.TempDir()}
	result, err := Discover(context.Background(), options)
	if err != nil || len(result.Movies) != 0 || len(result.Unrecognized) != 0 {
		t.Fatalf("empty discovery should be a successful no-op: %+v %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Discover(ctx, options); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestDiscoverySelectedFilesNeverFallsBackToWholeDirectory(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"TEST-001.mp4", "OTHER-002.mp4", "[未识别].mp4"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(filepath.Join("..", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Scanner.MinimumSize = "0B"
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	options := Options{ConfigData: data, ResourceRoot: "..", InputDirectory: root, InputFiles: []string{filepath.Join(root, "TEST-001.mp4"), filepath.Join(root, "[未识别].mp4")}}
	result, err := Discover(context.Background(), options)
	if err != nil || len(result.Movies) != 1 || result.Movies[0].MediaID != "TEST-001" || len(result.Unrecognized) != 1 {
		t.Fatalf("selected discovery lost or expanded input: %+v %v", result, err)
	}
	options.InputFiles = []string{}
	if _, err := Discover(context.Background(), options); err == nil {
		t.Fatal("empty selection expanded into directory discovery")
	}
}

func TestDiscoveryExcludesExactOutputDirectoryBeforeGrouping(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"library/archive/TEST-001.1.mp4", "pending/archive/TEST-002.mp4", "TEST-001.2.mp4"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(filepath.Join("..", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Scanner.MinimumSize = "0B"
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	options := Options{ConfigData: data, ResourceRoot: "..", InputDirectory: root, ExcludedDirectories: []string{filepath.Join(root, "library", "archive")}}
	result, err := Discover(context.Background(), options)
	if err != nil || len(result.Movies) != 2 {
		t.Fatalf("exact directory exclusion failed: %+v %v", result, err)
	}
	for _, movie := range result.Movies {
		if len(movie.Files) != 1 {
			t.Fatalf("output file was grouped with new input: %+v", movie)
		}
	}
	options.InputFiles = []string{filepath.Join(root, "library", "archive", "TEST-001.1.mp4")}
	if _, err := Discover(context.Background(), options); err == nil {
		t.Fatal("explicit files bypassed an excluded output root")
	}
	options.InputFiles = nil
	options.ExcludedDirectories = []string{"archive"}
	if _, err := Discover(context.Background(), options); err == nil {
		t.Fatal("relative exclusion silently accepted")
	}
}

func TestDiscoveryRootAliasWorksForRecursiveAndExplicitSelections(t *testing.T) {
	base := t.TempDir()
	realRoot, alias := filepath.Join(base, "real"), filepath.Join(base, "alias")
	if err := os.MkdirAll(filepath.Join(realRoot, "organized"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TEST-001.mp4", "organized/TEST-002.mp4"} {
		if err := os.WriteFile(filepath.Join(realRoot, name), []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join("..", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Scanner.MinimumSize = "0B"
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, exclusionRoot := range []string{alias, canonicalRoot} {
		for _, explicit := range []bool{false, true} {
			options := Options{ConfigData: data, ResourceRoot: "..", InputDirectory: alias, ExcludedDirectories: []string{filepath.Join(exclusionRoot, "organized")}}
			if explicit {
				options.InputFiles = []string{filepath.Join(alias, "TEST-001.mp4")}
			}
			result, err := Discover(context.Background(), options)
			if err != nil || len(result.Movies) != 1 || result.Movies[0].MediaID != "TEST-001" {
				t.Fatalf("alias discovery explicit=%t exclusions=%v: %+v %v", explicit, options.ExcludedDirectories, result, err)
			}
			options.InputFiles = []string{filepath.Join(alias, "organized", "TEST-002.mp4")}
			if _, err := Discover(context.Background(), options); err == nil {
				t.Fatal("root alias bypassed output exclusion")
			}
		}
	}
}
