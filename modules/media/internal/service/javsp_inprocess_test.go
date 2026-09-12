package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeEmbeddedJavsp struct {
	mu       sync.Mutex
	started  chan struct{}
	panicRun bool
	complete bool
	runCount int
	options  embeddedJavspOptions
}

func TestEmbeddedJavspUsesMemoryConfigWithoutRewritingLegacyFile(t *testing.T) {
	bundle, data := javspTestRuntime(t)
	fake := &fakeEmbeddedJavsp{}
	runner := newJavspRunnerWithEngine(bundle, data, fake)
	if err := runner.ensureRuntime(); err != nil {
		t.Fatal(err)
	}
	legacyBefore, err := os.ReadFile(runner.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	config := defaultJavspConfig()
	config.InputDirectory = t.TempDir()
	config.NetworkRetry = 9
	config.TimeoutSeconds = 1
	_, _ = runner.Run(context.Background(), config)
	legacyAfter, err := os.ReadFile(runner.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(legacyBefore, legacyAfter) {
		t.Fatal("embedded run rewrote the legacy config.yml")
	}
	fake.mu.Lock()
	options := fake.options
	fake.mu.Unlock()
	if len(options.ConfigData) == 0 {
		t.Fatalf("engine options = %#v, want in-memory config only", options)
	}
	if options.ResourceRoot != runner.workRoot {
		t.Fatalf("resource root = %q, want %q", options.ResourceRoot, runner.workRoot)
	}
}

func (f *fakeEmbeddedJavsp) Run(ctx context.Context, options embeddedJavspOptions) (embeddedJavspResult, error) {
	f.mu.Lock()
	f.runCount++
	f.options = options
	f.mu.Unlock()
	if f.panicRun {
		panic("embedded engine fixture")
	}
	options.Progress(JavspProgress{Phase: "processing", Movie: "TEST-001", Current: 1, Total: 2})
	if f.complete {
		return embeddedJavspResult{Total: 1, Processed: 1}, nil
	}
	if f.started != nil {
		close(f.started)
	}
	<-ctx.Done()
	return embeddedJavspResult{}, ctx.Err()
}

func (f *fakeEmbeddedJavsp) Version() string { return "embedded-test" }

func TestEmbeddedJavspStopBeforeWorkerStartsIsNotLost(t *testing.T) {
	bundle, data := javspTestRuntime(t)
	fake := &fakeEmbeddedJavsp{}
	runner := newJavspRunnerWithEngine(bundle, data, fake)
	config := defaultJavspConfig()
	config.InputDirectory = t.TempDir()
	config.TimeoutSeconds = 5

	// This models the window after startJavsp accepts a job but before its
	// goroutine enters Run.
	runner.BeginRun()
	runner.StopCurrent()
	started := time.Now()
	_, err := runner.Run(context.Background(), config)
	if err == nil || !strings.Contains(err.Error(), "终止") {
		t.Fatalf("Run error = %v, want the latched cancellation", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("latched cancellation took %v", elapsed)
	}
	fake.mu.Lock()
	runCount := fake.runCount
	fake.mu.Unlock()
	if runCount != 0 {
		t.Fatalf("embedded engine started %d time(s) after cancellation", runCount)
	}
}

func TestEmbeddedJavspCancelAfterRunDoesNotLeakIntoNextRun(t *testing.T) {
	bundle, data := javspTestRuntime(t)
	fake := &fakeEmbeddedJavsp{complete: true}
	runner := newJavspRunnerWithEngine(bundle, data, fake)
	config := defaultJavspConfig()
	config.InputDirectory = t.TempDir()
	config.TimeoutSeconds = 5

	runner.BeginRun()
	if _, err := runner.Run(context.Background(), config); err != nil {
		t.Fatalf("first Run error = %v", err)
	}
	if runner.StopCurrent() {
		t.Fatal("completed run remained cancellable during service bookkeeping")
	}

	runner.BeginRun()
	if _, err := runner.Run(context.Background(), config); err != nil {
		t.Fatalf("second Run inherited stale cancellation: %v", err)
	}
	fake.mu.Lock()
	runCount := fake.runCount
	fake.mu.Unlock()
	if runCount != 2 {
		t.Fatalf("engine run count = %d, want 2", runCount)
	}
}

func TestPrepareConfigDataIgnoresCorruptLegacyFile(t *testing.T) {
	bundle, data := javspTestRuntime(t)
	runner := newJavspRunnerWithEngine(bundle, data, &fakeEmbeddedJavsp{})
	if err := runner.ensureRuntime(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runner.ConfigPath(), []byte("not: [valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := defaultJavspConfig()
	config.InputDirectory = t.TempDir()
	if _, err := runner.PrepareConfigData(config); err != nil {
		t.Fatalf("database-backed config depended on corrupt legacy YAML: %v", err)
	}
}

func TestEmbeddedJavspRunnerReportsProgressAndHonorsTimeout(t *testing.T) {
	bundle, data := javspTestRuntime(t)
	fake := &fakeEmbeddedJavsp{started: make(chan struct{})}
	runner := newJavspRunnerWithEngine(bundle, data, fake)
	config := defaultJavspConfig()
	config.InputDirectory = t.TempDir()
	config.TimeoutSeconds = 1

	result, err := runner.Run(context.Background(), config)
	var runErr *JavspRunError
	if !errors.As(err, &runErr) || !strings.Contains(runErr.Message, "超过 1 秒") {
		t.Fatalf("Run error = %v, want bounded timeout", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0 for an in-process run", result.ExitCode)
	}
	fake.mu.Lock()
	gotOutput := fake.options.Output
	fake.mu.Unlock()
	if gotOutput == nil {
		t.Fatal("embedded engine did not receive the live output writer")
	}
	if progress := runner.LastProgress(); progress.Phase != "canceled" || progress.Total != 2 {
		t.Fatalf("last progress = %#v, want canceled progress preserving totals", progress)
	}
}

func TestEmbeddedJavspRunnerStopCancelsCurrentRun(t *testing.T) {
	bundle, data := javspTestRuntime(t)
	fake := &fakeEmbeddedJavsp{started: make(chan struct{})}
	runner := newJavspRunnerWithEngine(bundle, data, fake)
	config := defaultJavspConfig()
	config.InputDirectory = t.TempDir()
	config.TimeoutSeconds = 30
	done := make(chan error, 1)
	go func() { _, err := runner.Run(context.Background(), config); done <- err }()
	select {
	case <-fake.started:
	case <-time.After(2 * time.Second):
		t.Fatal("embedded engine did not start")
	}
	runner.StopCurrent()
	select {
	case err := <-done:
		var runErr *JavspRunError
		if !errors.As(err, &runErr) || !strings.Contains(runErr.Message, "应用停止") {
			t.Fatalf("stop error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StopCurrent did not cancel the embedded engine")
	}
}

func TestEmbeddedJavspRunnerContainsEnginePanic(t *testing.T) {
	bundle, data := javspTestRuntime(t)
	runner := newJavspRunnerWithEngine(bundle, data, &fakeEmbeddedJavsp{panicRun: true})
	config := defaultJavspConfig()
	config.InputDirectory = t.TempDir()
	config.TimeoutSeconds = 5
	if _, err := runner.Run(context.Background(), config); err == nil || !strings.Contains(err.Error(), "内部错误") {
		t.Fatalf("Run error = %v, want contained engine panic", err)
	}
}

func TestJavspComponentDoesNotRequireExecutable(t *testing.T) {
	bundle, data := javspTestRuntime(t)
	runner := newJavspRunnerWithEngine(bundle, data, &fakeEmbeddedJavsp{})
	component := runner.Component(context.Background())
	if available, _ := component["available"].(bool); !available {
		t.Fatalf("component unavailable without child binary: %#v", component)
	}
	if component["mode"] != "embedded" || component["version"] != "embedded-test" {
		t.Fatalf("component = %#v", component)
	}
}

func TestPackageHasNoStandaloneJavspExecutable(t *testing.T) {
	root := filepath.Join("..", "..", "fnos-media-acquisition-center", "app", "javsp")
	var found []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasPrefix(entry.Name(), "javsp-linux-") {
			found = append(found, path)
		}
		return nil
	})
	if len(found) != 0 {
		t.Fatalf("standalone JAVSP executables remain in package: %v", found)
	}
}

func TestJavspRunnerHasNoChildProcessExecutionPath(t *testing.T) {
	source, err := os.ReadFile("javsp.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"os/exec", "exec.Command", "activePID", "run.pid", "javsp-linux-"} {
		if strings.Contains(string(source), forbidden) {
			t.Errorf("legacy child-process implementation remains: %q", forbidden)
		}
	}
}

func javspTestRuntime(t *testing.T) (string, string) {
	t.Helper()
	bundle, data := t.TempDir(), t.TempDir()
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
	_ = io.Discard
	return bundle, data
}
