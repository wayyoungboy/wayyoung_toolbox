package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuukiy/javsp-go/internal/scanner"
)

func TestSelectedInputScansOnlyExplicitFiles(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "pending", "TEST-001.mp4")
	other := filepath.Join(root, "OTHER-002.mp4")
	if err := os.MkdirAll(filepath.Dir(selected), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{selected, other} {
		if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	movies, err := scanEngineInput(context.Background(), root, []string{selected, selected}, scanner.Options{FilenameExtensions: []string{".mp4"}}, io.Discard)
	if err != nil || len(movies) != 1 || movies[0].DVDID != "TEST-001" || len(movies[0].Files) != 1 {
		t.Fatalf("selected scan leaked into library: %+v %v", movies, err)
	}
}

func TestSelectedInputRejectsEmptyUnsafeAndCanceledRequests(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	for _, dir := range []string{root, outside} {
		if err := os.WriteFile(filepath.Join(dir, "TEST-001.mp4"), []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for name, paths := range map[string][]string{
		"empty": {}, "directory": {root}, "outside": {filepath.Join(outside, "TEST-001.mp4")},
		"symlink": {filepath.Join(link, "TEST-001.mp4")}, "relative": {"TEST-001.mp4"},
		"missing": {filepath.Join(root, "MISSING-002.mp4")},
	} {
		t.Run(name, func(t *testing.T) {
			if movies, err := scanEngineInput(context.Background(), root, paths, scanner.Options{FilenameExtensions: []string{".mp4"}}, io.Discard); err == nil || len(movies) > 0 {
				t.Fatalf("unsafe input accepted or full-scan fallback: %+v %v", movies, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := scanEngineInput(ctx, root, []string{filepath.Join(root, "TEST-001.mp4")}, scanner.Options{FilenameExtensions: []string{".mp4"}}, io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}

func TestRunEmptySelectionNeverFallsBackToConfiguredLibrary(t *testing.T) {
	_, err := Run(context.Background(), Options{ConfigPath: filepath.Join("..", "config.yml"), InputDirectory: t.TempDir(), InputFiles: []string{}})
	if err == nil || !strings.Contains(err.Error(), "explicit input files are empty") {
		t.Fatalf("Run did not honor explicit empty selection: %v", err)
	}
}
