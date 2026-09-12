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

func TestFalenoParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/top/works/fns222/" {
			t.Fatalf("request path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`
<html><body>
<div class="bar02"><h1>Faleno Title</h1></div>
<div class="box_works01_img">
  <a class="pop_sample" href="/media/FNS-222_PR.mp4"><img src="/media/FNS-222_1200.jpg?quality=60"></a>
</div>
<div class="box_works01_ga"><ul>
  <li><a class="pop_img" href="/media/FNS-222_s_1.jpg"><img src="/media/thumb-1.jpg"></a></li>
  <li><a class="pop_img" href="/media/FNS-222_s_2.jpg"><img src="/media/thumb-2.jpg"></a></li>
</ul></div>
<div class="box_works01_text"><p>Plot text</p></div>
<div class="box_works01_list clearfix"><ul>
  <li><span>出演女優</span><p>Actor A</p></li>
  <li><span>収録時間</span><p>115分</p></li>
</ul><ul>
  <li><span>配信日</span><p>2026/6/19</p></li>
  <li><span>発売日</span><p>2026/07/23</p></li>
</ul></div>
</body></html>`))
	}))
	defer server.Close()

	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewFaleno(client, server.URL+"/top")
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "FNS-222"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.DVDID != "FNS-222" || info.Title != "Faleno Title" || info.Plot != "Plot text" {
		t.Fatalf("basic fields = %#v", info)
	}
	if info.Cover != server.URL+"/media/FNS-222_1200.jpg?quality=60" {
		t.Fatalf("Cover = %q", info.Cover)
	}
	if len(info.Actress) != 1 || info.Actress[0] != "Actor A" || info.Duration != "115" {
		t.Fatalf("actress/duration = %#v / %q", info.Actress, info.Duration)
	}
	if info.PublishDate != "2026-06-19" || info.Producer != "FALENO" || info.Publisher != "FALENO" {
		t.Fatalf("date/producer/publisher = %q / %q / %q", info.PublishDate, info.Producer, info.Publisher)
	}
	if len(info.PreviewPics) != 2 || info.PreviewPics[0] != server.URL+"/media/FNS-222_s_1.jpg" {
		t.Fatalf("PreviewPics = %#v", info.PreviewPics)
	}
	if info.PreviewVideo != server.URL+"/media/FNS-222_PR.mp4" {
		t.Fatalf("PreviewVideo = %q", info.PreviewVideo)
	}
}

func TestFalenoNotFound(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewFaleno(client, server.URL)
	_, err = parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if !errors.Is(err, crawler.ErrMovieNotFound) {
		t.Fatalf("err = %v, want ErrMovieNotFound", err)
	}
}

func TestFalenoMissingTitleIsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body><div class="box_works01_text"><p>Plot only</p></div></body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewFaleno(client, server.URL).Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if !errors.Is(err, crawler.ErrMovieNotFound) {
		t.Fatalf("err = %v, want ErrMovieNotFound", err)
	}
}

func TestFalenoFallsBackToReleaseDate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`
<html><body>
<div class="bar02"><h1>Title</h1></div>
<div class="box_works01_list"><ul><li><span>発売日</span><p>2026/07/23</p></li></ul></div>
</body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	info, err := NewFaleno(client, server.URL).Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if err != nil {
		t.Fatal(err)
	}
	if info.PublishDate != "2026-07-23" {
		t.Fatalf("PublishDate = %q", info.PublishDate)
	}
}

func TestFalenoMapsSiteBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<html><title>Just a moment...</title><body>Just a moment...</body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewFaleno(client, server.URL).Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if !errors.Is(err, crawler.ErrSiteBlocked) {
		t.Fatalf("err = %v, want ErrSiteBlocked", err)
	}
}
