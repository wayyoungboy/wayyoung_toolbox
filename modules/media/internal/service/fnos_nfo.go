package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func javspVideoExtensions(config JavspConfig) map[string]struct{} {
	extensions := make(map[string]struct{}, len(config.FilenameExtensions))
	for _, extension := range config.FilenameExtensions {
		extensions[strings.ToLower(extension)] = struct{}{}
	}
	return extensions
}

func snapshotJavspOutputVideos(config JavspConfig) (string, map[string]struct{}, error) {
	return snapshotJavspOutputVideosContext(context.Background(), config)
}

func snapshotJavspOutputVideosContext(ctx context.Context, config JavspConfig) (string, map[string]struct{}, error) {
	videos := map[string]struct{}{}
	root, available, err := plannedJavspOutputRoot(config)
	if err != nil || !available {
		return "", videos, err
	}
	err = walkJavspOutputVideosContext(ctx, root, javspVideoExtensions(config), func(path string) error {
		videos[path] = struct{}{}
		return nil
	})
	return root, videos, err
}

func createFnosNFOsForNewVideos(config JavspConfig, root string, before map[string]struct{}) (int, error) {
	return createFnosNFOsForNewVideosContext(context.Background(), config, root, before)
}

func createFnosNFOsForNewVideosContext(ctx context.Context, config JavspConfig, root string, before map[string]struct{}) (int, error) {
	plannedRoot, available, err := plannedJavspOutputRoot(config)
	if err != nil || !available {
		return 0, err
	}
	if filepath.Clean(root) != filepath.Clean(plannedRoot) {
		return 0, fmt.Errorf("JAVSP 整理完成目录与配置不一致")
	}
	created := 0
	err = walkJavspOutputVideosContext(ctx, root, javspVideoExtensions(config), func(videoPath string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, existed := before[videoPath]; existed {
			return nil
		}
		directory := filepath.Dir(videoPath)
		source := filepath.Join(directory, "movie.nfo")
		sourceInfo, statErr := os.Lstat(source)
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		if statErr != nil {
			return statErr
		}
		if !sourceInfo.Mode().IsRegular() || sourceInfo.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		extension := filepath.Ext(videoPath)
		destination := strings.TrimSuffix(videoPath, extension) + ".nfo"
		if _, statErr := os.Lstat(destination); statErr == nil {
			return nil
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		copied, copyErr := copyFileExclusiveContext(ctx, source, destination, sourceInfo.Mode().Perm())
		if copyErr != nil {
			return copyErr
		}
		if copied {
			created++
		}
		return nil
	})
	return created, err
}

func walkJavspOutputVideos(root string, extensions map[string]struct{}, visit func(string) error) error {
	return walkJavspOutputVideosContext(context.Background(), root, extensions, visit)
}

func walkJavspOutputVideosContext(ctx context.Context, root string, extensions map[string]struct{}, visit func(string) error) error {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("JAVSP 整理完成路径不是普通目录")
	}
	visited := 0
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > maxCompletedMediaEntries {
			return fmt.Errorf("JAVSP 整理完成目录超过 %d 个条目", maxCompletedMediaEntries)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if _, ok := extensions[strings.ToLower(filepath.Ext(entry.Name()))]; !ok {
			return nil
		}
		entryInfo, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if !entryInfo.Mode().IsRegular() {
			return nil
		}
		return visit(path)
	})
}

func copyFileExclusive(source, destination string, mode os.FileMode) (copied bool, err error) {
	return copyFileExclusiveContext(context.Background(), source, destination, mode)
}

func copyFileExclusiveContext(ctx context.Context, source, destination string, mode os.FileMode) (copied bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	input, err := os.Open(source)
	if err != nil {
		return false, err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() {
		if closeErr := output.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(destination)
		}
	}()
	if _, err = copyWithContext(ctx, output, input); err != nil {
		return false, err
	}
	if err = output.Sync(); err != nil {
		return false, err
	}
	return true, nil
}
