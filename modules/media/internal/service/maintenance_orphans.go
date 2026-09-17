package service

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type OrphanFolder struct {
	Path       string   `json:"path"`
	Files      []string `json:"files"`
	Bytes      int64    `json:"bytes"`
	SkipReason string   `json:"skip_reason,omitempty"`
	nodes      map[string]os.FileInfo
}

type OrphanCleanupResult struct {
	Directory      string         `json:"directory"`
	ScannedDirs    int            `json:"scanned_dirs"`
	Candidates     []OrphanFolder `json:"candidates"`
	Found          int            `json:"found"`
	Removed        int            `json:"removed"`
	DeletedFiles   int            `json:"deleted_files"`
	ReclaimedBytes int64          `json:"reclaimed_bytes"`
	Directories    []string       `json:"directories"`
	Errors         []string       `json:"errors"`
}

type orphanCleanupPlan struct {
	OrphanCleanupResult
	rootInfo os.FileInfo
}

func isMovieNFO(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4*1024*1024 {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	var movie struct{ XMLName xml.Name }
	return xml.NewDecoder(io.LimitReader(file, 4*1024*1024)).Decode(&movie) == nil && movie.XMLName.Local == "movie"
}

func isOrphanSidecar(name string) bool {
	switch strings.ToLower(filepath.Base(name)) {
	case ".ds_store", "thumbs.db":
		return true
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".nfo", ".jpg", ".jpeg", ".png", ".webp", ".bmp", ".gif", ".tbn", ".avif", ".jfif", ".srt", ".ass", ".ssa", ".vtt", ".sub", ".idx":
		return true
	}
	return false
}

func standaloneAttachmentDirectory(name string) bool {
	switch strings.ToLower(name) {
	case ".actors", "extrafanart", "extrathumbs", "subs", "subtitles":
		return true
	}
	return false
}

func planOrphanFolders(root string, minimumAge time.Duration) (orphanCleanupPlan, error) {
	canonical, err := canonicalDirectory(root)
	if err != nil {
		return orphanCleanupPlan{}, fmt.Errorf("残留附件检查目录不可访问")
	}
	if minimumAge < time.Hour {
		return orphanCleanupPlan{}, fmt.Errorf("最短保留时间不能少于 1 小时")
	}
	info, err := os.Lstat(canonical)
	if err != nil {
		return orphanCleanupPlan{}, err
	}
	plan := orphanCleanupPlan{OrphanCleanupResult: OrphanCleanupResult{
		Directory: canonical, Candidates: []OrphanFolder{}, Directories: []string{}, Errors: []string{},
	}, rootInfo: info}
	visited := 0
	err = filepath.WalkDir(canonical, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > maxMaintenanceEntries {
			return fmt.Errorf("媒体目录超过 %d 个条目", maxMaintenanceEntries)
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if path == canonical {
			return nil
		}
		if protectedMaintenanceDirectory(entry.Name()) || standaloneAttachmentDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		plan.ScannedDirs++
		children, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		movieFolder := false
		for _, child := range children {
			if strings.EqualFold(filepath.Ext(child.Name()), ".nfo") && isMovieNFO(filepath.Join(path, child.Name())) {
				movieFolder = true
				break
			}
		}
		if !movieFolder {
			return nil
		}
		folder, hasVideo, err := inspectOrphanFolder(path, minimumAge, &visited)
		if err != nil {
			return err
		}
		if !hasVideo {
			plan.Candidates = append(plan.Candidates, folder)
			if folder.SkipReason == "" {
				plan.Found++
			}
		}
		// Never independently remove .actors/artwork or nested movie folders
		// under a recognized movie directory.
		return filepath.SkipDir
	})
	return plan, err
}

func inspectOrphanFolder(path string, minimumAge time.Duration, visited *int) (OrphanFolder, bool, error) {
	folder := OrphanFolder{Path: path, Files: []string{}, nodes: map[string]os.FileInfo{}}
	hasVideo := false
	cutoff := time.Now().Add(-minimumAge)
	err := filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		*visited++
		if *visited > maxMaintenanceEntries {
			return fmt.Errorf("媒体目录超过 %d 个条目", maxMaintenanceEntries)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		folder.nodes[current] = info
		if info.ModTime().After(cutoff) {
			folder.SkipReason = "目录或附件近期修改，暂不清理"
		}
		if entry.Type()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			folder.SkipReason = "包含符号链接或特殊文件"
			return nil
		}
		if info.IsDir() {
			if current != path && protectedMaintenanceDirectory(entry.Name()) {
				folder.SkipReason = "包含受保护目录"
				return filepath.SkipDir
			}
			return nil
		}
		if isMaintenanceVideo(current) {
			hasVideo = true
			return nil
		}
		if !isOrphanSidecar(entry.Name()) {
			folder.SkipReason = "包含非影片附件，需人工检查"
		}
		if filepath.Dir(current) != path && strings.EqualFold(filepath.Ext(current), ".nfo") {
			folder.SkipReason = "包含其他目录的 NFO，需人工检查"
		}
		folder.Files = append(folder.Files, current)
		folder.Bytes += info.Size()
		return nil
	})
	return folder, hasVideo, err
}

