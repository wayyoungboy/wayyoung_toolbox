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

func TestArzonParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/itemlist.html":
			if r.URL.Query().Get("q") != "ABC-123" {
				t.Fatalf("q = %s, want ABC-123", r.URL.Query().Get("q"))
			}
			_, _ = w.Write([]byte(`<html><body><h2><a href="/item_1.html">item</a></h2></body></html>`))
		case "/item_1.html":
			_, _ = w.Write([]byte(`
<html><body>
<div class="detail_title_new2"><h1>Arzon Title</h1></div>
<table><tr><td align="center"><a><img src="//img.example/cover.jpg"></a></td></tr></table>
<div class="item_text"> Plot text </div>
<div class="detail_img"><img src="//img.example/m_sample1.jpg"><img src="//img.example/m_sample2.jpg"></div>
<div class="item_register"><table>
  <tr><td>AV女優：</td><td>Actor A<br>Actor B</td></tr>
  <tr><td>AVメーカー：</td><td>Producer</td></tr>
  <tr><td>AVレーベル：</td><td>Label</td></tr>
  <tr><td>シリーズ：</td><td>Series</td></tr>
  <tr><td>監督：</td><td>Director</td></tr>
  <tr><td>発売日：</td><td>2024/11/12</td></tr>
  <tr><td>収録時間：</td><td>77分</td></tr>
  <tr><td>品番：</td><td>ABC-123</td></tr>
  <tr><td>タグ：</td><td>Tag</td></tr>
</table></div>
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
	parser := NewArzon(client, server.URL, false)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.Title != "Arzon Title" || info.Cover != "https://img.example/cover.jpg" {
		t.Fatalf("title/cover = %q / %q", info.Title, info.Cover)
	}
	if info.Plot != "Plot text" || info.Producer != "Producer" || info.Serial != "Series" || info.Director != "Director" {
		t.Fatalf("fields = %#v", info)
	}
	if len(info.Actress) != 2 || info.Actress[1] != "Actor B" {
		t.Fatalf("actress = %#v", info.Actress)
	}
	if len(info.Genre) != 2 || info.Genre[0] != "Label" || info.Genre[1] != "Tag" {
		t.Fatalf("genre = %#v", info.Genre)
	}
	if info.PublishDate != "2024-11-12" || info.Duration != "77" {
		t.Fatalf("date/duration = %q / %q", info.PublishDate, info.Duration)
	}
	if len(info.PreviewPics) != 2 || info.PreviewPics[0] != "https://img.example/sample1.jpg" {
		t.Fatalf("preview pics = %#v", info.PreviewPics)
	}
}

func TestArzonNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body></body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewArzon(client, server.URL, false)
	_, err = parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if !errors.Is(err, crawler.ErrMovieNotFound) {
		t.Fatalf("err = %v, want ErrMovieNotFound", err)
	}
}
