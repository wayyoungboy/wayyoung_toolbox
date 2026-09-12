package sites

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
)

func TestFC2Parse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/article/718323/" {
			t.Fatalf("path = %s, want /article/718323/", r.URL.Path)
		}
		_, _ = w.Write([]byte(`
<html><body>
<div class="items_article_left">
  <div class="items_article_headerInfo"><h3>FC2 </h3><h3>Title</h3></div>
  <div class="items_article_MainitemThumb">
    <span><img src="/thumb.jpg"><p class="items_article_info">01:20:30</p></span>
  </div>
  <li>by </li><li><a>Producer</a></li>
  <a class="tag tagTag">Genre A</a>
  <a class="tag tagTag">Genre B</a>
  <div class="items_article_Releasedate"><p>販売日 : 2017/11/30</p></div>
  <ul data-feed="sample-images"><li><a href="/preview1.jpg"></a></li><li><a href="/preview2.jpg"></a></li></ul>
  <a class="items_article_Stars"><p><span class="items_article_Star4"></span></p></a>
</div>
</body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewFC2(client, server.URL, false)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "FC2-718323"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.Title != "FC2 Title" || info.Cover != server.URL+"/preview1.jpg" {
		t.Fatalf("title/cover = %q / %q", info.Title, info.Cover)
	}
	if info.DVDID != "FC2-718323" || info.Duration != "80" || info.PublishDate != "2017-11-30" {
		t.Fatalf("basic fields = %#v", info)
	}
	if info.Producer != "Producer" || len(info.Genre) != 2 || info.Score != "8.00" {
		t.Fatalf("producer/genre/score = %#v", info)
	}
	if len(info.PreviewPics) != 2 || info.PreviewPics[1] != server.URL+"/preview2.jpg" {
		t.Fatalf("preview pics = %#v", info.PreviewPics)
	}
}

func TestFC2HardworkingPreviewVideo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/article/718323/":
			_, _ = w.Write([]byte(`
<html><body>
<div class="items_article_left">
  <div class="items_article_headerInfo"><h3>Title</h3></div>
  <div class="items_article_MainitemThumb"><span><img src="/thumb.jpg"><p class="items_article_info">10:00</p></span></div>
  <li>by </li><li><a>Producer</a></li>
  <div class="items_article_Releasedate"><p>販売日 : 2017/11/30</p></div>
  <section class="items_article_Contents"><iframe src="/widget/article/718323/description?ac=abc"></iframe></section>
</div>
</body></html>`))
		case "/api/v2/videos/718323/sample":
			if r.URL.Query().Get("key") != "abc" {
				t.Fatalf("key = %s, want abc", r.URL.Query().Get("key"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"path":"https://cdn.example/sample.mp4"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewFC2(client, server.URL, true)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "FC2-718323"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.PreviewVideo != "https://cdn.example/sample.mp4" {
		t.Fatalf("PreviewVideo = %q", info.PreviewVideo)
	}
}
