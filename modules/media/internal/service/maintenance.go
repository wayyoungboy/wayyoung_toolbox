package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxMaintenanceEntries = 200000
	fingerprintChunkSize  = 64 * 1024
)

type DuplicateVideoFile struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	Fingerprint string `json:"fingerprint,omitempty"`
	snapshot    os.FileInfo
}

type DuplicateVideoGroup struct {
	MediaID   string               `json:"media_id"`
	MatchType string               `json:"match_type"`
	SameSize  bool                 `json:"same_size"`
	Files     []DuplicateVideoFile `json:"files"`
}

type DuplicateScanResult struct {
	Directory     string                `json:"directory"`
	ScannedVideos int                   `json:"scanned_videos"`
	Groups        []DuplicateVideoGroup `json:"groups"`
}

type DuplicateDeleteSelection struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type DuplicateDeleteResult struct {
	Selected       int      `json:"selected"`
	Deleted        int      `json:"deleted"`
	ReclaimedBytes int64    `json:"reclaimed_bytes"`
	Files          []string `json:"files"`
}

func deleteDuplicateVideos(root string, selections []DuplicateDeleteSelection) (DuplicateDeleteResult, error) {
	if len(selections) == 0 {
		return DuplicateDeleteResult{}, fmt.Errorf("请至少选择一个重复视频")
	}
	if len(selections) > 500 {
		return DuplicateDeleteResult{}, fmt.Errorf("单次最多删除 500 个重复视频")
	}
	canonicalRoot, err := canonicalDirectory(root)
	if err != nil {
		return DuplicateDeleteResult{}, fmt.Errorf("媒体维护目录不可访问")
	}
	scan, err := scanDuplicateVideos(canonicalRoot)
	if err != nil {
		return DuplicateDeleteResult{}, err
	}
	type member struct {
		group int
		file  DuplicateVideoFile
	}
	members := make(map[string]member)
	for groupIndex, group := range scan.Groups {
		for _, file := range group.Files {
			members[filepath.Clean(file.Path)] = member{group: groupIndex, file: file}
		}
	}
	selectedByGroup := make(map[int]int)
	validated := make([]DuplicateVideoFile, 0, len(selections))
	seen := make(map[string]bool)
	for _, selection := range selections {
		path := filepath.Clean(strings.TrimSpace(selection.Path))
		if path == "." || !filepath.IsAbs(path) || hasParentSegment(selection.Path) || seen[path] || !pathWithinRoot(canonicalRoot, path) {
			return DuplicateDeleteResult{}, fmt.Errorf("待删除路径无效或不在媒体维护目录内")
		}
		seen[path] = true
		item, ok := members[path]
		if !ok {
			return DuplicateDeleteResult{}, fmt.Errorf("%s 已不是当前重复扫描结果", filepath.Base(path))
		}
		if item.file.Size != selection.Size || item.file.Fingerprint != selection.Fingerprint {
			return DuplicateDeleteResult{}, fmt.Errorf("%s 扫描后已发生变化，请重新扫描", filepath.Base(path))
		}
		selectedByGroup[item.group]++
		validated = append(validated, item.file)
	}
	for groupIndex, selected := range selectedByGroup {
		if selected >= len(scan.Groups[groupIndex].Files) {
			return DuplicateDeleteResult{}, fmt.Errorf("每个重复组必须至少保留一个文件")
		}
	}

	result := DuplicateDeleteResult{Selected: len(validated), Files: make([]string, 0, len(validated))}
	for _, file := range validated {
		info, statErr := os.Lstat(file.Path)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != file.Size {
			return result, fmt.Errorf("%s 删除前校验失败", filepath.Base(file.Path))
		}
		if file.Fingerprint != "" {
			fingerprint, fingerprintErr := sampledVideoFingerprint(file.Path, info.Size())
			if fingerprintErr != nil || fingerprint != file.Fingerprint {
				return result, fmt.Errorf("%s 删除前内容校验失败", filepath.Base(file.Path))
			}
		}
		if removeErr := os.Remove(file.Path); removeErr != nil {
			return result, fmt.Errorf("删除 %s：%w", filepath.Base(file.Path), removeErr)
		}
		result.Deleted++
		result.ReclaimedBytes += file.Size
		result.Files = append(result.Files, file.Path)
	}
	return result, nil
}

