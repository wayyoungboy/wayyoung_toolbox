package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuukiy/javsp-go/internal/config"
	"github.com/yuukiy/javsp-go/internal/model"
)

func TestRunReturnsCancellationWithoutStartingWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var events []Event
	_, err := Run(ctx, Options{Progress: func(event Event) { events = append(events, event) }})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if len(events) != 1 || events[0].Phase != PhaseCanceled {
		t.Fatalf("events = %#v, want one canceled event", events)
	}
}

func TestRunAcceptsInMemoryConfig(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Run(context.Background(), Options{
		ConfigData:     data,
		ResourceRoot:   filepath.Clean(".."),
		InputDirectory: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected empty scan to fail")
	}
	if strings.Contains(err.Error(), "config path is required") {
		t.Fatalf("in-memory config unexpectedly required a path: %v", err)
	}
}

func TestMovieProgressIncludesCrawlerSitesAndFailureReason(t *testing.T) {
	cfg := &config.Config{}
	cfg.Crawler.Selection.Normal = []string{"javbus", "javdb"}
	movie := &model.Movie{DVDID: "MIKR-069", DataSource: model.DataSourceNormal}

	started := movieProgressEvent(movie, cfg, movieJobResult{index: 0}, 2, 0)
	if started.Site != "" || started.Movie != "MIKR-069" {
		t.Fatalf("started event = %#v", started)
	}
	failed := movieProgressEvent(movie, cfg, movieJobResult{index: 0, err: errors.New("blocked")}, 2, 0)
	if failed.Message != "blocked" || failed.Site != "" {
		t.Fatalf("failed event = %#v", failed)
	}
}

func TestProgressReporterSerializesAndPreventsCurrentRegression(t *testing.T) {
	var events []Event
	reporter := newProgressReporter(func(event Event) { events = append(events, event) })
	reporter.Emit(Event{Phase: PhaseProcessing, Movie: "THIRD", Current: 3, Total: 4, Processed: 2})
	reporter.Emit(Event{Phase: PhaseProcessing, Movie: "FIRST", Site: "javbus", Current: 1, Total: 4, Processed: 0})
	if len(events) != 2 || events[0].Current != 3 || events[1].Current != 3 {
		t.Fatalf("events = %#v, want monotonic current", events)
	}
	if events[1].Site != "javbus" {
		t.Fatalf("actual site lost from progress: %#v", events[1])
	}
	if events[1].Processed != 2 {
		t.Fatalf("processed regressed: %#v", events[1])
	}
	reporter.Emit(Event{Phase: PhaseCanceled, Current: 3, Total: 4, Processed: 2})
	reporter.Emit(Event{Phase: PhaseProcessing, Movie: "LATE", Site: "javdb", Current: 4, Total: 4, Processed: 2})
	if len(events) != 3 || events[2].Phase != PhaseCanceled {
		t.Fatalf("late worker overwrote terminal progress: %#v", events)
	}
}

func TestRunReportsEmptyScanThroughProgress(t *testing.T) {
	root := t.TempDir()
	var events []Event
	result, err := Run(context.Background(), Options{
		ConfigPath:     filepath.Join("..", "config.yml"),
		InputDirectory: root,
		Output:         io.Discard,
		Progress:       func(event Event) { events = append(events, event) },
	})
	if err == nil {
		t.Fatal("Run unexpectedly succeeded for an empty scan")
	}
	if result.Total != 0 || result.Processed != 0 {
		t.Fatalf("result = %#v, want zero totals", result)
	}
	want := []Phase{PhasePreparing, PhaseScanning, PhaseFailed}
	if len(events) != len(want) {
		t.Fatalf("events = %#v, want phases %v", events, want)
	}
	for index := range want {
		if events[index].Phase != want[index] {
			t.Fatalf("event %d phase = %q, want %q", index, events[index].Phase, want[index])
		}
	}
}
