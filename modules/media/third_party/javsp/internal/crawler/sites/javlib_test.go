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

func TestJavLibParseSearchResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cn/vl_searchbyid.php":
			_, _ = w.Write([]byte(`<html><body><div class="video" id="v1"><a href="/cn/detail.php?v=abc" title="normal"><div class="id">ABC-123</div></a></div></body></html>`))
		case "/cn/detail.php":
			_, _ = w.Write([]byte(`
<html><body><div><div id="rightcolumn">
  <div><h3><a>ABC-123 JavLib Title</a></h3></div>
  <img id="video_jacket_img" src="//img.example/cover.jpg">
  <div id="video_info">
    <div id="video_id"><table><tr><td class="text">ABC-123</td></tr></table></div>
    <div id="video_date"><table><tr><td class="text">2024-02-03</td></tr></table></div>
    <div id="video_length"><span class="text">120</span></div>
    <span class="director"><a>Director</a></span>
    <span class="maker"><a>Producer</a></span>
    <span class="label"><a>Publisher</a></span>
    <span class="score">(8.1)</span>
    <span class="genre"><a>Genre A</a></span>
    <span class="star"><a>Actor A</a></span>
  </div>
</div></div></body></html>`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewJavLib(client, server.URL)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.Title != "JavLib Title" || info.Cover != "https://img.example/cover.jpg" {
		t.Fatalf("title/cover = %q / %q", info.Title, info.Cover)
	}
	if info.DVDID != "ABC-123" || info.PublishDate != "2024-02-03" || info.Duration != "120" {
		t.Fatalf("basic fields = %#v", info)
	}
	if info.Director != "Director" || info.Producer != "Producer" || info.Publisher != "Publisher" || info.Score != "8.1" {
		t.Fatalf("staff/score = %#v", info)
	}
	if len(info.Genre) != 1 || len(info.Actress) != 1 {
		t.Fatalf("genre/actress = %#v / %#v", info.Genre, info.Actress)
	}
}

func TestJavLibNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body></body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewJavLib(client, server.URL)
	_, err = parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if !errors.Is(err, crawler.ErrMovieNotFound) {
		t.Fatalf("err = %v, want ErrMovieNotFound", err)
	}
}
