package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yuukiy/javsp-go/internal/model"
	"github.com/yuukiy/javsp-go/internal/scanner"
)

func scanEngineInput(ctx context.Context, root string, files []string, opts scanner.Options, output io.Writer) ([]model.Movie, error) {
	if files == nil {
		return scanMoviesWithDiagnostics(ctx, root, opts, output)
	}
	selected, err := resolveSelectedFiles(ctx, root, files)
	if err != nil {
		return nil, err
	}
	return scanWithDiagnostics(opts, output, func(options scanner.Options) ([]model.Movie, error) {
		return scanner.ScanFilesContext(ctx, selected, options)
	})
}

func resolveSelectedFiles(ctx context.Context, root string, files []string) ([]string, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("explicit input files are empty")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	canonicalRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return nil, err
	}
	directory, err := os.OpenRoot(canonicalRoot)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	selected := make([]string, 0, len(files))
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !filepath.IsAbs(file) || strings.ContainsRune(file, 0) {
			return nil, fmt.Errorf("explicit input must be an absolute file path")
		}
		for _, part := range strings.Split(file, string(filepath.Separator)) {
			if part == ".." {
				return nil, fmt.Errorf("explicit input cannot contain parent traversal")
			}
		}
		rel, err := filepath.Rel(absoluteRoot, file)
		if err != nil || !filepath.IsLocal(rel) {
			rel, err = filepath.Rel(canonicalRoot, file)
		}
		if err != nil || !filepath.IsLocal(rel) || rel == "." {
			return nil, fmt.Errorf("explicit input is outside the configured input directory")
		}
		current := ""
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			info, err := directory.Lstat(current)
			if err != nil {
				return nil, err
			}
			if info.Mode()&os.ModeSymlink != 0 || (current == rel && !info.Mode().IsRegular()) {
				return nil, fmt.Errorf("explicit input must not contain links or non-regular files: %s", file)
			}
		}
		selected = append(selected, filepath.Join(canonicalRoot, rel))
	}
	return selected, nil
}
