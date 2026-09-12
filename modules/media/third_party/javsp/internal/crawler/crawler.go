package crawler

import (
	"context"
	"fmt"
	"time"

	"github.com/yuukiy/javsp-go/internal/model"
)

type MovieRef struct {
	DVDID string
	CID   string
}

type Crawler interface {
	ID() model.CrawlerID
	Parse(ctx context.Context, input MovieRef) (*model.MovieInfo, error)
}

type Registry struct {
	crawlers map[model.CrawlerID]Crawler
}

func NewRegistry() *Registry {
	return &Registry{crawlers: make(map[model.CrawlerID]Crawler)}
}

func (r *Registry) Register(crawler Crawler) error {
	id := crawler.ID()
	if _, exists := r.crawlers[id]; exists {
		return fmt.Errorf("crawler already registered: %s", id)
	}
	r.crawlers[id] = crawler
	return nil
}

func (r *Registry) Get(id model.CrawlerID) (Crawler, bool) {
	crawler, ok := r.crawlers[id]
	return crawler, ok
}

type Options struct {
	Retry   int
	Timeout time.Duration
}

type Runner struct {
	registry *Registry
	retry    int
	timeout  time.Duration
}

func NewRunner(registry *Registry, opts Options) *Runner {
	return &Runner{registry: registry, retry: opts.Retry, timeout: opts.Timeout}
}

type Result struct {
	CrawlerID model.CrawlerID
	Info      *model.MovieInfo
	Err       error
}

func (r *Runner) Run(ctx context.Context, ids []model.CrawlerID, input MovieRef) []Result {
	return r.RunWithProgress(ctx, ids, input, nil)
}

// RunWithProgress reports the crawler that is actually about to execute. The
// callback may be invoked concurrently because configured crawlers run in
// parallel; callers that update a UI should serialize their reporter.
func (r *Runner) RunWithProgress(ctx context.Context, ids []model.CrawlerID, input MovieRef, onStart func(model.CrawlerID)) []Result {
	runCtx := ctx
	cancel := func() {}
	if r.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, r.timeout)
	}
	defer cancel()

	results := make([]Result, len(ids))
	type indexedResult struct {
		index  int
		result Result
	}
	deliveries := make(chan indexedResult, len(ids))
	pending := make([]bool, len(ids))
	remaining := 0
	for idx, id := range ids {
		results[idx].CrawlerID = id
		crawler, ok := r.registry.Get(id)
		if !ok {
			results[idx].Err = fmt.Errorf("crawler not registered: %s", id)
			continue
		}
		pending[idx] = true
		remaining++
		go func(index int, crawler Crawler) {
			if runCtx.Err() == nil && onStart != nil {
				onStart(crawler.ID())
			}
			deliveries <- indexedResult{index: index, result: r.runOne(runCtx, crawler, input)}
		}(idx, crawler)
	}

	store := func(delivery indexedResult) {
		if pending[delivery.index] {
			results[delivery.index] = delivery.result
			pending[delivery.index] = false
			remaining--
		}
	}
	for remaining > 0 {
		select {
		case delivery := <-deliveries:
			store(delivery)
		case <-runCtx.Done():
			for {
				select {
				case delivery := <-deliveries:
					store(delivery)
				default:
					for index, unfinished := range pending {
						if unfinished {
							results[index].Err = runCtx.Err()
						}
					}
					return results
				}
			}
		}
	}
	return results
}

func (r *Runner) runOne(ctx context.Context, crawler Crawler, input MovieRef) Result {
	result := Result{CrawlerID: crawler.ID()}
	for attempt := 0; attempt < r.retry; attempt++ {
		if err := ctx.Err(); err != nil {
			result.Err = err
			return result
		}
		info, err := crawler.Parse(ctx, input)
		if err == nil {
			result.Info = info
			result.Err = nil
			return result
		}
		result.Err = err
		if IsTerminal(err) || ctx.Err() != nil {
			if ctx.Err() != nil {
				result.Err = ctx.Err()
			}
			return result
		}
	}
	return result
}
