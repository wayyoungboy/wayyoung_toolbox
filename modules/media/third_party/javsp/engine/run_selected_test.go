package engine

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/yuukiy/javsp-go/internal/config"
	"gopkg.in/yaml.v3"
)

func TestRunSelectedMovieReturnsConcreteResultAndOriginalRootPlan(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "pending", "TEST-001.mp4")
	if err := os.MkdirAll(filepath.Dir(input), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{input, filepath.Join(root, "OTHER-002.mp4")} {
		if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var details, planned, moved atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, "fixture site available")
		case "/TEST-001":
			details.Add(1)
			fmt.Fprint(w, `<html><body><div class="container"><h3>TEST-001 Example title</h3><a class="bigImage"><img src="/cover.png"></a><div class="col-md-3 info"><p><span>識別碼:</span> TEST-001</p><p><span>發行日期:</span> 2024-01-02</p></div></div></body></html>`)
		case "/cover.png":
			w.Header().Set("Content-Type", "image/png")
			if err := png.Encode(w, image.NewRGBA(image.Rect(0, 0, 40, 20))); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected request beyond explicit movie: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()
	cfg, err := config.Load(filepath.Join("..", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Scanner.MinimumSize = "0B"
	cfg.Scanner.Manual = false
	cfg.Network.ProxyServer = nil
	cfg.Network.ProxyFree = map[string]string{"javbus": remote.URL, "javdb": remote.URL, "avsox": remote.URL, "javlib": remote.URL}
	cfg.Network.Retry = 1
	cfg.Crawler.Selection.Normal = []string{"javbus"}
	cfg.Crawler.RequiredKeys = []string{"title", "cover"}
	cfg.Crawler.MovieConcurrency = 1
	cfg.Crawler.SleepAfterScraping = "0s"
	cfg.Crawler.NormalizeActressName = false
	cfg.Translator.Engine = nil
	cfg.Other.Interactive = false
	cfg.Other.CheckUpdate = false
	cfg.Other.AutoUpdate = false
	cfg.Summarizer.MoveFiles = true
	cfg.Summarizer.Path.OutputFolderPattern = "output/{num}"
	cfg.Summarizer.Path.BasenamePattern = "{num}"
	cfg.Summarizer.Cover.AddLabel = false
	cfg.Summarizer.Cover.Crop.OnIDPattern = nil
	cfg.Summarizer.ExtraFanarts.Enabled = false
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "output", "TEST-001", "TEST-001.mp4")
	result, err := Run(context.Background(), Options{ConfigData: data, ResourceRoot: "..", InputDirectory: root, InputFiles: []string{input}, Hooks: ExecutionHooks{
		BeforeOrganize: func(ctx context.Context, plan MoviePlan) error {
			planned.Add(1)
			if len(plan.Files) != 1 || plan.Files[0].Destination != want {
				t.Errorf("relative naming anchored to wrong root: %+v", plan)
			}
			return nil
		},
		MoveFiles: func(ctx context.Context, plan MoviePlan) (MoveResult, error) {
			moved.Add(1)
			if err := os.Rename(plan.Files[0].Source, plan.Files[0].Destination); err != nil {
				return MoveResult{}, err
			}
			return MoveResult{OutputPaths: []string{plan.Files[0].Destination}}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || result.Processed != 1 || result.Failed != 0 || len(result.Movies) != 1 || result.Movies[0].Status != "success" || len(result.Movies[0].OutputPaths) != 1 || result.Movies[0].OutputPaths[0] != want {
		t.Fatalf("wrong result: %+v", result)
	}
	if details.Load() != 1 || planned.Load() != 1 || moved.Load() != 1 {
		t.Fatalf("wrong run counts: %d %d %d", details.Load(), planned.Load(), moved.Load())
	}
	if _, err := os.Stat(filepath.Join(root, "OTHER-002.mp4")); err != nil {
		t.Fatal(err)
	}
}
