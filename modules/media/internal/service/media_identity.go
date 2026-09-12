package service

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxCompletedMediaEntries = 200000

var (
	fc2TitlePattern      = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])fc2(?:[-_ ]?ppv)?[-_ ]+(\d{5,9})(?:[^a-z0-9]|$)`)
	mediaTitlePattern    = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])([a-z0-9]{2,16})[-_]+(\d{2,9})(?:[^a-z0-9]|$)`)
	fc2FilePattern       = regexp.MustCompile(`(?i)^fc2(?:[-_ ]?ppv)?[-_ ]+(\d{5,9})(?:[-_](?:uc|u|c))?(?:[-_ .](?:(?:cd|part|pt)[-_ ]?)?\d{1,3})?$`)
	mediaFilePattern     = regexp.MustCompile(`(?i)^([a-z0-9]{2,16})[-_]+(\d{2,9})(?:[-_](?:uc|u|c))?(?:[-_ .](?:(?:cd|part|pt)[-_ ]?)?\d{1,3})?$`)
	ignoredMediaPrefixes = map[string]bool{
		"AAC": true, "AVC": true, "FHD": true, "H264": true, "H265": true,
		"HD": true, "HEVC": true, "UHD": true, "X264": true, "X265": true,
	}
)

type completedMediaIndex struct {
	ids       map[string]struct{}
	available bool
}

func normalizedMediaID(prefix, number string) (string, bool) {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	number = strings.TrimSpace(number)
	hasLetter := false
	for _, value := range prefix {
		if value >= 'A' && value <= 'Z' {
			hasLetter = true
			break
		}
	}
	if !hasLetter || ignoredMediaPrefixes[prefix] {
		return "", false
	}
	return prefix + "-" + number, true
}

func mediaIDFromTitle(title string) (string, bool) {
	if match := fc2TitlePattern.FindStringSubmatch(title); len(match) == 2 {
		return "FC2-" + match[1], true
	}
	for _, match := range mediaTitlePattern.FindAllStringSubmatch(title, -1) {
		if id, ok := normalizedMediaID(match[1], match[2]); ok {
			return id, true
		}
	}
	return "", false
}

func mediaIDFromVideoFilename(name string) (string, bool) {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if match := fc2FilePattern.FindStringSubmatch(base); len(match) == 2 {
		return "FC2-" + match[1], true
	}
	if match := mediaFilePattern.FindStringSubmatch(base); len(match) == 3 {
		return normalizedMediaID(match[1], match[2])
	}
	return "", false
}

func plannedJavspOutputRoot(config JavspConfig) (string, bool, error) {
	if config.OutputDirectory != "" {
		root, err := canonicalDirectory(config.OutputDirectory)
		if err != nil {
			return "", false, err
		}
		if root == config.InputDirectory || pathWithinRoot(root, config.InputDirectory) {
			return "", false, fmt.Errorf("JAVSP 整理完成目录不能等于或包含扫描目录")
		}
		return root, true, nil
	}
	input := filepath.Clean(config.InputDirectory)
	if !filepath.IsAbs(input) || input == string(filepath.Separator) {
		return "", false, fmt.Errorf("JAVSP 扫描目录无效")
	}
	parts := strings.Split(filepath.ToSlash(config.OutputFolderPattern), "/")
	staticParts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if strings.ContainsAny(part, "{}") {
			break
		}
		staticParts = append(staticParts, part)
	}
	if len(staticParts) == 0 {
		return "", false, nil
	}
	root := filepath.Join(append([]string{input}, staticParts...)...)
	relative, err := filepath.Rel(input, root)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false, fmt.Errorf("JAVSP 整理完成目录不在扫描目录内")
	}
	return root, true, nil
}

func staticJavspOutputRoot(config JavspConfig) (string, bool, error) {
	root, available, err := plannedJavspOutputRoot(config)
	if err != nil || !available {
		return "", available, err
	}
	if config.OutputDirectory != "" {
		return root, true, nil
	}
	input := filepath.Clean(config.InputDirectory)
	resolvedInput, err := filepath.EvalSymlinks(input)
	if err != nil {
		return "", false, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	relative, err := filepath.Rel(resolvedInput, resolvedRoot)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false, fmt.Errorf("JAVSP 整理完成目录不在扫描目录内")
	}
	info, err := os.Stat(resolvedRoot)
	if err != nil {
		return "", false, err
	}
	if !info.IsDir() {
		return "", false, fmt.Errorf("JAVSP 整理完成路径不是目录")
	}
	return resolvedRoot, true, nil
}

func buildCompletedMediaIndex(config JavspConfig) (completedMediaIndex, error) {
	index := completedMediaIndex{ids: map[string]struct{}{}}
	root, available, err := staticJavspOutputRoot(config)
	if err != nil || !available {
		return index, err
	}
	extensions := make(map[string]bool, len(config.FilenameExtensions))
	for _, extension := range config.FilenameExtensions {
		extensions[strings.ToLower(extension)] = true
	}
	visited := 0
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > maxCompletedMediaEntries {
			return fmt.Errorf("JAVSP 整理完成目录超过 %d 个条目", maxCompletedMediaEntries)
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return nil
		}
		if !extensions[strings.ToLower(filepath.Ext(entry.Name()))] {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if id, ok := mediaIDFromVideoFilename(entry.Name()); ok {
			index.ids[id] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return completedMediaIndex{ids: map[string]struct{}{}}, err
	}
	index.available = true
	return index, nil
}

func (index completedMediaIndex) Available() bool { return index.available }

func (index completedMediaIndex) MatchTitle(title string) (string, bool) {
	id, ok := mediaIDFromTitle(title)
	if !ok {
		return "", false
	}
	_, found := index.ids[id]
	return id, found
}
