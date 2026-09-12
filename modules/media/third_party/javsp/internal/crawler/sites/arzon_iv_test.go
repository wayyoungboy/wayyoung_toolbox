package sites

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
)

func TestArzonIVParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/imagelist.html":
			_, _ = w.Write([]byte(`<html><body><h2><a href="/iv_item.html">item</a></h2></body></html>`))
		case "/iv_item.html":
			_, _ = w.Write([]byte(`
<html><body>
<div class="detail_title_new"><h1>Arzon IV Title</h1></div>
<table><tr><td align="center"><a><img src="//img.example/iv-cover.jpg"></a></td></tr></table>
<div class="item_text"> IV plot </div>
<div class="item_register"><table>
  <tr><td>タレント：</td><td>Actor A<br>Actor B</td></tr>
  <tr><td>イメージメーカー：</td><td>Producer</td></tr>
  <tr><td>イメージレーベル：</td><td>IV Label</td></tr>
  <tr><td>監督：</td><td>Director</td></tr>
  <tr><td>発売日：</td><td>2024/12/13</td></tr>
  <tr><td>収録時間：</td><td>44分</td></tr>
  <tr><td>品番：</td><td>IV-123</td></tr>
  <tr><td>タグ：</td><td>IV Tag</td></tr>
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
	parser := NewArzonIV(client, server.URL)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "IV-123"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.Title != "Arzon IV Title" || info.Cover != "https://img.example/iv-cover.jpg" {
		t.Fatalf("title/cover = %q / %q", info.Title, info.Cover)
	}
	if len(info.Genre) != 2 || info.Genre[0] != "IV Label" || info.Genre[1] != "IV Tag" {
		t.Fatalf("genre = %#v", info.Genre)
	}
	if info.Producer != "Producer" || info.Director != "Director" || info.PublishDate != "2024-12-13" || info.Duration != "44" {
		t.Fatalf("fields = %#v", info)
	}
}
