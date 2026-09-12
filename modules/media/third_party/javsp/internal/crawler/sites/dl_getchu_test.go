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

func TestDLGetchuParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/i/item4041026" {
			t.Fatalf("path = %s, want /i/item4041026", r.URL.Path)
		}
		_, _ = w.Write([]byte(`
<html><body>
<form action="https://dl.getchu.com/cart/">
  <div>
    <table></table>
    <table><tr><td><div>DL Getchu Title</div></td></tr></table>
    <table><tr><td><table>
      <tr><td class="bluetext">サークル</td><td><a>Producer</a></td></tr>
      <tr><td class="bluetext">作者</td><td><a>Actor A</a></td></tr>
      <tr><td class="bluetext">画像数&amp;ページ数</td><td>動画90分</td></tr>
      <tr><td class="bluetext">配信開始日</td><td>2024/08/09</td></tr>
      <tr><td class="bluetext">趣向</td><td><a>Genre A</a><a>Genre B</a></td></tr>
      <tr><td class="bluetext">作品内容</td><td>Plot line 1 ※ note</td></tr>
    </table></td></tr></table>
  </div>
</form>
<img src="/images/4041026top.jpg">
<img src="/images/4041026_001.jpg">
<img src="/images/4041026_002.jpg">
</body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewDLGetchu(client, server.URL)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "GETCHU-4041026"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.Title != "DL Getchu Title" || info.Cover != server.URL+"/images/4041026top.jpg" {
		t.Fatalf("title/cover = %q / %q", info.Title, info.Cover)
	}
	if info.DVDID != "GETCHU-4041026" || info.Producer != "Producer" || info.Duration != "90" || info.PublishDate != "2024-08-09" {
		t.Fatalf("basic fields = %#v", info)
	}
	if len(info.Actress) != 1 || info.Actress[0] != "Actor A" || len(info.Genre) != 2 || info.Genre[1] != "Genre B" {
		t.Fatalf("actress/genre = %#v / %#v", info.Actress, info.Genre)
	}
	if info.Plot != "Plot line 1 " {
		t.Fatalf("Plot = %q", info.Plot)
	}
	if len(info.PreviewPics) != 2 || info.PreviewPics[0] != server.URL+"/images/4041026_001.jpg" {
		t.Fatalf("PreviewPics = %#v", info.PreviewPics)
	}
}

func TestDLGetchuInvalidID(t *testing.T) {
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewDLGetchu(client, "http://example.test")
	_, err = parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if !errors.Is(err, crawler.ErrMovieNotFound) {
		t.Fatalf("err = %v, want ErrMovieNotFound", err)
	}
}
