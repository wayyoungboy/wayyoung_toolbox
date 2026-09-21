package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/model"
	"github.com/yuukiy/javsp-go/internal/translate"
)

type translationRunner []crawler.Result

func (r translationRunner) Run(context.Context, []model.CrawlerID, crawler.MovieRef) []crawler.Result {
	return r
}

type gateTranslator struct{ unchanged bool }

func (g gateTranslator) Translate(_ context.Context, text string, _ []string) (translate.Result, error) {
	if g.unchanged {
		return translate.Result{Text: text}, nil
	}
	return translate.Result{Text: "今天是晴天"}, nil
}

func TestMergedOriginalTitleCannotBypassTranslationBeforePublish(t *testing.T) {
	for _, unchanged := range []bool{false, true} {
		t.Run(map[bool]string{false: "translated", true: "failed_provider"}[unchanged], func(t *testing.T) {
			movie, cfg, fixture := planFixture(t)
			cfg.Translator.Fields.Title = true
			fixture.info.Title = "今日は晴れです"
			runner := translationRunner{
				{CrawlerID: "javbus", Info: fixture.info},
				{CrawlerID: "javdb", Info: &model.MovieInfo{Title: "今天是晴天", OriginalTitle: "今日は晴れです 原題"}},
			}
			planned := false
			stop := errors.New("test stops before real publication")
			ok, err := processMovieWithHooks(context.Background(), movie, runner, nil, cfg, nil, nil, gateTranslator{unchanged}, "", nil, nil, ExecutionHooks{
				BeforeOrganize: func(context.Context, MoviePlan) error {
					planned = true
					if movie.Info.Title != "今天是晴天" {
						t.Errorf("publication received untranslated title: %q", movie.Info.Title)
					}
					return stop
				},
			})
			if ok || err == nil {
				t.Fatal("expected a stopped transaction")
			}
			if unchanged && planned {
				t.Fatal("failed translation reached organize/publication")
			}
			if !unchanged && (!planned || !errors.Is(err, stop)) {
				t.Fatalf("successful translation did not reach plan: %v", err)
			}
			if _, err := os.Stat(movie.Files[0]); err != nil {
				t.Fatal("translation failure moved source video")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(movie.Files[0])), "output")); !os.IsNotExist(err) {
				t.Fatal("translation gate wrote output")
			}
		})
	}
}
