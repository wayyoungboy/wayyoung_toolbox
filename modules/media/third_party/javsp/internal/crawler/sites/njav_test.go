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

func TestNJAVParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			if r.URL.Query().Get("keyword") != "ABC-123" {
				t.Fatalf("keyword = %s", r.URL.Query().Get("keyword"))
			}
			_, _ = w.Write([]byte(`<html><body><div class="box-item"><div class="detail"><a href="/video/abc">ABC-123 Search Title</a></div></div></body></html>`))
		case "/video/abc":
			_, _ = w.Write([]byte(`
<html><body>
<div class="container"><div><div class="col">
  <div class="d-flex justify-content-between align-items-start"><div><h1>ABC-123 NJAV Title uncensored</h1></div></div>
  <div id="player" data-poster="/poster.jpg"></div>
  <div class="description"><p>Plot A</p><p>Plot B</p></div>
  <div class="magnet"><a href="magnet:?xt=uncensored"></a></div>
  <div class="detail-item">
    <div><span>タグ:</span><span><a>Tag A</a></span></div>
    <div><span>ジャンル:</span><span><a>Genre A</a></span></div>
    <div><span>女優:</span><span><a>Actor A</a></span></div>
    <div><span>シリーズ:</span><span><a>Series</a></span></div>
    <div><span>メーカー:</span><span><a>Producer</a></span></div>
    <div><span>コード:</span><span>ABC-123</span></div>
    <div><span>公開日:</span><span>2024-01-02</span></div>
    <div><span>再生時間:</span><span>01:02:03</span></div>
  </div>
</div></div></div>
</body></html>`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewNJAV(client, server.URL)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.Title != "NJAVTitleuncensored" || info.Cover != server.URL+"/poster.jpg" {
		t.Fatalf("title/cover = %q / %q", info.Title, info.Cover)
	}
	if info.Plot != "Plot A Plot B" || info.Duration != "62" || info.PublishDate != "2024-01-02" {
		t.Fatalf("plot/duration/date = %#v", info)
	}
	if info.Producer != "Producer" || info.Serial != "Series" || len(info.Actress) != 1 || len(info.Genre) != 2 {
		t.Fatalf("fields = %#v", info)
	}
	if len(info.Magnet) != 1 || info.Magnet[0] != "magnet:?xt=uncensored" {
		t.Fatalf("magnet = %#v", info.Magnet)
	}
	if info.Uncensored == nil || !*info.Uncensored {
		t.Fatalf("Uncensored = %v, want true", info.Uncensored)
	}
}

func TestNJAVNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body></body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewNJAV(client, server.URL)
	_, err = parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if !errors.Is(err, crawler.ErrMovieNotFound) {
		t.Fatalf("err = %v, want ErrMovieNotFound", err)
	}
}
