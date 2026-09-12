package sites

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
)

func TestFC2PPVDBParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/articles/123456" {
			t.Fatalf("path = %s, want /articles/123456", r.URL.Path)
		}
		_, _ = w.Write([]byte(`
<html><body>
<div class="container lg:px-5 px-2 py-12 mx-auto">
  <div>
    <h2><a>FC2 Sample Title</a></h2>
    <img alt="123456" src="/cover.jpg">
    <div>収録時間：<span>88</span></div>
    <div>女優：<span><a>Actor A</a><a>Actor B</a></span></div>
    <div>タグ：<span><a>Tag A</a><a>Tag B</a></span></div>
    <div>販売日：<span>2024-03-04</span></div>
    <div>販売者：<span><a>Seller</a></span></div>
    <div>モザイク：<span>無</span></div>
    <a href="/sample.mp4">サンプル動画</a>
  </div>
</div>
</body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewFC2PPVDB(client, server.URL)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "FC2-123456"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.Title != "FC2 Sample Title" || info.Cover != server.URL+"/cover.jpg" {
		t.Fatalf("title/cover = %q / %q", info.Title, info.Cover)
	}
	if info.Duration != "88" || info.PublishDate != "2024-03-04" || info.Publisher != "Seller" {
		t.Fatalf("duration/date/publisher = %#v", info)
	}
	if len(info.Actress) != 2 || info.Actress[1] != "Actor B" || len(info.Genre) != 2 || info.Genre[0] != "Tag A" {
		t.Fatalf("actress/genre = %#v / %#v", info.Actress, info.Genre)
	}
	if info.Uncensored == nil || !*info.Uncensored {
		t.Fatalf("Uncensored = %v, want true", info.Uncensored)
	}
	if info.PreviewVideo != server.URL+"/sample.mp4" {
		t.Fatalf("PreviewVideo = %q", info.PreviewVideo)
	}
}

func TestFC2PPVDBNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body></body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewFC2PPVDB(client, server.URL)
	_, err = parser.Parse(context.Background(), crawler.MovieRef{DVDID: "FC2-123456"})
	if !errors.Is(err, crawler.ErrMovieNotFound) {
		t.Fatalf("err = %v, want ErrMovieNotFound", err)
	}
}
