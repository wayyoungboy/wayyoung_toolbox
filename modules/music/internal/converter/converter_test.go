package converter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestScanFiltersNCMFilesAndHonorsRecursion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "first.ncm"), "first")
	mustWrite(t, filepath.Join(root, "SECOND.NCM"), "second")
	mustWrite(t, filepath.Join(root, "notes.txt"), "ignore")
	mustWrite(t, filepath.Join(root, "nested", "third.ncm"), "third")

	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(root, "first.ncm"), filepath.Join(root, "linked.ncm")); err != nil {
			t.Fatalf("create symlink: %v", err)
		}
	}

	flat, err := Scan(context.Background(), ScanOptions{SourceDir: root})
	if err != nil {
		t.Fatalf("flat scan: %v", err)
	}
	assertRelatives(t, flat, []string{"SECOND.NCM", "first.ncm"})

	recursive, err := Scan(context.Background(), ScanOptions{SourceDir: root, Recursive: true})
	if err != nil {
		t.Fatalf("recursive scan: %v", err)
	}
	assertRelatives(t, recursive, []string{"SECOND.NCM", "first.ncm", filepath.Join("nested", "third.ncm")})
}

func TestScanRejectsMissingOrNonDirectorySource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	file := filepath.Join(root, "song.ncm")
	mustWrite(t, file, "song")

	for name, source := range map[string]string{
		"missing": filepath.Join(root, "missing"),
		"file":    file,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Scan(context.Background(), ScanOptions{SourceDir: source})
			if !errors.Is(err, ErrInvalidSource) {
				t.Fatalf("expected ErrInvalidSource, got %v", err)
			}
		})
	}
}

func TestBatchPreservesTreeAndNeverDeletesSource(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	output := t.TempDir()
	input := filepath.Join(source, "album", "song.ncm")
	mustWrite(t, input, "encrypted")

	items, err := Scan(context.Background(), ScanOptions{SourceDir: source, Recursive: true})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	runner := NewRunner(fakeEngine{extension: ".flac"})
	results := runner.Run(context.Background(), RunOptions{
		SourceDir:    source,
		OutputDir:    output,
		PreserveTree: true,
		Conflict:     ConflictSkip,
		Concurrency:  2,
	}, items)

	if len(results) != 1 || results[0].Status != StatusCompleted {
		t.Fatalf("unexpected results: %#v", results)
	}
	expected := filepath.Join(output, "album", "song.flac")
	if results[0].Output != expected {
		t.Fatalf("output = %q, want %q", results[0].Output, expected)
	}
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("converted file missing: %v", err)
	}
	if _, err := os.Stat(input); err != nil {
		t.Fatalf("source file must remain: %v", err)
	}
}

func TestBatchConflictPolicies(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		policy     ConflictPolicy
		wantStatus Status
		wantName   string
		wantBody   string
	}{
		{name: "skip", policy: ConflictSkip, wantStatus: StatusSkipped, wantName: "song.mp3", wantBody: "existing"},
		{name: "rename", policy: ConflictRename, wantStatus: StatusCompleted, wantName: "song (1).mp3", wantBody: "converted"},
		{name: "overwrite", policy: ConflictOverwrite, wantStatus: StatusCompleted, wantName: "song.mp3", wantBody: "converted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := t.TempDir()
			output := t.TempDir()
			input := filepath.Join(source, "song.ncm")
			mustWrite(t, input, "encrypted")
			mustWrite(t, filepath.Join(output, "song.mp3"), "existing")
			items, err := Scan(context.Background(), ScanOptions{SourceDir: source})
			if err != nil {
				t.Fatalf("scan: %v", err)
			}

			results := NewRunner(fakeEngine{extension: ".mp3"}).Run(context.Background(), RunOptions{
				SourceDir:   source,
				OutputDir:   output,
				Conflict:    test.policy,
				Concurrency: 1,
			}, items)
			if len(results) != 1 || results[0].Status != test.wantStatus {
				t.Fatalf("unexpected result: %#v", results)
			}
			body, err := os.ReadFile(filepath.Join(output, test.wantName))
			if err != nil {
				t.Fatalf("read output: %v", err)
			}
			if string(body) != test.wantBody {
				t.Fatalf("output body = %q, want %q", body, test.wantBody)
			}
			if _, err := os.Stat(input); err != nil {
				t.Fatalf("source file must remain: %v", err)
			}
		})
	}
}

