package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yuukiy/javsp-go/internal/config"
	"github.com/yuukiy/javsp-go/internal/model"
	"github.com/yuukiy/javsp-go/internal/organizer"
	"gopkg.in/yaml.v3"
)

func TestPreviewNamesMatchesExecutionWithoutFileAccess(t *testing.T) {
	for _, mode := range []string{"relative", "absolute", "in_place", "multipart", "long_title"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			cfg, err := config.Load(filepath.Join("..", "config.yml"))
			if err != nil {
				t.Fatal(err)
			}
			cfg.Summarizer.MoveFiles = mode != "in_place"
			cfg.Summarizer.Path.OutputFolderPattern = "completed/{year}/{num} {title}"
			cfg.Summarizer.Path.BasenamePattern = "{num}"
			cfg.Summarizer.NFO.BasenamePattern = "{num}"
			cfg.Summarizer.NFO.TitlePattern = "{num} {title}"
			cfg.Summarizer.Path.LengthMaximum = 4096
			if mode == "absolute" {
				cfg.Summarizer.Path.OutputFolderPattern = filepath.Join(root, "separate", "{num}")
			}
			sample := NamingSample{MediaID: "TEST-001", Title: "示例标题", Actress: []string{"示例演员"}, PublishDate: "2026-01-02", SourceFiles: []string{filepath.Join(root, "TEST-001-C.mp4")}}
			if mode == "multipart" {
				sample.SourceFiles = []string{filepath.Join(root, "TEST-001-CD1.mp4"), filepath.Join(root, "TEST-001-CD2.mkv")}
			}
			if mode == "long_title" {
				sample.Title = strings.Repeat("示例", 100)
				cfg.Summarizer.Path.LengthMaximum = len(root) + 80
			}
			raw, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			got, err := PreviewNames(context.Background(), Options{ConfigData: raw, ResourceRoot: "..", InputDirectory: root}, sample)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Summarizer.Path.OutputFolderPattern, err = resolveOutputFolderPattern(root, cfg.Summarizer.Path.OutputFolderPattern)
			if err != nil {
				t.Fatal(err)
			}
			movie := &model.Movie{DVDID: sample.MediaID, Files: sample.SourceFiles, Info: &model.MovieInfo{DVDID: sample.MediaID, Title: sample.Title, Actress: sample.Actress, PublishDate: sample.PublishDate}}
			if err := organizer.GenerateNames(movie, buildOrganizerOptions(cfg)); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Plan, buildMoviePlan(movie, cfg)) || got.NFOTitle != movie.Info.NFOTitle {
				t.Fatalf("preview differs from execution: %+v", got)
			}
			if mode == "relative" && filepath.Base(got.Plan.Files[0].Destination) != "TEST-001-C.mp4" {
				t.Fatal("preview lost filename attributes")
			}
			if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
				t.Fatalf("preview accessed or created media artifacts: %v %v", entries, err)
			}
		})
	}
}

func TestPreviewNamesRejectsCanceledOrInvalidInputs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PreviewNames(ctx, Options{}, NamingSample{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{ConfigData: raw, ResourceRoot: "..", InputDirectory: t.TempDir()}
	for _, sample := range []NamingSample{{}, {MediaID: "TEST-001"}, {MediaID: "TEST-001", SourceFiles: []string{"/outside.mp4"}}} {
		if _, err := PreviewNames(context.Background(), opts, sample); err == nil {
			t.Fatal("invalid sample accepted")
		}
	}
}
