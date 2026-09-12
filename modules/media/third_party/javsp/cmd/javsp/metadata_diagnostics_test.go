package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/model"
)

func TestMissingMetadataPreservesSafeSourceDiagnostics(t *testing.T) {
	root := t.TempDir()
	cfg := testProcessConfig(root)
	cfg.Crawler.RequiredKeys = []string{"cover", "title"}
	runner := &fakeMovieRunner{results: []crawler.Result{
		{CrawlerID: "fc2", Err: crawler.ErrMovieNotFound},
		{CrawlerID: "javmenu", Err: crawler.ErrWebsite},
		{CrawlerID: "fc2ppvdb", Err: errors.New("http status 526: https://user:secret@example.test/path?token=private")},
		{CrawlerID: "javdb", Err: context.DeadlineExceeded},
		{CrawlerID: "fc2db", Info: &model.MovieInfo{Title: "Sample title"}},
	}}
	movie := &model.Movie{DVDID: "FC2-4419625", DataSource: model.DataSourceFC2}
	ok, err := processMovie(context.Background(), movie, runner, nil, cfg, nil, nil, nil, root, nil)
	if ok || err == nil {
		t.Fatalf("missing metadata must be an error: ok=%v err=%v", ok, err)
	}
	for _, text := range []string{"missing required metadata", "required: cover,title", "fc2=not_found", "javmenu=parse_error", "fc2ppvdb=http_526", "javdb=timeout", "fc2db=partial(title=yes,cover=no)"} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("missing %q in %q", text, err)
		}
	}
	for _, secret := range []string{"secret", "private", "example.test"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("diagnostics leak %q", secret)
		}
	}
	if movie.Info != nil {
		t.Fatal("failed scrape published partial metadata")
	}
}
