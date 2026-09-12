package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yuukiy/javsp-go/internal/model"
	"github.com/yuukiy/javsp-go/internal/organizer"
)

// NamingSample is already-known metadata, not a request to scrape or translate.
// SourceFiles may be hypothetical absolute paths under the configured input root.
type NamingSample struct {
	MediaID     string   `json:"media_id"`
	Title       string   `json:"title"`
	Actress     []string `json:"actress"`
	PublishDate string   `json:"publish_date"`
	SourceFiles []string `json:"source_files"`
}

type NamingPreview struct {
	Plan     MoviePlan `json:"plan"`
	NFOTitle string    `json:"nfo_title"`
}

// PreviewNames calls the same naming and plan builders used by execution. It
// reads configuration only: no scanner, network clients, assets or file writes.
func PreviewNames(ctx context.Context, options Options, sample NamingSample) (NamingPreview, error) {
	if err := ctx.Err(); err != nil {
		return NamingPreview{}, err
	}
	if strings.TrimSpace(sample.MediaID) == "" || len(sample.SourceFiles) == 0 {
		return NamingPreview{}, fmt.Errorf("preview requires a media ID and source files")
	}
	_, cfg, err := loadEmbeddedConfig(options)
	if err != nil {
		return NamingPreview{}, err
	}
	root, err := buildScanRoot(options.InputDirectory, cfg.Scanner.InputDirectory)
	if err != nil {
		return NamingPreview{}, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return NamingPreview{}, err
	}
	for _, path := range sample.SourceFiles {
		rel, err := filepath.Rel(root, path)
		if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) || err != nil || !filepath.IsLocal(rel) || rel == "." {
			return NamingPreview{}, fmt.Errorf("preview source is outside input root")
		}
	}
	cfg.Summarizer.Path.OutputFolderPattern, err = resolveOutputFolderPattern(root, cfg.Summarizer.Path.OutputFolderPattern)
	if err != nil {
		return NamingPreview{}, err
	}
	movie := &model.Movie{DVDID: sample.MediaID, Files: append([]string(nil), sample.SourceFiles...), Info: &model.MovieInfo{DVDID: sample.MediaID, Title: sample.Title, Actress: append([]string(nil), sample.Actress...), PublishDate: sample.PublishDate}}
	if err := organizer.GenerateNames(movie, buildOrganizerOptions(cfg)); err != nil {
		return NamingPreview{}, err
	}
	if err := ctx.Err(); err != nil {
		return NamingPreview{}, err
	}
	return NamingPreview{Plan: buildMoviePlan(movie, cfg), NFOTitle: movie.Info.NFOTitle}, nil
}
