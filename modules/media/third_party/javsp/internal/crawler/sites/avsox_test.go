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

func TestAVSoxParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tw/search/ABC-123":
			_, _ = w.Write([]byte(`
<html><body>
  <a class="movie-box" href="/movie/abc"><div class="photo-info"><span><date>ABC-123</date></span></div></a>
</body></html>`))
		case "/movie/abc":
			_, _ = w.Write([]byte(`
<html><body>
<div class="container">
  <h3>ABC-123 AVSox Title</h3>
  <a class="bigImage" href="/cover.jpg"></a>
  <div><div class="col-md-3 info">
    <p><span style="color:red">ABC-123</span></p>
    <p><span>发行时间:</span> 2024-02-03</p>
    <p><span>长度:</span> 98分钟</p>
    <p>制作商: </p><p><a>Producer</a></p>
    <p>系列:</p><p><a>Series</a></p>
    <p><span class="genre"><a>Drama</a></span><span class="genre"><a>HD</a></span></p>
  </div></div>
  <a class="avatar-box"><span>Actor A</span></a>
</div>
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
	parser := NewAVSox(client, server.URL)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.Title != "AVSox Title" || info.Cover != server.URL+"/cover.jpg" {
		t.Fatalf("title/cover = %q / %q", info.Title, info.Cover)
	}
	if info.DVDID != "ABC-123" || info.PublishDate != "2024-02-03" || info.Duration != "98" {
		t.Fatalf("basic info = %#v", info)
	}
	if info.Producer != "Producer" || info.Serial != "Series" {
		t.Fatalf("producer/serial = %q / %q", info.Producer, info.Serial)
	}
	if len(info.Genre) != 2 || info.Genre[0] != "Drama" || info.Actress[0] != "Actor A" {
		t.Fatalf("genre/actress = %#v / %#v", info.Genre, info.Actress)
	}
	if info.Uncensored == nil || *info.Uncensored {
		t.Fatalf("Uncensored = %v, want false", info.Uncensored)
	}
}

func TestAVSoxPreservesRequestedIDWhenDetailPrefixIsSplit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tw/search/JUR-822":
			_, _ = w.Write([]byte(`<html><body>
  <a class="movie-box" href="/movie/jur822"><div class="photo-info"><span><date>JUR-822</date></span></div></a>
</body></html>`))
		case "/movie/jur822":
			_, _ = w.Write([]byte(`<html><body><div class="container">
  <h3>JUR-822 Regression Title</h3>
  <a class="bigImage" href="/cover.jpg"></a>
  <div><div class="col-md-3 info">
    <p>JUR<span style="color:red">-822</span></p>
    <p><span>发行时间:</span> 2026-09-08</p>
    <p><span>长度:</span> 120分钟</p>
  </div></div>
  <a class="avatar-box"><span>Actor</span></a>
</div></body></html>`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	info, err := NewAVSox(client, server.URL).Parse(context.Background(), crawler.MovieRef{DVDID: "JUR-822"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.DVDID != "JUR-822" {
		t.Fatalf("DVDID = %q, want JUR-822", info.DVDID)
	}
}

func TestAVSoxNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body></body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewAVSox(client, server.URL)
	_, err = parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if !errors.Is(err, crawler.ErrMovieNotFound) {
		t.Fatalf("err = %v, want ErrMovieNotFound", err)
	}
}
