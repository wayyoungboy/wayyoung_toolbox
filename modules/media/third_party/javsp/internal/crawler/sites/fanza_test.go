package sites

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
)

func TestFanzaVideoaParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/digital/videoa/-/detail/=/cid=cid001/" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`
<html><body>
<div class="hreview"><h1>Fanza Title</h1></div>
<table class="mg-b12"><tr><td>
  <div id="sample-video"><a href="https://img.example/cover.jpg"></a></div>
  <table>
    <tr><td>配信開始日：</td><td>2024/01/02</td></tr>
    <tr><td>収録時間：</td><td>123分</td></tr>
    <tr><td>監督：</td><td><a>Director</a></td></tr>
    <tr><td>シリーズ：</td><td><a>Series</a></td></tr>
    <tr><td>メーカー：</td><td><a>Producer</a></td></tr>
    <tr><td>ジャンル：</td><td><a href="/search/?keyword=g1">Genre A</a><a href="/article=keyword/id=g2/">Genre B</a></td></tr>
    <tr><td>品番：</td><td>cid001</td></tr>
  </table>
  <span id="performer"><a>Actor A</a><a>Actor B</a></span>
  <div class="mg-b20 lh4">Plot text</div>
  <a name="sample-image"><img src="https://img.example/1.jpg"></a>
  <a name="sample-image"><img src="https://img.example/2.jpg"></a>
  <p class="d-review__average"><strong>4.5</strong></p>
</td></tr></table>
</body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewFanza(client, server.URL, false)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{CID: "cid001"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.Title != "Fanza Title" || info.Cover != "https://img.example/cover.jpg" {
		t.Fatalf("title/cover = %q / %q", info.Title, info.Cover)
	}
	if info.CID != "cid001" || info.PublishDate != "2024-01-02" || info.Duration != "123" {
		t.Fatalf("basic fields = %#v", info)
	}
	if info.Director != "Director" || info.Serial != "Series" || info.Producer != "Producer" {
		t.Fatalf("staff = %#v", info)
	}
	if len(info.Actress) != 2 || len(info.Genre) != 2 || len(info.GenreID) != 2 {
		t.Fatalf("actress/genre = %#v / %#v ids=%#v", info.Actress, info.Genre, info.GenreID)
	}
	if info.Plot != "Plot text" || len(info.PreviewPics) != 2 || info.Score != "8.00" {
		t.Fatalf("plot/previews/score = %#v", info)
	}
	if info.Uncensored == nil || *info.Uncensored {
		t.Fatalf("Uncensored = %v, want false", info.Uncensored)
	}
}

func TestFanzaSearchFallbackParsesAnimePage(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/digital/videoa/-/detail/=/cid=anime001/":
			http.NotFound(w, r)
		case "/search/":
			_, _ = w.Write([]byte(`<html><body><ul id="list"><li><div><p><a href="` + server.URL + `/digital/anime/-/detail/=/cid=anime001/?foo=1">anime</a></p></div></li></ul></body></html>`))
		case "/digital/anime/-/detail/=/cid=anime001/":
			_, _ = w.Write([]byte(`
<html><body>
<h1 id="title">Anime Title</h1>
<table class="mg-b12"><tr><td>
  <img name="package-image" src="https://img.example/anime.jpg">
  <table>
    <tr><td>発売日：</td><td>2024/05/06</td></tr>
    <tr><td>収録時間：</td><td>90分</td></tr>
    <tr><td>シリーズ：</td><td><a>Anime Series</a></td></tr>
    <tr><td>メーカー：</td><td><a>Anime Producer</a></td></tr>
    <tr><td>ジャンル：</td><td><a href="/article=keyword/id=anime-g1/">Anime Genre</a></td></tr>
    <tr><td>品番：</td><td>anime001</td></tr>
    <tr><td>平均評価：</td><td><img src="/review/40.gif"></td></tr>
  </table>
  <div class="mg-b20 lh4"><p>Anime plot</p></div>
  <a name="sample-image"><img data-lazy="https://img.example/anime-1.jpg"></a>
</td></tr></table>
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
	parser := NewFanza(client, server.URL, false)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{CID: "anime001"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.URL != server.URL+"/digital/anime/-/detail/=/cid=anime001/" || info.CID != "anime001" {
		t.Fatalf("url/cid = %q / %q", info.URL, info.CID)
	}
	if info.Title != "Anime Title" || info.Cover != "https://img.example/anime.jpg" || info.PublishDate != "2024-05-06" {
		t.Fatalf("title/cover/date = %#v", info)
	}
	if info.Duration != "90" || info.Serial != "Anime Series" || info.Producer != "Anime Producer" {
		t.Fatalf("duration/staff = %#v", info)
	}
	if info.Plot != "Anime plot" || info.Score != "8.00" || len(info.PreviewPics) != 1 {
		t.Fatalf("plot/score/previews = %#v", info)
	}
	if len(info.Genre) != 1 || info.GenreID[0] != "anime-g1" {
		t.Fatalf("genre = %#v / %#v", info.Genre, info.GenreID)
	}
	if info.Uncensored == nil || *info.Uncensored {
		t.Fatalf("Uncensored = %v, want false", info.Uncensored)
	}
}
