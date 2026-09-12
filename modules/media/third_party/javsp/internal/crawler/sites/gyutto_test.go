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

func TestGyuttoParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/i/item266923" || r.URL.Query().Get("select_uaflag") != "1" {
			t.Fatalf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`
<html><body>
<h1>Gyutto Title</h1>
<a class="highslide"><img src="/cover.jpg"></a>
<a class="highslide"><img src="/sample1.jpg"></a>
<dl class="BasicInfo clearfix"><dt>サークル</dt><dd><a>Producer</a></dd></dl>
<dl class="BasicInfo clearfix"><dt>ジャンル</dt><dd><a>Genre A</a><a>Genre B</a></dd></dl>
<dl class="BasicInfo clearfix"><dt>配信開始日</dt><dd>2024年07月08日</dd></dl>
<div class="unit_DetailLead"><p>Plot text</p></div>
</body></html>`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewGyutto(client, server.URL)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "GYUTTO-266923"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.Title != "Gyutto Title" || info.Cover != server.URL+"/cover.jpg" {
		t.Fatalf("title/cover = %q / %q", info.Title, info.Cover)
	}
	if info.DVDID != "GYUTTO-266923" || info.Producer != "Producer" || info.PublishDate != "2024-07-08" {
		t.Fatalf("basic fields = %#v", info)
	}
	if len(info.Genre) != 2 || info.Genre[1] != "Genre B" {
		t.Fatalf("genre = %#v", info.Genre)
	}
	if len(info.PreviewPics) != 2 || info.PreviewPics[1] != server.URL+"/sample1.jpg" {
		t.Fatalf("preview pics = %#v", info.PreviewPics)
	}
}

func TestGyuttoRejectsInvalidID(t *testing.T) {
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewGyutto(client, "http://example.test")
	_, err = parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if !errors.Is(err, crawler.ErrMovieNotFound) {
		t.Fatalf("err = %v, want ErrMovieNotFound", err)
	}
}
