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

func TestJav321Parse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.Method != http.MethodPost {
			t.Fatalf("request = %s %s, want POST /search", r.Method, r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("sn") != "ABC-123" {
			t.Fatalf("sn = %q, want ABC-123", r.Form.Get("sn"))
		}
		_, _ = w.Write([]byte(`
<html><body>
<ul class="dropdown-menu"><li><a href="/video/abc00123"></a></li></ul>
<div class="panel-heading"><h3>Jav321 Title</h3></div>
<div class="col-md-9">
  <a href="/company/1">Producer</a>
  <a href="/genre/4025/1">Drama</a>
  <a href="/series/10">Series</a>
  <b>品番</b>: ABC-123
  <b>配信開始日</b>: 2024-04-05
  <b>収録時間</b>: 76 minutes
  <b>平均評価</b><img data-original="/img/35.gif">
  <video><source src="/sample.mp4"></video>
</div>
<div class="thumbnail"><a href="/star/1"><img src="/actor.jpg"> Actor A</a></div>
<div class="panel-body"><div class="row"><div class="col-md-12">Plot text</div></div></div>
<div class="col-xs-12 col-md-12"><p>
  <a><img class="img-responsive" src="/cover.jpg"></a>
  <a><img class="img-responsive" src="/preview.jpg"></a>
</p></div>
</body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewJav321(client, server.URL)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.URL != server.URL+"/video/abc00123" || info.CID != "abc00123" {
		t.Fatalf("url/cid = %q / %q", info.URL, info.CID)
	}
	if info.Title != "Jav321 Title" || info.DVDID != "ABC-123" {
		t.Fatalf("title/dvdid = %q / %q", info.Title, info.DVDID)
	}
	if info.Cover != server.URL+"/cover.jpg" || len(info.PreviewPics) != 1 || info.PreviewPics[0] != server.URL+"/preview.jpg" {
		t.Fatalf("cover/previews = %q / %#v", info.Cover, info.PreviewPics)
	}
	if info.Producer != "Producer" || info.Serial != "Series" || info.Duration != "76" || info.Score != "7.0" {
		t.Fatalf("producer/serial/duration/score = %#v", info)
	}
	if len(info.Genre) != 1 || info.GenreID[0] != "4025" {
		t.Fatalf("genre = %#v ids=%#v", info.Genre, info.GenreID)
	}
	if len(info.Actress) != 1 || info.Actress[0] != "Actor A" || info.ActressPics["Actor A"] != server.URL+"/actor.jpg" {
		t.Fatalf("actress = %#v pics=%#v", info.Actress, info.ActressPics)
	}
	if info.Plot != "Plot text" || info.PreviewVideo != server.URL+"/sample.mp4" {
		t.Fatalf("plot/video = %q / %q", info.Plot, info.PreviewVideo)
	}
}

func TestJav321NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body><ul class="dropdown-menu"><li><a href="/search"></a></li></ul></body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewJav321(client, server.URL)
	_, err = parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if !errors.Is(err, crawler.ErrMovieNotFound) {
		t.Fatalf("err = %v, want ErrMovieNotFound", err)
	}
}
