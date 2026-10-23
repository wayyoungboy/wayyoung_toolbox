//go:build catalog_browser

package service

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Opt-in integration journey: built frontend, actual HTTP/SQLite catalog and a
// generated non-personal H.264 sample. No NAS files or external sites are read.
func TestCatalogBrowserJourney(t *testing.T) {
	service := testService(t)
	public, err := filepath.Abs("../../frontend/dist")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(public, "index.html")); err != nil {
		t.Fatal("build frontend before catalog_browser test: ", err)
	}
	service.publicDir = public
	root := t.TempDir()
	sample := filepath.Join(t.TempDir(), "sample.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100", "-t", "15", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-movflags", "+faststart", sample)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate sample: %v %s", err, output)
	}
	video, err := os.ReadFile(sample)
	if err != nil {
		t.Fatal(err)
	}
	for index, title := range []string{"雾中的山谷", "城市漫游", "海岸线", "午后时光", "远山来信", "沿途风景", "冬日花园", "夜航"} {
		name := fmt.Sprintf("TEST-%03d", index+1)
		if err := os.WriteFile(filepath.Join(root, name+".mp4"), video, 0o600); err != nil {
			t.Fatal(err)
		}
		actor := "Test Actor"
		if index == 7 {
			actor = "Test Actor Extra"
		}
		catalogWrite(t, root, name+".nfo", fmt.Sprintf(`<movie><num>%s</num><title>%s</title><year>2026</year><plot>用于自动化验收的中性测试影片。验证资料、文件、播放与个人记录的完整链路。</plot><actor><name>%s</name></actor><genre>测试样本</genre></movie>`, name, title, actor))
		poster := image.NewRGBA(image.Rect(0, 0, 200, 300))
		for y := 0; y < 300; y++ {
			for x := 0; x < 200; x++ {
				poster.Set(x, y, color.RGBA{uint8(20 + index*20), uint8(65 + y/3), uint8(95 + x/2), 255})
			}
		}
		file, err := os.Create(filepath.Join(root, name+"-poster.png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, poster); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	catalogWrite(t, root, "TEST-001.zh.srt", "1\n00:00:01,000 --> 00:00:04,000\n字幕测试\n")
	if err := os.WriteFile(filepath.Join(root, "TEST-001-C.mp4"), video, 0o600); err != nil {
		t.Fatal(err)
	}
	catalogWrite(t, root, "_646-C.mp4", "unconfirmed sample")
	job := enqueueQueueTestJob(t, service.db, "browser-cleanup")
	if _, err := service.db.db.Exec(`UPDATE media_jobs SET status='failed',updated_at='2020-01-01T00:00:00Z' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	handler := service.Handler()
	stop := make(chan struct{})
	var stopOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'self'; media-src 'self' blob:; worker-src 'self' blob:")
		if os.Getenv("CATALOG_REMOTE_FIXTURE") == "1" && r.Method == "POST" && r.URL.Path == "/__fixture/stop" {
			stopOnce.Do(func() { close(stop) })
			w.WriteHeader(200)
			return
		}
		r.Header.Set("X-Trim-Isadmin", "true")
		r.Header.Set("X-Trim-Userid", "1000")
		r.Host = "localhost" // native gateway Host rewrite, browser Origin stays intact
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	if os.Getenv("CATALOG_REMOTE_FIXTURE") == "1" {
		fmt.Printf("FIXTURE_URL=%s%s/\nFIXTURE_ROOT=%s\n", server.URL, service.prefix, root)
		select {
		case <-stop:
		case <-ctx.Done():
			t.Fatal("fixture server deadline reached")
		}
		if _, err := os.Stat(filepath.Join(root, "TEST-002.mp4")); !os.IsNotExist(err) {
			t.Fatal("selected synthetic deletion not applied")
		}
		if _, err := os.Stat(filepath.Join(root, "TEST-001.mp4")); err != nil {
			t.Fatal("unselected synthetic media changed", err)
		}
		return
	}
	node := os.Getenv("CATALOG_NODE")
	if node == "" {
		node = "node"
	}
	command = exec.CommandContext(ctx, node, "../../scripts/catalog-browser.mjs", server.URL+service.prefix+"/", root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("browser journey: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