func verifyOrphanSnapshot(plan orphanCleanupPlan, folder OrphanFolder) error {
	rootInfo, err := os.Lstat(plan.Directory)
	if err != nil || !rootInfo.IsDir() || !os.SameFile(rootInfo, plan.rootInfo) {
		return fmt.Errorf("维护根目录已变化")
	}
	resolved, err := filepath.EvalSymlinks(folder.Path)
	if err != nil || resolved != folder.Path || folder.Path == plan.Directory || !pathWithinRoot(plan.Directory, resolved) {
		return fmt.Errorf("残留目录路径已变化")
	}
	count := 0
	err = filepath.WalkDir(folder.Path, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		count++
		expected, exists := folder.nodes[path]
		current, err := entry.Info()
		if !exists || err != nil || entry.Type()&os.ModeSymlink != 0 || !os.SameFile(expected, current) ||
			expected.Mode() != current.Mode() || expected.Size() != current.Size() || !expected.ModTime().Equal(current.ModTime()) {
			return fmt.Errorf("残留目录内容已变化，停止清理")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if count != len(folder.nodes) {
		return fmt.Errorf("残留目录条目已变化")
	}
	return nil
}

func executeOrphanFolders(plan orphanCleanupPlan) (OrphanCleanupResult, error) {
	result := plan.OrphanCleanupResult
	for _, folder := range plan.Candidates {
		if folder.SkipReason != "" {
			continue
		}
		if err := removeOrphanFolder(plan, folder, &result); err != nil {
			result.Errors = append(result.Errors, folder.Path+"："+err.Error())
		}
	}
	if len(result.Errors) > 0 {
		return result, errors.New(strings.Join(result.Errors, "\n"))
	}
	return result, nil
}

func removeOrphanFolder(plan orphanCleanupPlan, folder OrphanFolder, result *OrphanCleanupResult) error {
	// Only explicitly scanned attachments are unlinked; never recursively remove
	// a folder, as a newly-arrived movie must survive even during external writes.
	paths := make([]string, 0, len(folder.nodes))
	for path := range folder.nodes {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		left, right := strings.Count(paths[i], string(filepath.Separator)), strings.Count(paths[j], string(filepath.Separator))
		if left != right {
			return left > right
		}
		return paths[i] < paths[j]
	})
	for _, path := range paths {
		if err := verifyOrphanSnapshot(plan, folder); err != nil {
			return err
		}
		expected := folder.nodes[path]
		if !expected.IsDir() && !isOrphanSidecar(path) {
			return fmt.Errorf("拒绝删除非附件文件")
		}
		parent, err := os.OpenRoot(filepath.Dir(path))
		if err != nil {
			return err
		}
		bound, err := parent.Lstat(filepath.Base(path))
		if err != nil || !os.SameFile(expected, bound) || !expected.ModTime().Equal(bound.ModTime()) || expected.Size() != bound.Size() {
			parent.Close()
			return fmt.Errorf("删除目标已变化")
		}
		err = parent.Remove(filepath.Base(path)) // rmdir is non-recursive and rejects new files.
		parent.Close()
		if err != nil {
			return err
		}
		if !expected.IsDir() {
			result.DeletedFiles++
			result.ReclaimedBytes += expected.Size()
		}
		delete(folder.nodes, path)
		parentPath := filepath.Dir(path)
		if previous, ok := folder.nodes[parentPath]; ok {
			current, err := os.Lstat(parentPath)
			if err != nil || !os.SameFile(previous, current) {
				return fmt.Errorf("父目录已变化")
			}
			folder.nodes[parentPath] = current
		}
	}
	result.Removed++
	result.Directories = append(result.Directories, folder.Path)
	return nil
}
