package sites

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
)

func TestMGStageParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/product/product_detail/ABC-123/":
			_, _ = w.Write([]byte(`
<html><body>
<div class="common_detail_cover"><h1>MGStage Title</h1></div>
<div class="detail_left">
  <a id="EnlargeImage" href="/cover.jpg"></a>
  <table>
    <tr><th>出演：</th><td> Actor Text <a>Actor Link</a></td></tr>
    <tr><th>メーカー：</th><td><a>Producer</a></td></tr>
    <tr><th>収録時間：</th><td>88min</td></tr>
    <tr><th>品番：</th><td>ABC-123</td></tr>
    <tr><th>配信開始日：</th><td>2024/05/06</td></tr>
    <tr><th>シリーズ：</th><td><a>Series</a></td></tr>
    <tr><th>ジャンル：</th><td><a>Drama</a><a>HD</a></td></tr>
  </table>
  <table><tr><td class="review"><span></span>4.12 stars</td></tr></table>
  <dl id="introduction"><dd><p>Plot text</p></dd></dl>
  <a class="sample_image" href="/sample1.jpg"></a>
  <a class="sample_image" href="/sample2.jpg"></a>
  <a class="button_sample" href="/sampleplayer/pid001"></a>
</div>
</body></html>`))
		case "/sampleplayer/sampleRespons.php":
			if r.URL.Query().Get("pid") != "pid001" {
				t.Fatalf("pid = %s, want pid001", r.URL.Query().Get("pid"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"url":"https://cdn.example/sample/movie.ism/request?uid=x"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewMGStage(client, server.URL, true)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.Title != "MGStage Title" || info.Cover != server.URL+"/cover.jpg" {
		t.Fatalf("title/cover = %q / %q", info.Title, info.Cover)
	}
	if len(info.Actress) != 2 || info.Actress[0] != "Actor Text" || info.Actress[1] != "Actor Link" {
		t.Fatalf("actress = %#v", info.Actress)
	}
	if info.Duration != "88" || info.PublishDate != "2024-05-06" || info.Score != "8.24" {
		t.Fatalf("duration/date/score = %#v", info)
	}
	if info.Serial != "Series" || len(info.Genre) != 2 || info.Genre[1] != "HD" {
		t.Fatalf("serial/genre = %q / %#v", info.Serial, info.Genre)
	}
	if info.Plot != "Plot text" || len(info.PreviewPics) != 2 {
		t.Fatalf("plot/previews = %q / %#v", info.Plot, info.PreviewPics)
	}
	if info.PreviewVideo != "https://cdn.example/sample/movie.mp4" {
		t.Fatalf("PreviewVideo = %q", info.PreviewVideo)
	}
	if info.Uncensored == nil || *info.Uncensored {
		t.Fatalf("Uncensored = %v, want false", info.Uncensored)
	}
}
