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

func TestPrestigeParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/goods/goods_detail.php" || r.URL.Query().Get("sku") != "ABC-123" {
			t.Fatalf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`
<html><body>
<section class="px-4 mb-4 md:px-8 md:mb-16">
  <h1><span>ABC-123</span> Prestige Title</h1>
  <div class="c-ratio-image mr-8"><picture><source><img src="/cover.jpg?cache=1"></source></picture></div>
  <p>出演者：</p><div><p><a>Actor A</a><a>Actor B</a></p></div>
  <p>収録時間：</p><div>120分</div>
  <p>発売日：</p><div><a href="/search?date=2024-06-07">date</a></div>
  <p>メーカー：</p><div><a>Producer</a></div>
  <p>品番：</p><div><p>ABC-123</p></div>
  <p>ジャンル：</p><div><a>Drama</a><a>HD</a></div>
  <p>レーベル：</p><div><a>Series</a></div>
  <h2>商品紹介</h2><div><p>Plot text</p></div>
  <h2>サンプル画像</h2><div>
    <div><picture><source><img src="/sample1.jpg?x=1"></source></picture></div>
    <div><picture><source><img src="/sample2.jpg?x=1"></source></picture></div>
  </div>
</section>
</body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewPrestige(client, server.URL)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.Title != "Prestige Title" || info.Cover != server.URL+"/cover.jpg" {
		t.Fatalf("title/cover = %q / %q", info.Title, info.Cover)
	}
	if info.DVDID != "ABC-123" || info.Duration != "120" || info.PublishDate != "2024-06-07" {
		t.Fatalf("basic fields = %#v", info)
	}
	if len(info.Actress) != 2 || info.Actress[0] != "ActorA" {
		t.Fatalf("actress = %#v", info.Actress)
	}
	if info.Producer != "Producer" || info.Serial != "Series" || info.Plot != "Plot text" {
		t.Fatalf("producer/serial/plot = %#v", info)
	}
	if len(info.Genre) != 2 || info.Genre[1] != "HD" || len(info.PreviewPics) != 2 || info.PreviewPics[0] != server.URL+"/sample1.jpg" {
		t.Fatalf("genre/previews = %#v / %#v", info.Genre, info.PreviewPics)
	}
	if info.Uncensored == nil || *info.Uncensored {
		t.Fatalf("Uncensored = %v, want false", info.Uncensored)
	}
}

func TestPrestigeNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body></body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewPrestige(client, server.URL)
	_, err = parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if !errors.Is(err, crawler.ErrMovieNotFound) {
		t.Fatalf("err = %v, want ErrMovieNotFound", err)
	}
}
