package crawler

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuukiy/javsp-go/internal/model"
)

type fakeCrawler struct {
	id       model.CrawlerID
	delay    time.Duration
	failures []error
	attempts atomic.Int32
}

type contextIgnoringCrawler struct {
	id       model.CrawlerID
	release  <-chan struct{}
	finished chan<- struct{}
}

func (c *contextIgnoringCrawler) ID() model.CrawlerID { return c.id }

func (c *contextIgnoringCrawler) Parse(context.Context, MovieRef) (*model.MovieInfo, error) {
	<-c.release
	close(c.finished)
	return &model.MovieInfo{Title: "late result"}, nil
}

func (f *fakeCrawler) ID() model.CrawlerID {
	return f.id
}

func (f *fakeCrawler) Parse(ctx context.Context, input MovieRef) (*model.MovieInfo, error) {
	attempt := int(f.attempts.Add(1))
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if attempt <= len(f.failures) {
		return nil, f.failures[attempt-1]
	}
	return &model.MovieInfo{DVDID: input.DVDID, Title: string(f.id)}, nil
}

func TestRegistryRejectsDuplicate(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&fakeCrawler{id: "javbus"}); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if err := registry.Register(&fakeCrawler{id: "javbus"}); err == nil {
		t.Fatalf("expected duplicate register error")
	}
}

func TestRunnerReturnsConfiguredOrder(t *testing.T) {
	registry := NewRegistry()
	slow := &fakeCrawler{id: "slow", delay: 20 * time.Millisecond}
	fast := &fakeCrawler{id: "fast"}
	_ = registry.Register(slow)
	_ = registry.Register(fast)

	runner := NewRunner(registry, Options{Retry: 1})
	results := runner.Run(context.Background(), []model.CrawlerID{"slow", "fast"}, MovieRef{DVDID: "ABC-123"})
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
	if results[0].CrawlerID != "slow" || results[1].CrawlerID != "fast" {
		t.Fatalf("results out of order: %#v", results)
	}
}

func TestRunnerReportsEachActualCrawlerStart(t *testing.T) {
	registry := NewRegistry()
	_ = registry.Register(&fakeCrawler{id: "javbus"})
	_ = registry.Register(&fakeCrawler{id: "javdb"})
	runner := NewRunner(registry, Options{Retry: 1})
	var mu sync.Mutex
	var started []string

	results := runner.RunWithProgress(context.Background(), []model.CrawlerID{"javbus", "missing", "javdb"}, MovieRef{DVDID: "MIKR-069"}, func(id model.CrawlerID) {
		mu.Lock()
		started = append(started, string(id))
		mu.Unlock()
	})
	sort.Strings(started)
	if strings.Join(started, ",") != "javbus,javdb" {
		t.Fatalf("actual crawler starts = %v", started)
	}
	if len(results) != 3 || results[1].Err == nil {
		t.Fatalf("results = %#v", results)
	}
}

func TestRunnerDoesNotReportCrawlerStartAfterCancellation(t *testing.T) {
	registry := NewRegistry()
	_ = registry.Register(&fakeCrawler{id: "javbus"})
	runner := NewRunner(registry, Options{Retry: 1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var starts atomic.Int32
	results := runner.RunWithProgress(ctx, []model.CrawlerID{"javbus"}, MovieRef{DVDID: "MIKR-069"}, func(model.CrawlerID) {
		starts.Add(1)
	})
	if starts.Load() != 0 {
		t.Fatalf("reported %d crawler start(s) after cancellation", starts.Load())
	}
	if len(results) != 1 || !errors.Is(results[0].Err, context.Canceled) {
		t.Fatalf("results = %#v, want canceled", results)
	}
}

func TestRunnerRetriesTransientErrors(t *testing.T) {
	registry := NewRegistry()
	transient := errors.New("temporary")
	crawler := &fakeCrawler{id: "retry", failures: []error{transient, transient}}
	_ = registry.Register(crawler)
	runner := NewRunner(registry, Options{Retry: 3})
	results := runner.Run(context.Background(), []model.CrawlerID{"retry"}, MovieRef{DVDID: "ABC-123"})
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("results = %#v", results)
	}
	if crawler.attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", crawler.attempts.Load())
	}
}