func scanDuplicateVideos(root string) (DuplicateScanResult, error) {
	canonicalRoot, err := canonicalDirectory(root)
	if err != nil {
		return DuplicateScanResult{}, fmt.Errorf("重复视频扫描目录不可访问")
	}
	result := DuplicateScanResult{Directory: canonicalRoot, Groups: []DuplicateVideoGroup{}}
	candidates := map[string][]DuplicateVideoFile{}
	visited := 0
	err = filepath.WalkDir(canonicalRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > maxMaintenanceEntries {
			return fmt.Errorf("媒体目录超过 %d 个条目", maxMaintenanceEntries)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if path != canonicalRoot && protectedMaintenanceDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isMaintenanceVideo(path) {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		result.ScannedVideos++
		mediaID, ok := mediaIDFromVideoFilename(entry.Name())
		if !ok {
			return nil
		}
		candidates[mediaID] = append(candidates[mediaID], DuplicateVideoFile{Path: path, Name: entry.Name(), Size: info.Size(), snapshot: info})
		return nil
	})
	if err != nil {
		return DuplicateScanResult{}, err
	}
	mediaIDs := make([]string, 0, len(candidates))
	for mediaID, files := range candidates {
		if len(files) > 1 {
			mediaIDs = append(mediaIDs, mediaID)
		}
	}
	sort.Strings(mediaIDs)
	for _, mediaID := range mediaIDs {
		files := candidates[mediaID]
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		sameSize := true
		for index := 1; index < len(files); index++ {
			if files[index].Size != files[0].Size {
				sameSize = false
				break
			}
		}
		matchType := "media_id"
		if sameSize {
			fingerprintsMatch := true
			for index := range files {
				fingerprint, fingerprintErr := sampledVideoFingerprint(files[index].Path, files[index].Size)
				if fingerprintErr != nil {
					return DuplicateScanResult{}, fmt.Errorf("计算 %s 内容指纹：%w", files[index].Name, fingerprintErr)
				}
				files[index].Fingerprint = fingerprint
				if index > 0 && fingerprint != files[0].Fingerprint {
					fingerprintsMatch = false
				}
			}
			if fingerprintsMatch {
				matchType = "fingerprint"
			}
		}
		result.Groups = append(result.Groups, DuplicateVideoGroup{MediaID: mediaID, MatchType: matchType, SameSize: sameSize, Files: files})
	}
	return result, nil
}

func sampledVideoFingerprint(path string, size int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	_, _ = io.WriteString(hash, strconv.FormatInt(size, 10))
	offsets := []int64{0}
	if size > fingerprintChunkSize {
		offsets = append(offsets, max(0, size/2-fingerprintChunkSize/2), max(0, size-fingerprintChunkSize))
	}
	seen := map[int64]bool{}
	for _, offset := range offsets {
		if seen[offset] {
			continue
		}
		seen[offset] = true
		if _, err := io.CopyN(hash, io.NewSectionReader(file, offset, min(int64(fingerprintChunkSize), size-offset)), min(int64(fingerprintChunkSize), size-offset)); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func isMaintenanceVideo(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".3gp", ".avi", ".f4v", ".flv", ".iso", ".m2ts", ".m4v", ".mkv", ".mov", ".mp4", ".mpeg", ".mpg", ".rm", ".rmvb", ".strm", ".ts", ".vob", ".webm", ".wmv":
		return true
	default:
		return false
	}
}

func protectedMaintenanceDirectory(name string) bool {
	switch strings.ToLower(name) {
	case ".snapshot", "#recycle", "@eadir", "lost+found":
		return true
	default:
		return false
	}
}

type EmptyDirectoryResult struct {
	Directory   string   `json:"directory"`
	ScannedDirs int      `json:"scanned_dirs"`
	Found       int      `json:"found"`
	Removed     int      `json:"removed"`
	Failed      int      `json:"failed"`
	Directories []string `json:"directories"`
	Errors      []string `json:"errors"`
}

type maintenanceDirectory struct {
	path string
	info os.FileInfo
}

func inspectEmptyDirectories(root string, minimumAge time.Duration, remove bool) (EmptyDirectoryResult, error) {
	canonicalRoot, err := canonicalDirectory(root)
	if err != nil {
		return EmptyDirectoryResult{}, fmt.Errorf("空目录检查路径不可访问")
	}
	if minimumAge < time.Hour {
		return EmptyDirectoryResult{}, fmt.Errorf("空目录最短保留时间不能少于 1 小时")
	}
	result := EmptyDirectoryResult{Directory: canonicalRoot, Directories: []string{}, Errors: []string{}}
	directories := make([]maintenanceDirectory, 0, 1024)
	rootInfo, err := os.Lstat(canonicalRoot)
	if err != nil {
		return EmptyDirectoryResult{}, err
	}
	knownDirectories := map[string]os.FileInfo{canonicalRoot: rootInfo}
	visited := 0
	err = filepath.WalkDir(canonicalRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > maxMaintenanceEntries {
			return fmt.Errorf("媒体目录超过 %d 个条目", maxMaintenanceEntries)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if path == canonicalRoot {
			return nil
		}
		if protectedMaintenanceDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		directories = append(directories, maintenanceDirectory{path: path, info: info})
		knownDirectories[path] = info
		return nil
	})
	if err != nil {
		return EmptyDirectoryResult{}, err
	}
	result.ScannedDirs = len(directories)
	sort.Slice(directories, func(i, j int) bool {
		leftDepth := strings.Count(directories[i].path, string(filepath.Separator))
		rightDepth := strings.Count(directories[j].path, string(filepath.Separator))
		if leftDepth == rightDepth {
			return directories[i].path > directories[j].path
		}
		return leftDepth > rightDepth
	})
	planned := map[string]bool{}
	selected := make([]maintenanceDirectory, 0)
	now := time.Now()
	for _, directory := range directories {
		if now.Sub(directory.info.ModTime()) < minimumAge {
			continue
		}
		entries, readErr := os.ReadDir(directory.path)
		if readErr != nil {
			result.Failed++
			result.Errors = append(result.Errors, directory.path+"："+readErr.Error())
			continue
		}
		emptyAfterPlannedCleanup := true
		for _, entry := range entries {
			child := filepath.Join(directory.path, entry.Name())
			if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && planned[child] {
				continue
			}
			emptyAfterPlannedCleanup = false
			break
		}
		if !emptyAfterPlannedCleanup {
			continue
		}
		planned[directory.path] = true
		selected = append(selected, directory)
		result.Directories = append(result.Directories, directory.path)
	}
	result.Found = len(result.Directories)
	if !remove {
		return result, nil
	}
	for _, directory := range selected {
		parent := filepath.Dir(directory.path)
		removed, parentInfo, removeErr := safeRemoveEmptyDirectory(
			canonicalRoot,
			directory.path,
			knownDirectories[directory.path],
			knownDirectories[parent],
		)
		if removeErr != nil {
			result.Failed++
			result.Errors = append(result.Errors, directory.path+"："+removeErr.Error())
			continue
		}
		if removed {
			result.Removed++
			knownDirectories[parent] = parentInfo
		}
	}
	return result, nil
}
