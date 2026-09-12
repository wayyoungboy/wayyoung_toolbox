package converter

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"git.taurusxin.com/taurusxin/ncmdump-go/ncmcrypt"
)

type ncmFile interface {
	Dump(targetDir string) (bool, error)
	FixMetadata(fetchAlbumImageFromRemote bool) (bool, error)
	GetDumpFilePath() string
}

type ncmFactory func(source string) (ncmFile, error)

type NCMEngine struct {
	open ncmFactory
}

func NewNCMEngine() *NCMEngine {
	return newNCMEngine(func(source string) (ncmFile, error) {
		return ncmcrypt.NewNeteaseCloudMusic(source)
	})
}

func newNCMEngine(factory ncmFactory) *NCMEngine {
	return &NCMEngine{open: factory}
}

func (engine *NCMEngine) Convert(ctx context.Context, source, stagingDir string, fetchRemoteArt bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if engine == nil || engine.open == nil {
		return "", errors.New("NCM 转换组件不可用")
	}
	file, err := engine.open(source)
	if err != nil {
		return "", fmt.Errorf("读取 NCM 文件失败: %w", err)
	}
	dumped, err := file.Dump(stagingDir)
	if err != nil {
		return "", fmt.Errorf("解析音频失败: %w", err)
	}
	if !dumped {
		return "", errors.New("解析音频失败: 转换组件未生成结果")
	}
	output := filepath.Clean(file.GetDumpFilePath())
	if !isWithin(stagingDir, output) {
		return "", errors.New("转换组件返回了临时目录之外的文件")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.EqualFold(filepath.Ext(output), ".m4a") {
		// The upstream metadata writer only supports MP3/FLAC. M4A is restored
		// byte-for-byte, retaining existing container tags but not adding NCM tags.
		return output, nil
	}
	metadata, err := file.FixMetadata(fetchRemoteArt)
	if err != nil {
		return "", fmt.Errorf("写入音乐元数据失败: %w", err)
	}
	if !metadata {
		return "", errors.New("写入音乐元数据失败: 转换组件未确认写入")
	}
	return output, nil
}

func isWithin(root, path string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return relative != ".." && !filepath.IsAbs(relative) && !startsWithParent(relative)
}

func startsWithParent(path string) bool {
	return len(path) > 3 && path[:3] == ".."+string(filepath.Separator)
}