func TestRunnerZeroRetriesPerformsNoParseAttempt(t *testing.T) {
	registry := NewRegistry()
	crawler := &fakeCrawler{id: "zero"}
	_ = registry.Register(crawler)
	runner := NewRunner(registry, Options{Retry: 0})

	results := runner.Run(context.Background(), []model.CrawlerID{"zero"}, MovieRef{DVDID: "ABC-123"})
	if got := crawler.attempts.Load(); got != 0 {
		t.Fatalf("attempts = %d, want 0", got)
	}
	if len(results) != 1 || results[0].Info != nil || results[0].Err != nil {
		t.Fatalf("results = %#v, want one unattempted result", results)
	}
}

func TestRunnerDoesNotRetryTerminalErrors(t *testing.T) {
	for _, terminal := range []error{
		ErrMovieNotFound,
		ErrMovieDuplicate,
		ErrSiteBlocked,
		ErrSitePermission,
		ErrCredential,
	} {
		registry := NewRegistry()
		crawler := &fakeCrawler{id: "terminal", failures: []error{terminal}}
		_ = registry.Register(crawler)
		runner := NewRunner(registry, Options{Retry: 3})
		results := runner.Run(context.Background(), []model.CrawlerID{"terminal"}, MovieRef{DVDID: "ABC-123"})
		if len(results) != 1 || !errors.Is(results[0].Err, terminal) {
			t.Fatalf("terminal %v results = %#v", terminal, results)
		}
		if crawler.attempts.Load() != 1 {
			t.Fatalf("terminal %v attempts = %d, want 1", terminal, crawler.attempts.Load())
		}
	}
}

func TestRunnerHonorsContextCancel(t *testing.T) {
	registry := NewRegistry()
	crawler := &fakeCrawler{id: "slow", delay: time.Second}
	_ = registry.Register(crawler)
	runner := NewRunner(registry, Options{Retry: 3})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	results := runner.Run(ctx, []model.CrawlerID{"slow"}, MovieRef{DVDID: "ABC-123"})
	if len(results) != 1 || !errors.Is(results[0].Err, context.DeadlineExceeded) {
		t.Fatalf("results = %#v, want deadline exceeded", results)
	}
	if crawler.attempts.Load() != 1 {
		t.Fatalf("attempts = %d, want 1", crawler.attempts.Load())
	}
}

func TestRunnerOverallTimeoutReturnsOrderedPartialResults(t *testing.T) {
	registry := NewRegistry()
	release := make(chan struct{})
	finished := make(chan struct{})
	_ = registry.Register(&contextIgnoringCrawler{id: "stalled", release: release, finished: finished})
	_ = registry.Register(&fakeCrawler{id: "fast"})
	runner := NewRunner(registry, Options{Retry: 1, Timeout: 25 * time.Millisecond})

	started := time.Now()
	results := runner.Run(context.Background(), []model.CrawlerID{"stalled", "fast"}, MovieRef{DVDID: "ABC-123"})
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("Run elapsed = %s, want deadline-bounded return", elapsed)
	}
	if len(results) != 2 || results[0].CrawlerID != "stalled" || results[1].CrawlerID != "fast" {
		t.Fatalf("results out of input order: %#v", results)
	}
	if !errors.Is(results[0].Err, context.DeadlineExceeded) {
		t.Fatalf("stalled result = %#v, want deadline exceeded", results[0])
	}
	if results[1].Err != nil || results[1].Info == nil || results[1].Info.Title != "fast" {
		t.Fatalf("completed result = %#v, want retained success", results[1])
	}

	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("late crawler did not finish")
	}
	time.Sleep(10 * time.Millisecond)
	if !errors.Is(results[0].Err, context.DeadlineExceeded) || results[0].Info != nil {
		t.Fatalf("late worker mutated returned results: %#v", results[0])
	}
}
