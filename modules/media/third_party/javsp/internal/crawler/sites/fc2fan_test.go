package sites

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
)

func TestFC2FanLocalMirrorParse(t *testing.T) {
	root := t.TempDir()
	htmlPath := filepath.Join(root, "FC2-1879420.html")
	if err := os.WriteFile(htmlPath, []byte(`
<html><body>
<div class="col-sm-8">
  <h3>FC2Fan Title</h3>
  <h5><strong>影片评分</strong> 87分</h5>
  <h5><strong>资源参数</strong> 无码 HD</h5>
  <h5><strong>卖家信息</strong><a>Producer</a></h5>
  <h5><strong>影片标签</strong><a>Genre A</a><a>Genre B</a></h5>
  <h5><strong>女优名字</strong><a>Actor A</a></h5>
  <ul class="slides"><li><img src="pics/1.jpg"></li><li><img src="pics/2.jpg"></li></ul>
</div>
</body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewFC2Fan(client, "https://example.test", root)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "FC2-1879420"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.Title != "FC2Fan Title" || info.Score != "8.7" || info.Producer != "Producer" {
		t.Fatalf("title/score/producer = %#v", info)
	}
	if info.Uncensored == nil || !*info.Uncensored {
		t.Fatalf("Uncensored = %v, want true", info.Uncensored)
	}
	if len(info.Genre) != 2 || len(info.Actress) != 1 {
		t.Fatalf("genre/actress = %#v / %#v", info.Genre, info.Actress)
	}
	if info.Cover != filepath.Join(root, "pics/1.jpg") || len(info.PreviewPics) != 2 {
		t.Fatalf("cover/previews = %q / %#v", info.Cover, info.PreviewPics)
	}
}

func TestFC2FanLocalMirrorNotFound(t *testing.T) {
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewFC2Fan(client, "https://example.test", t.TempDir())
	_, err = parser.Parse(context.Background(), crawler.MovieRef{DVDID: "FC2-1879420"})
	if !errors.Is(err, crawler.ErrMovieNotFound) {
		t.Fatalf("err = %v, want ErrMovieNotFound", err)
	}
}
