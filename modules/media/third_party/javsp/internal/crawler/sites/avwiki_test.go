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

func TestAVWikiParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ABC-123" {
			t.Fatalf("path = %s, want /ABC-123", r.URL.Path)
		}
		_, _ = w.Write([]byte(`
<html><body>
<header><div><a class="image-link-border"><img src="/small.jpg" srcset="/small.jpg 300w, /large.jpg 900w"></a></div></header>
<section class="article-body">
  <div><p>【ABC-123】AVWiki Title</p><cite><a href="https://source.example/page?aff=1">source</a></cite></div>
  <dl class="dltable">
    <dt>メーカー</dt><dd><a>Producer</a></dd>
    <dt>AV女優名</dt><dd><a>Actor A</a><a>Actor B</a></dd>
    <dt>メーカー品番</dt><dd>ABC-123</dd>
    <dt>シリーズ</dt><dd><a>Series</a></dd>
    <dt>配信開始日</dt><dd>2024-09-10</dd>
  </dl>
</section>
</body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewAVWiki(client, server.URL)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.Title != "AVWiki Title" || info.Cover != server.URL+"/large.jpg" {
		t.Fatalf("title/cover = %q / %q", info.Title, info.Cover)
	}
	if info.URL != server.URL+"/ABC-123" || info.DVDID != "ABC-123" || info.Producer != "Producer" {
		t.Fatalf("basic fields = %#v", info)
	}
	if len(info.Actress) != 2 || info.Actress[1] != "Actor B" {
		t.Fatalf("actress = %#v", info.Actress)
	}
	if info.Serial != "Series" || info.PublishDate != "2024-09-10" {
		t.Fatalf("serial/date = %q / %q", info.Serial, info.PublishDate)
	}
	if info.Uncensored == nil || *info.Uncensored {
		t.Fatalf("Uncensored = %v, want false", info.Uncensored)
	}
}

func TestAVWikiNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body></body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewAVWiki(client, server.URL)
	_, err = parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if !errors.Is(err, crawler.ErrMovieNotFound) {
		t.Fatalf("err = %v, want ErrMovieNotFound", err)
	}
}
