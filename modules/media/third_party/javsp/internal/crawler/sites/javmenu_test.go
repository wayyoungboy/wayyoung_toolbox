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

func TestJavMenuParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/FC2-718323" {
			t.Fatalf("path = %s, want /FC2-718323", r.URL.Path)
		}
		_, _ = w.Write([]byte(`
<html><body>
<div class="col-md-9 px-0">
  <div class="col-12 mb-3"><h1><strong>FC2-718323 JavMenu Title 免費在線看</strong></h1></div>
  <div class="single-video"><video data-poster=" /cover.jpg "></video></div>
  <div class="card-body">
    <div><span>日期:</span><span>2024-10-11</span></div>
    <div><span>時長:</span><span>55分鐘</span></div>
    <div><span>製作:</span><a><span>Producer</span></a></div>
    <div><span>女優:</span><span><a>Actor A</a></span></div>
    <a class="genre" href="/censored/genre/g1">Genre A</a>
  </div>
  <table class="magnet-table"><tbody>
    <tr><td><a href="magnet:?xt=1[javdb.com]"></a></td></tr>
    <tr><td><a href="magnet:?xt=2"></a></td></tr>
  </tbody></table>
  <a data-fancybox="gallery" href="/preview1.jpg"></a>
  <a data-fancybox="gallery" href="/preview2.jpg"></a>
</div>
</body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewJavMenu(client, server.URL)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "FC2-718323"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.Title != "JavMenu Title" || info.Cover != server.URL+"/cover.jpg" {
		t.Fatalf("title/cover = %q / %q", info.Title, info.Cover)
	}
	if info.PublishDate != "2024-10-11" || info.Duration != "55" || info.Producer != "Producer" {
		t.Fatalf("basic fields = %#v", info)
	}
	if len(info.Genre) != 1 || info.GenreID[0] != "censored/g1" {
		t.Fatalf("genre = %#v ids=%#v", info.Genre, info.GenreID)
	}
	if len(info.Actress) != 1 || info.Actress[0] != "Actor A" {
		t.Fatalf("actress = %#v", info.Actress)
	}
	if len(info.Magnet) != 2 || info.Magnet[0] != "magnet:?xt=1" {
		t.Fatalf("magnet = %#v", info.Magnet)
	}
	if len(info.PreviewPics) != 2 || info.PreviewPics[0] != server.URL+"/preview1.jpg" {
		t.Fatalf("preview pics = %#v", info.PreviewPics)
	}
}

func TestJavMenuNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body></body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewJavMenu(client, server.URL)
	_, err = parser.Parse(context.Background(), crawler.MovieRef{DVDID: "FC2-718323"})
	if !errors.Is(err, crawler.ErrMovieNotFound) {
		t.Fatalf("err = %v, want ErrMovieNotFound", err)
	}
}
