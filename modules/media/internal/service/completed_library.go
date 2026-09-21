package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type completedLibraryItem struct {
	MediaID     string `json:"media_id"`
	FileCount   int    `json:"file_count"`
	ExamplePath string `json:"example_path"`
}

type completedLibraryReport struct {
	Scanned              bool                   `json:"scanned"`
	Root                 string                 `json:"root"`
	ScannedAt            string                 `json:"scanned_at"`
	VideoCount           int                    `json:"video_count"`
	UnrecognizedCount    int                    `json:"unrecognized_count"`
	UnrecognizedExamples []string               `json:"unrecognized_examples"`
	IDCount              int                    `json:"id_count"`
	Items                []completedLibraryItem `json:"items"`
	Total                int                    `json:"total"`
	Offset               int                    `json:"offset"`
	Limit                int                    `json:"limit"`
}

// This cache is for display only. Every subscription builds a fresh index.
type completedLibraryState struct {
	mu       sync.RWMutex
	scanLock sync.Mutex
	key      string
	report   completedLibraryReport
}

func completedLibraryKey(cfg JavspConfig) string {
	data, _ := json.Marshal([]any{cfg.InputDirectory, cfg.OutputDirectory, cfg.OutputFolderPattern, cfg.FilenameExtensions})
	return string(data)
}

func scanCompletedLibrary(ctx context.Context, cfg JavspConfig) (completedMediaIndex, error) {
	index := completedMediaIndex{ids: map[string]struct{}{}}
	if err := ctx.Err(); err != nil {
		return index, err
	}
	path, available, err := staticJavspOutputRoot(cfg)
	if err != nil || !available {
		return index, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return index, err
	}
	defer root.Close()
	index.report = completedLibraryReport{Root: path, Items: []completedLibraryItem{}, UnrecognizedExamples: []string{}}
	extensions := make(map[string]bool, len(cfg.FilenameExtensions))
	for _, ext := range cfg.FilenameExtensions {
		extensions[strings.ToLower(ext)] = true
	}
	items := map[string]completedLibraryItem{}
	visited := 0
	err = fs.WalkDir(root.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > maxCompletedMediaEntries {
			return fmt.Errorf("整理完成目录超过 %d 个条目，已停止核对", maxCompletedMediaEntries)
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || !extensions[strings.ToLower(filepath.Ext(entry.Name()))] {
			return nil
		}
		info, err := root.Lstat(relative)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() <= 0 {
			return nil
		}
		index.report.VideoCount++
		id, ok := unambiguousMediaID(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
		if !ok {
			index.report.UnrecognizedCount++
			if len(index.report.UnrecognizedExamples) < 20 {
				index.report.UnrecognizedExamples = append(index.report.UnrecognizedExamples, relative)
			}
			return nil
		}
		item := items[id]
		item.MediaID, item.FileCount = id, item.FileCount+1
		if item.ExamplePath == "" {
			item.ExamplePath = relative
		}
		items[id], index.ids[id] = item, struct{}{}
		return nil
	})
	if err != nil {
		return completedMediaIndex{}, err
	}
	for _, item := range items {
		index.report.Items = append(index.report.Items, item)
	}
	sort.Slice(index.report.Items, func(i, j int) bool { return index.report.Items[i].MediaID < index.report.Items[j].MediaID })
	index.available, index.report.Scanned, index.report.ScannedAt = true, true, nowUTC()
	index.report.IDCount = len(items)
	return index, ctx.Err()
}

func (s *Service) completedIndex(ctx context.Context, cfg JavspConfig) (completedMediaIndex, error) {
	// Serialize with manual refresh, so an older scan cannot replace a newer one.
	s.completedLibrary.scanLock.Lock()
	defer s.completedLibrary.scanLock.Unlock()
	index, err := scanCompletedLibrary(ctx, cfg)
	s.completedLibrary.mu.Lock()
	s.completedLibrary.key, s.completedLibrary.report = completedLibraryKey(cfg), index.report
	s.completedLibrary.mu.Unlock()
	return index, err
}

func (s *Service) handleCompletedLibrary(w http.ResponseWriter, r *http.Request) {
	limit, offset := 50, 0
	var err error
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
	}
	if err != nil || limit < 1 || limit > 200 {
		writeError(w, 400, fmt.Errorf("limit 必须在 1–200 之间"))
		return
	}
	if value := r.URL.Query().Get("offset"); value != "" {
		offset, err = strconv.Atoi(value)
	}
	if err != nil || offset < 0 {
		writeError(w, 400, fmt.Errorf("offset 无效"))
		return
	}
	cfg, err := s.db.JavspConfig()
	if err != nil {
		writeError(w, 500, err)
		return
	}
	root, available, err := staticJavspOutputRoot(cfg)
	if err != nil {
		writeError(w, 409, fmt.Errorf("整理完成目录不可用：%w", err))
		return
	}
	s.completedLibrary.mu.RLock()
	report, key := s.completedLibrary.report, s.completedLibrary.key
	s.completedLibrary.mu.RUnlock()
	if !available || key != completedLibraryKey(cfg) {
		report = completedLibraryReport{Root: root}
	}
	query := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("query")))
	items := make([]completedLibraryItem, 0)
	for _, item := range report.Items {
		if strings.Contains(item.MediaID, query) {
			items = append(items, item)
		}
	}
	report.Total, report.Offset, report.Limit = len(items), offset, limit
	start := min(offset, len(items))
	report.Items = items[start:min(start+limit, len(items))]
	writeJSON(w, 200, report)
}

func (s *Service) handleScanCompletedLibrary(w http.ResponseWriter, r *http.Request) {
	var payload struct{}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	if !s.completedLibrary.scanLock.TryLock() {
		writeError(w, 409, fmt.Errorf("番号识别正在运行，请稍后刷新结果"))
		return
	}
	defer s.completedLibrary.scanLock.Unlock()
	cfg, err := s.db.JavspConfig()
	if err != nil {
		writeError(w, 500, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	index, err := scanCompletedLibrary(ctx, cfg)
	s.completedLibrary.mu.Lock()
	s.completedLibrary.key, s.completedLibrary.report = completedLibraryKey(cfg), index.report
	s.completedLibrary.mu.Unlock()
	if err == nil && !index.Available() {
		err = fmt.Errorf("请先配置可读取的整理完成目录")
	}
	if err != nil {
		writeError(w, 409, err)
		return
	}
	s.handleCompletedLibrary(w, r)
}
