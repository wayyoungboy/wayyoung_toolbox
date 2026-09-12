package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type selectedJavspFixture func(context.Context, embeddedJavspOptions) (embeddedJavspResult, error)

func (f selectedJavspFixture) Run(ctx context.Context, options embeddedJavspOptions) (embeddedJavspResult, error) {
	return f(ctx, options)
}
func (selectedJavspFixture) Version() string { return "selected-fixture" }

func TestSelectedEmbeddedRunNeverFinalizesUnrelatedLibraryFiles(t *testing.T) {
	bundle, data := javspTestRuntime(t)
	root := t.TempDir()
	input := filepath.Join(root, "pending", "TEST-001.mp4")
	unrelated := filepath.Join(root, "output", "OTHER-002")
	empty := filepath.Join(root, "output", "unrelated-empty")
	config := defaultJavspConfig()
	config.InputDirectory = root
	config.OutputFolderPattern = "output/{num}"
	config.TimeoutSeconds = 5
	runner := newJavspRunnerWithEngine(bundle, data, selectedJavspFixture(func(ctx context.Context, options embeddedJavspOptions) (embeddedJavspResult, error) {
		if options.InputDirectory != root || len(options.InputFiles) != 1 || options.InputFiles[0] != input {
			t.Errorf("wrong explicit input: %+v", options.InputFiles)
		}
		if options.Hooks.BeforeOrganize == nil || options.Hooks.MoveFiles == nil {
			t.Error("queue hooks not forwarded")
		}
		writeIntakeFixture(t, unrelated, map[string]string{"OTHER-002.mp4": "video", "movie.nfo": "<movie><title>Example</title></movie>"})
		if err := os.MkdirAll(empty, 0700); err != nil {
			t.Fatal(err)
		}
		return embeddedJavspResult{Total: 1, Processed: 1, Movies: []JavspMovieOutcome{{MediaID: "TEST-001", SourcePaths: []string{input}, OutputPaths: []string{filepath.Join(root, "output", "TEST-001.mp4")}, Status: "success"}}}, nil
	}))
	result, err := runner.RunSelected(context.Background(), config, []string{input}, JavspExecutionHooks{BeforeOrganize: func(context.Context, JavspMoviePlan) error { return nil }, MoveFiles: func(context.Context, JavspMoviePlan) (JavspMoveResult, error) { return JavspMoveResult{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Movies) != 1 || result.Movies[0].MediaID != "TEST-001" || result.Processed != 1 {
		t.Fatalf("engine result lost: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(unrelated, "OTHER-002.nfo")); !os.IsNotExist(err) {
		t.Fatalf("selected run touched unrelated NFO: %v", err)
	}
	if _, err := os.Stat(empty); err != nil {
		t.Fatalf("selected run removed unrelated empty directory: %v", err)
	}
}

func TestSelectedEmbeddedRunRejectsEmptySelectionAndPreservesContextReason(t *testing.T) {
	bundle, data := javspTestRuntime(t)
	calls := 0
	runner := newJavspRunnerWithEngine(bundle, data, selectedJavspFixture(func(ctx context.Context, _ embeddedJavspOptions) (embeddedJavspResult, error) {
		calls++
		return embeddedJavspResult{}, context.DeadlineExceeded
	}))
	config := defaultJavspConfig()
	config.InputDirectory = t.TempDir()
	config.TimeoutSeconds = 5
	for _, files := range [][]string{nil, {}} {
		if _, err := runner.RunSelected(context.Background(), config, files, JavspExecutionHooks{}); err == nil {
			t.Fatal("empty selection fell back to full scan")
		}
	}
	if calls != 0 {
		t.Fatal("empty selection invoked engine")
	}
	_, err := runner.RunSelected(context.Background(), config, []string{filepath.Join(config.InputDirectory, "TEST-001.mp4")}, JavspExecutionHooks{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queue cannot classify timeout: %v", err)
	}
}