func TestRunnerValidatesConcurrencyAndPaths(t *testing.T) {
	t.Parallel()
	runner := NewRunner(fakeEngine{extension: ".mp3"})
	root := t.TempDir()
	for name, options := range map[string]RunOptions{
		"zero concurrency":     {SourceDir: root, OutputDir: root, Concurrency: 0},
		"too much concurrency": {SourceDir: root, OutputDir: root, Concurrency: 9},
		"relative source":      {SourceDir: "relative", OutputDir: root, Concurrency: 1},
		"relative output":      {SourceDir: root, OutputDir: "relative", Concurrency: 1},
		"unknown conflict":     {SourceDir: root, OutputDir: root, Concurrency: 1, Conflict: "unknown"},
	} {
		t.Run(name, func(t *testing.T) {
			results := runner.Run(context.Background(), options, []Item{{Source: filepath.Join(root, "song.ncm"), Relative: "song.ncm"}})
			if len(results) != 1 || results[0].Status != StatusFailed {
				t.Fatalf("expected validation failure, got %#v", results)
			}
		})
	}
}

func TestRunnerHonorsCanceledContext(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	output := t.TempDir()
	input := filepath.Join(source, "song.ncm")
	mustWrite(t, input, "encrypted")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results := NewRunner(fakeEngine{extension: ".mp3"}).Run(ctx, RunOptions{
		SourceDir:   source,
		OutputDir:   output,
		Conflict:    ConflictSkip,
		Concurrency: 1,
	}, []Item{{Source: input, Relative: "song.ncm"}})
	if len(results) != 1 || results[0].Status != StatusCanceled {
		t.Fatalf("expected canceled result, got %#v", results)
	}
	if _, err := os.Stat(input); err != nil {
		t.Fatalf("source file must remain: %v", err)
	}
}

func TestRunnerMarksInFlightConversionCanceled(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	output := t.TempDir()
	input := filepath.Join(source, "song.ncm")
	mustWrite(t, input, "encrypted")
	engine := &cancelEngine{started: make(chan struct{})}
	runner := NewRunner(engine)
	ctx, cancel := context.WithCancel(context.Background())
	resultChannel := make(chan []Result, 1)
	go func() {
		resultChannel <- runner.Run(ctx, RunOptions{
			SourceDir:   source,
			OutputDir:   output,
			Conflict:    ConflictSkip,
			Concurrency: 1,
		}, []Item{{Source: input, Relative: "song.ncm"}})
	}()
	<-engine.started
	cancel()
	results := <-resultChannel
	if len(results) != 1 || results[0].Status != StatusCanceled {
		t.Fatalf("expected in-flight cancellation, got %#v", results)
	}
}

func TestRunnerReportsEngineErrorWithoutLeavingTemporaryFiles(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	output := t.TempDir()
	input := filepath.Join(source, "song.ncm")
	mustWrite(t, input, "encrypted")

	results := NewRunner(errorEngine{}).Run(context.Background(), RunOptions{
		SourceDir:   source,
		OutputDir:   output,
		Conflict:    ConflictSkip,
		Concurrency: 1,
	}, []Item{{Source: input, Relative: "song.ncm"}})
	if len(results) != 1 || results[0].Status != StatusFailed || !strings.Contains(results[0].Error, "decode failed") {
		t.Fatalf("expected engine failure, got %#v", results)
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary files were not cleaned up: %v", entries)
	}
}

func TestEnginePanicIsIsolatedAsFailure(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	output := t.TempDir()
	input := filepath.Join(source, "broken.ncm")
	mustWrite(t, input, "broken")
	items, err := Scan(context.Background(), ScanOptions{SourceDir: source})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	results := NewRunner(panicEngine{}).Run(context.Background(), RunOptions{
		SourceDir:   source,
		OutputDir:   output,
		Conflict:    ConflictSkip,
		Concurrency: 1,
	}, items)
	if len(results) != 1 || results[0].Status != StatusFailed || !strings.Contains(results[0].Error, "解析异常") {
		t.Fatalf("panic was not isolated: %#v", results)
	}
	if _, err := os.Stat(input); err != nil {
		t.Fatalf("source file must remain: %v", err)
	}
}

type fakeEngine struct {
	extension string
}

func (engine fakeEngine) Convert(_ context.Context, source, stagingDir string, _ bool) (string, error) {
	output := filepath.Join(stagingDir, strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))+engine.extension)
	if err := os.WriteFile(output, []byte("converted"), 0o600); err != nil {
		return "", err
	}
	return output, nil
}

type panicEngine struct{}

func (panicEngine) Convert(context.Context, string, string, bool) (string, error) {
	panic("malformed metadata")
}

type errorEngine struct{}

func (errorEngine) Convert(context.Context, string, string, bool) (string, error) {
	return "", errors.New("decode failed")
}

type cancelEngine struct {
	started chan struct{}
}

func (engine *cancelEngine) Convert(ctx context.Context, _, _ string, _ bool) (string, error) {
	close(engine.started)
	<-ctx.Done()
	return "", ctx.Err()
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertRelatives(t *testing.T, items []Item, want []string) {
	t.Helper()
	got := make([]string, 0, len(items))
	for _, item := range items {
		got = append(got, item.Relative)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("relative files = %v, want %v", got, want)
	}
}
