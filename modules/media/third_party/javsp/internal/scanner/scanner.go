package scanner

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yuukiy/javsp-go/internal/avid"
	"github.com/yuukiy/javsp-go/internal/model"
)

type Options struct {
	FilenameExtensions       []string
	MinimumSize              int64
	IgnoredIDPattern         []string
	IgnoredFolderNamePattern []string
	SkipNFODir               bool
	OnUnrecognized           func(UnrecognizedFile)
	ExcludedDirectories      []string
}

// UnrecognizedFile describes eligible media that cannot be assigned a supported
// identifier. Reporting it never changes the file or fabricates an identifier.
type UnrecognizedFile struct {
	Path   string
	Reason string
}

func Scan(root string, opts Options) ([]model.Movie, error) {
	return ScanContext(context.Background(), root, opts)
}

func ScanContext(ctx context.Context, root string, opts Options) ([]model.Movie, error) {
	return scanRootsContext(ctx, []string{root}, opts)
}

// ScanFilesContext identifies only the named regular files. Directory skip
// rules apply to recursive discovery, not an explicit file selection. File
// extension, minimum size and identifier filters still apply.
func ScanFilesContext(ctx context.Context, files []string, opts Options) ([]model.Movie, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("explicit input files are empty")
	}
	unique := make([]string, 0, len(files))
	seen := make(map[string]bool)
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if excludedScanPath(file, opts.ExcludedDirectories) {
			return nil, fmt.Errorf("explicit input is inside an excluded directory: %s", file)
		}
		info, err := os.Lstat(file)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("explicit input must be a regular file: %s", file)
		}
		if !seen[file] {
			seen[file] = true
			unique = append(unique, file)
		}
	}
	return scanRootsContext(ctx, unique, opts)
}

func scanRootsContext(ctx context.Context, roots []string, opts Options) ([]model.Movie, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	extensions := make(map[string]bool)
	for _, ext := range opts.FilenameExtensions {
		extensions[strings.ToLower(ext)] = true
	}
	ignoredFolders, err := compilePatterns(opts.IgnoredFolderNamePattern)
	if err != nil {
		return nil, err
	}

	byID := make(map[string][]string)
	var idOrder []string

	for _, root := range roots {
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if excludedScanPath(path, opts.ExcludedDirectories) {
				if entry != nil && entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if path == root {
					return nil
				}
				name := entry.Name()
				if matchesAny(ignoredFolders, name) {
					return filepath.SkipDir
				}
				if opts.SkipNFODir && dirHasNFO(path) {
					return filepath.SkipDir
				}
				return nil
			}

			ext := strings.ToLower(filepath.Ext(path))
			if !extensions[ext] {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}

			if info.Size() < opts.MinimumSize {
				return nil
			}

			id := fileID(path, opts.IgnoredIDPattern)
			if id == "" {
				if opts.OnUnrecognized != nil {
					opts.OnUnrecognized(UnrecognizedFile{Path: path, Reason: "no_supported_identifier"})
				}
				return nil
			}
			if _, ok := byID[id]; !ok {
				idOrder = append(idOrder, id)
			}
			byID[id] = append(byID[id], path)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	for id, files := range byID {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(files) == 1 {
			continue
		}
		if mapped, ok := mapSlices(files); ok {
			byID[id] = mapped
			continue
		}
		selected, err := largestPreferredMP4(files)
		if err != nil {
			return nil, err
		}
		byID[id] = []string{selected}
	}

	movies := make([]model.Movie, 0, len(byID))
	for _, id := range idOrder {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		files, ok := byID[id]
		if !ok {
			continue
		}
		src := avid.GuessAVType(id)
		movie := model.Movie{
			Files:      files,
			DataSource: src,
		}
		if src == model.DataSourceCID {
			movie.CID = id
			movie.DVDID = avid.GetID(files[0], opts.IgnoredIDPattern)
		} else {
			movie.DVDID = id
		}
		movies = append(movies, movie)
	}
	return movies, nil
}

func excludedScanPath(path string, directories []string) bool {
	for _, directory := range directories {
		rel, err := filepath.Rel(directory, path)
		if err == nil && filepath.IsLocal(rel) {
			return true
		}
	}
	return false
}

func largestPreferredMP4(files []string) (string, error) {
	candidates := files
	mp4Files := make([]string, 0, len(files))
	for _, path := range files {
		if strings.EqualFold(filepath.Ext(path), ".mp4") {
			mp4Files = append(mp4Files, path)
		}
	}
	if len(mp4Files) > 0 {
		candidates = mp4Files
	}

	selected := ""
	var selectedSize int64 = -1
	for _, path := range candidates {
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if info.Size() > selectedSize || (info.Size() == selectedSize && (selected == "" || path < selected)) {
			selected = path
			selectedSize = info.Size()
		}
	}
	return selected, nil
}

func compilePatterns(patterns []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		out = append(out, compiled)
	}
	return out, nil
}

func matchesAny(patterns []*regexp.Regexp, s string) bool {
	for _, pattern := range patterns {
		if match := pattern.FindStringIndex(s); match != nil && match[0] == 0 {
			return true
		}
	}
	return false
}

func dirHasNFO(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".nfo") {
			return true
		}
	}
	return false
}

func fileID(path string, ignoredPatterns []string) string {
	dvdid := avid.GetID(path, ignoredPatterns)
	cid := avid.GetCID(path)
	if cid != "" {
		return cid
	}
	return dvdid
}

func mapSlices(files []string) ([]string, bool) {
	dirs := make(map[string]bool)
	basenames := make([]string, 0, len(files))
	for _, file := range files {
		dirs[filepath.Dir(file)] = true
		basenames = append(basenames, filepath.Base(file))
	}
	if len(dirs) > 1 {
		return nil, false
	}

	prefix := commonPrefix(basenames)
	pattern, err := regexp.Compile(`(?i)` + regexp.QuoteMeta(prefix) + `\s*([a-z\d])\s*`)
	if err != nil {
		return nil, false
	}

	slices := make([]byte, 0, len(files))
	postfixes := make(map[string]bool)
	sliceToFile := make(map[byte]string)
	for idx, basename := range basenames {
		remaining := strings.ToLower(pattern.ReplaceAllString(basename, "$1"))
		if remaining == "" {
			return nil, false
		}
		slice := remaining[0]
		postfix := remaining[1:]
		postfixes[postfix] = true
		if _, exists := sliceToFile[slice]; exists {
			return nil, false
		}
		slices = append(slices, slice)
		sliceToFile[slice] = files[idx]
	}
	if len(postfixes) != 1 {
		return nil, false
	}

	sort.Slice(slices, func(i, j int) bool { return slices[i] < slices[j] })
	first := slices[0]
	last := slices[len(slices)-1]
	if first != '0' && first != '1' && first != 'a' {
		return nil, false
	}
	if int(last) != int(first)+len(slices)-1 {
		return nil, false
	}

	mapped := make([]string, 0, len(files))
	for _, slice := range slices {
		mapped = append(mapped, sliceToFile[slice])
	}
	return mapped, true
}

func commonPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	prefix := values[0]
	for _, value := range values[1:] {
		for !strings.HasPrefix(value, prefix) {
			if prefix == "" {
				return ""
			}
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}
