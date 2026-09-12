package engine

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/yuukiy/javsp-go/internal/model"
	"github.com/yuukiy/javsp-go/internal/scanner"
)

// DiscoveredMovie is a scanner grouping, not a scraped metadata result. Files
// preserves the scanner's multipart ordering and format-selection rules.
type DiscoveredMovie struct {
	MediaID string
	Files   []string
}

type UnrecognizedInput struct {
	Path   string
	Reason string
}

type DiscoveryResult struct {
	Movies       []DiscoveredMovie
	Unrecognized []UnrecognizedInput
}

// Discover reads a configured input directory without constructing network
// clients, scraping metadata, writing artifacts, or moving files. An empty
// directory is a successful no-op; unsupported inputs remain visible. Returned
// file paths use the canonical input root for both recursive and explicit scans.
func Discover(ctx context.Context, options Options) (DiscoveryResult, error) {
	result := DiscoveryResult{Movies: []DiscoveredMovie{}, Unrecognized: []UnrecognizedInput{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	_, cfg, err := loadEmbeddedConfig(options)
	if err != nil {
		return result, fmt.Errorf("load discovery config: %w", err)
	}
	root, err := buildScanRoot(options.InputDirectory, cfg.Scanner.InputDirectory)
	if err != nil {
		return result, err
	}
	opts, err := buildScannerOptions(cfg)
	if err != nil {
		return result, fmt.Errorf("build discovery options: %w", err)
	}
	opts.OnUnrecognized = func(file scanner.UnrecognizedFile) {
		result.Unrecognized = append(result.Unrecognized, UnrecognizedInput{Path: file.Path, Reason: file.Reason})
	}
	// Explicit selections are canonicalized by their boundary checks. Preserve
	// both spellings so root aliases cannot bypass these exact exclusions.
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return result, err
	}
	canonicalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return result, err
	}
	for _, excluded := range options.ExcludedDirectories {
		if !filepath.IsAbs(excluded) {
			return result, fmt.Errorf("excluded directory must be absolute")
		}
		rel, err := filepath.Rel(absRoot, excluded)
		if err != nil || !filepath.IsLocal(rel) {
			rel, err = filepath.Rel(canonicalRoot, excluded)
		}
		if err != nil || !filepath.IsLocal(rel) {
			return result, fmt.Errorf("excluded directory is outside the input root")
		}
		opts.ExcludedDirectories = append(opts.ExcludedDirectories, filepath.Join(absRoot, rel), filepath.Join(canonicalRoot, rel))
	}
	var movies []model.Movie
	if options.InputFiles == nil {
		movies, err = scanner.ScanContext(ctx, canonicalRoot, opts)
	} else {
		var files []string
		files, err = resolveSelectedFiles(ctx, root, options.InputFiles)
		if err == nil {
			movies, err = scanner.ScanFilesContext(ctx, files, opts)
		}
	}
	if err != nil {
		return DiscoveryResult{}, fmt.Errorf("discover movies: %w", err)
	}
	for _, movie := range movies {
		result.Movies = append(result.Movies, DiscoveredMovie{MediaID: movieName(&movie), Files: append([]string(nil), movie.Files...)})
	}
	return result, nil
}
