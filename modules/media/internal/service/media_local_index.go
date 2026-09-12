package service

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Local file presence is not a content-equality or completeness verdict.
// Keep actual paths so an obsolete directory listing alone cannot block RSS.
type localMediaFile struct {
	root, relative, source string
	rootInfo, info         os.FileInfo
}

type localMediaIndex struct {
	files map[string][]localMediaFile
}

func buildLocalMediaIndex(ctx context.Context, cfg JavspConfig, intake IntakeConfig) (localMediaIndex, error) {
	if err := ctx.Err(); err != nil {
		return localMediaIndex{}, err
	}
	index := localMediaIndex{files: make(map[string][]localMediaFile)}
	type scope struct{ root, source string }
	var scopes []scope
	output, available, err := staticJavspOutputRoot(cfg)
	if err != nil {
		return localMediaIndex{}, fmt.Errorf("读取已整理目录：%w", err)
	}
	if available {
		scopes = append(scopes, scope{output, "completed"})
	}
	for _, path := range []string{cfg.InputDirectory, intake.DestinationDirectory} {
		root, err := canonicalDirectory(path)
		if err != nil {
			return localMediaIndex{}, fmt.Errorf("读取未处理目录 %q：%w", path, err)
		}
		scopes = append(scopes, scope{root, "pending"})
	}
	extensions := make(map[string]bool, len(cfg.FilenameExtensions))
	for _, extension := range cfg.FilenameExtensions {
		extensions[strings.ToLower(extension)] = true
	}
	var scanned []string
	visited := 0
	for _, scope := range scopes {
		covered := false
		for _, previous := range scanned {
			if configuredPathWithin(previous, scope.root) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		rootInfo, err := os.Lstat(scope.root)
		if err != nil {
			return localMediaIndex{}, fmt.Errorf("读取本地查重目录：%w", err)
		}
		if !rootInfo.IsDir() {
			return localMediaIndex{}, fmt.Errorf("本地查重路径不是普通目录：%s", scope.root)
		}
		root, err := os.OpenRoot(scope.root)
		if err != nil {
			return localMediaIndex{}, fmt.Errorf("打开本地查重目录：%w", err)
		}
		err = fs.WalkDir(root.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return walkErr
			}
			visited++
			if visited > maxCompletedMediaEntries {
				return fmt.Errorf("本地查重目录合计超过 %d 个条目", maxCompletedMediaEntries)
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if entry.IsDir() {
				for _, previous := range scanned {
					if filepath.Join(scope.root, relative) == previous {
						return fs.SkipDir
					}
				}
				return nil
			}
			if !extensions[strings.ToLower(filepath.Ext(entry.Name()))] {
				return nil
			}
			id, ok := unambiguousMediaID(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
			if !ok {
				return nil
			}
			info, err := root.Lstat(relative)
			if err != nil {
				return err
			}
			if info.Mode().IsRegular() && info.Size() > 0 {
				index.files[id] = append(index.files[id], localMediaFile{root: scope.root, relative: relative, source: scope.source, rootInfo: rootInfo, info: info})
			}
			return nil
		})
		closeErr := root.Close()
		if err != nil {
			return localMediaIndex{}, fmt.Errorf("扫描本地查重目录 %q：%w", scope.root, err)
		}
		if closeErr != nil {
			return localMediaIndex{}, closeErr
		}
		scanned = append(scanned, scope.root)
	}
	return index, ctx.Err()
}

// Matching probes only indexed candidates; it never walks a media directory.
func (index localMediaIndex) MatchTitle(ctx context.Context, title string) (*MediaDuplicate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id, ok := unambiguousMediaID(title)
	if !ok {
		return nil, nil
	}
	for _, candidate := range index.files[id] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rootInfo, err := os.Lstat(candidate.root)
		if err != nil {
			return nil, fmt.Errorf("复核本地查重目录：%w", err)
		}
		if !rootInfo.IsDir() || !os.SameFile(candidate.rootInfo, rootInfo) {
			return nil, fmt.Errorf("本地查重目录已变化，请重新检查：%s", candidate.root)
		}
		root, err := os.OpenRoot(candidate.root)
		if err != nil {
			return nil, fmt.Errorf("复核本地查重目录：%w", err)
		}
		// Ancestor links must not redirect a captured candidate, even inside
		// the same root. Reuse intake's descriptor-relative link checks.
		err = rejectIntakeLinks(root, candidate.relative, false)
		var info os.FileInfo
		if err == nil {
			info, err = root.Lstat(candidate.relative)
		}
		closeErr := root.Close()
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("复核本地视频 %q：%w", filepath.Join(candidate.root, candidate.relative), err)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			continue
		}
		if !os.SameFile(candidate.info, info) || candidate.info.Size() != info.Size() || !candidate.info.ModTime().Equal(info.ModTime()) {
			return nil, fmt.Errorf("本地查重视频已变化，请重新检查：%s", filepath.Join(candidate.root, candidate.relative))
		}
		return &MediaDuplicate{Source: candidate.source, MediaID: id, Path: filepath.Join(candidate.root, candidate.relative), CheckedAt: nowUTC()}, nil
	}
	return nil, nil
}
