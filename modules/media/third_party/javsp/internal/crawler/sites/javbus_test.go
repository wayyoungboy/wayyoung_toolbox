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

func TestJavBusParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ABC-123" {
			t.Fatalf("path = %s, want /ABC-123", r.URL.Path)
		}
		_, _ = w.Write([]byte(`
<html>
<head><title>ABC-123</title></head>
<body>
<div class="container">
  <h3>ABC-123 Sample Title</h3>
  <a class="bigImage"><img src="/cover.jpg"></a>
  <div class="col-md-3 info">
    <p><span>識別碼:</span> ABC-123</p>
    <p><span>發行日期:</span> 2024-01-02</p>
    <p><span>長度:</span> 120分鐘</p>
    <p><span>導演:</span><a>Director</a></p>
    <p><span>製作商:</span><a>Producer</a></p>
    <p><span>發行商:</span><a>Publisher</a></p>
    <p><span>系列:</span><a>Series</a></p>
    <span class="genre"><label><a href="/genre/g1">Drama</a></label></span>
    <span class="genre"><label><a href="/genre/g2">Featured Actress</a></label></span>
  </div>
  <a class="avatar-box"><div><img title="Actor A" src="/actor-a.jpg"></div></a>
  <div id="sample-waterfall"><a href="/sample1.jpg"></a><a href="/sample2.jpg"></a></div>
</div>
</body>
</html>`))
	}))
	defer server.Close()

	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewJavBus(client, server.URL)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.Title != "Sample Title" {
		t.Fatalf("Title = %q, want Sample Title", info.Title)
	}
	if info.Cover != server.URL+"/cover.jpg" {
		t.Fatalf("Cover = %q", info.Cover)
	}
	if info.DVDID != "ABC-123" || info.PublishDate != "2024-01-02" || info.Duration != "120" {
		t.Fatalf("basic info = %#v", info)
	}
	if info.Director != "Director" || info.Producer != "Producer" || info.Publisher != "Publisher" || info.Serial != "Series" {
		t.Fatalf("staff info = %#v", info)
	}
	if len(info.Genre) != 2 || info.Genre[0] != "Drama" || info.GenreID[0] != "g1" {
		t.Fatalf("genre = %#v ids=%#v", info.Genre, info.GenreID)
	}
	if len(info.Actress) != 1 || info.Actress[0] != "Actor A" || info.ActressPics["Actor A"] != server.URL+"/actor-a.jpg" {
		t.Fatalf("actress = %#v pics=%#v", info.Actress, info.ActressPics)
	}
	if len(info.PreviewPics) != 2 || info.PreviewPics[0] != server.URL+"/sample1.jpg" {
		t.Fatalf("preview pics = %#v", info.PreviewPics)
	}
	if info.Uncensored == nil || *info.Uncensored {
		t.Fatalf("Uncensored = %v, want false", info.Uncensored)
	}
}

func TestJavBusNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title>404 Page Not Found</title></head></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewJavBus(client, server.URL)
	_, err = parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if !errors.Is(err, crawler.ErrMovieNotFound) {
		t.Fatalf("err = %v, want ErrMovieNotFound", err)
	}
}
