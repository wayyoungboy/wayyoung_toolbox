package converter

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var ErrInvalidSource = errors.New("invalid source directory")

type ConflictPolicy string

const (
	ConflictSkip      ConflictPolicy = "skip"
	ConflictRename    ConflictPolicy = "rename"
	ConflictOverwrite ConflictPolicy = "overwrite"
)

type Status string

const (
	StatusWaiting   Status = "waiting"
	StatusCompleted Status = "completed"
	StatusSkipped   Status = "skipped"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
)

type ScanOptions struct {
	SourceDir  string
	OutputDir  string
	Recursive  bool
	MigrateMP3 bool
}

type RunOptions struct {
	SourceDir      string
	OutputDir      string
	PreserveTree   bool
	FetchRemoteArt bool
	MigrateMP3     bool
	Conflict       ConflictPolicy
	Concurrency    int
}

type Item struct {
	Source   string `json:"source"`
	Relative string `json:"relative"`
}

type Result struct {
	Source string `json:"source"`
	Output string `json:"output,omitempty"`
	Format string `json:"format,omitempty"`
	Status Status `json:"status"`
	Error  string `json:"error,omitempty"`
}

type Engine interface {
	Convert(ctx context.Context, source, stagingDir string, fetchRemoteArt bool) (string, error)
}

type Runner struct {
	engine   Engine
	commitMu sync.Mutex
}

func NewRunner(engine Engine) *Runner {
	return &Runner{engine: engine}
}

func Scan(ctx context.Context, options ScanOptions) ([]Item, error) {
	source, err := validDirectory(options.SourceDir)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}

	items := make([]Item, 0)
	if !options.Recursive {
		entries, readErr := os.ReadDir(source)
		if readErr != nil {
			return nil, fmt.Errorf("read source directory: %w", readErr)
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if entry.Type().IsRegular() && supportedSource(entry.Name(), options.MigrateMP3) {
				items = append(items, Item{Source: filepath.Join(source, entry.Name()), Relative: entry.Name()})
			}
		}
		return items, nil
	}

	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == source {
			return nil
		}
		if entry.IsDir() && options.OutputDir != "" && filepath.Clean(options.OutputDir) != source && path == filepath.Clean(options.OutputDir) {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || !supportedSource(entry.Name(), options.MigrateMP3) {
			return nil
		}
		relative, relErr := filepath.Rel(source, path)
		if relErr != nil {
			return fmt.Errorf("relative path for %s: %w", path, relErr)
		}
		items = append(items, Item{Source: path, Relative: relative})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan source directory: %w", err)
	}
	return items, nil
}

func (runner *Runner) Run(ctx context.Context, options RunOptions, items []Item) []Result {
	results := make([]Result, len(items))
	if err := validateRun(options, runner.engine); err != nil {
		for index, item := range items {
			results[index] = Result{Source: item.Source, Status: StatusFailed, Error: err.Error()}
		}
		return results
	}

	type indexedItem struct {
		index int
		item  Item
	}
	queue := make(chan indexedItem)
	var workers sync.WaitGroup
	for range options.Concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range queue {
				results[job.index] = runner.convertOne(ctx, options, job.item)
			}
		}()
	}

	for index, item := range items {
		select {
		case queue <- indexedItem{index: index, item: item}:
		case <-ctx.Done():
			results[index] = Result{Source: item.Source, Status: StatusCanceled, Error: ctx.Err().Error()}
			for remaining := index + 1; remaining < len(items); remaining++ {
				results[remaining] = Result{Source: items[remaining].Source, Status: StatusCanceled, Error: ctx.Err().Error()}
			}
			close(queue)
			workers.Wait()
			return results
		}
	}
	close(queue)
	workers.Wait()
	return results
}

func (runner *Runner) convertOne(ctx context.Context, options RunOptions, item Item) Result {
	result := Result{Source: item.Source, Status: StatusFailed}
	if err := ctx.Err(); err != nil {
		result.Status = StatusCanceled
		result.Error = err.Error()
		return result
	}
	if err := validateItem(options.SourceDir, item, options.MigrateMP3); err != nil {
		result.Error = err.Error()
		return result
	}

	destinationDir := options.OutputDir
	if options.PreserveTree {
		destinationDir = filepath.Join(destinationDir, filepath.Dir(item.Relative))
	}
	if isMP3(item.Source) {
		return runner.migrateMP3(ctx, options, item, destinationDir)
	}
	if err := os.MkdirAll(destinationDir, 0o755); err != nil {
		result.Error = fmt.Sprintf("创建输出目录失败: %v", err)
		return result
	}
	stagingDir, err := os.MkdirTemp(destinationDir, ".music-converter-")
	if err != nil {
		result.Error = fmt.Sprintf("创建临时目录失败: %v", err)
		return result
	}
	defer os.RemoveAll(stagingDir)

	staged, err := guardedConvert(runner.engine, ctx, item.Source, stagingDir, options.FetchRemoteArt)
	if err != nil {
		if ctx.Err() != nil {
			result.Status = StatusCanceled
			result.Error = ctx.Err().Error()
			return result
		}
		result.Error = err.Error()
		return result
	}
	info, err := os.Stat(staged)
	if err != nil || !info.Mode().IsRegular() {
		result.Error = "转换组件未生成有效的音频文件"
		return result
	}

	destination := filepath.Join(destinationDir, filepath.Base(staged))
	result.Format = strings.TrimPrefix(strings.ToLower(filepath.Ext(staged)), ".")
	runner.commitMu.Lock()
	defer runner.commitMu.Unlock()
	switch options.Conflict {
	case ConflictSkip:
		if pathExists(destination) {
			result.Output = destination
			result.Status = StatusSkipped
			return result
		}
	case ConflictRename:
		destination = availableName(destination)
	case ConflictOverwrite:
		if pathExists(destination) {
			if err := os.Remove(destination); err != nil {
				result.Error = fmt.Sprintf("覆盖旧文件失败: %v", err)
				return result
			}
		}
	}

	if err := os.Rename(staged, destination); err != nil {
		result.Error = fmt.Sprintf("保存转换结果失败: %v", err)
		return result
	}
	result.Output = destination
	result.Status = StatusCompleted
	return result
}

func guardedConvert(engine Engine, ctx context.Context, source, stagingDir string, fetchRemoteArt bool) (output string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("解析异常: %v", recovered)
		}
	}()
	return engine.Convert(ctx, source, stagingDir, fetchRemoteArt)
}

func validateRun(options RunOptions, engine Engine) error {
	if engine == nil {
		return errors.New("转换组件不可用")
	}
	if options.Concurrency < 1 || options.Concurrency > 8 {
		return errors.New("并发任务数必须在 1 到 8 之间")
	}
	if _, err := validDirectory(options.SourceDir); err != nil {
		return fmt.Errorf("源目录无效: %w", err)
	}
	if _, err := validDirectory(options.OutputDir); err != nil {
		return fmt.Errorf("输出目录无效: %w", err)
	}
	switch options.Conflict {
	case ConflictSkip, ConflictRename, ConflictOverwrite:
		return nil
	default:
		return errors.New("未知的文件冲突策略")
	}
}

func validateItem(sourceDir string, item Item, allowMP3 bool) error {
	if item.Source == "" || item.Relative == "" {
		return errors.New("待转换文件信息不完整")
	}
	cleanRelative := filepath.Clean(item.Relative)
	if filepath.IsAbs(cleanRelative) || cleanRelative == ".." || strings.HasPrefix(cleanRelative, ".."+string(filepath.Separator)) {
		return errors.New("待转换文件路径越过源目录")
	}
	cleanSource := filepath.Clean(item.Source)
	expected := filepath.Join(filepath.Clean(sourceDir), cleanRelative)
	if cleanSource != expected {
		return errors.New("待转换文件与源目录不一致")
	}
	info, err := os.Lstat(cleanSource)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !supportedSource(cleanSource, allowMP3) {
		return errors.New("待处理文件不是允许的普通 NCM / MP3 文件")
	}
	return nil
}

func validDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	clean := filepath.Clean(path)
	info, err := os.Stat(clean)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("path is not a directory")
	}
	return clean, nil
}

func isNCM(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".ncm")
}

func isMP3(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".mp3")
}

func supportedSource(name string, allowMP3 bool) bool {
	return isNCM(name) || (allowMP3 && isMP3(name))
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func availableName(path string) string {
	if !pathExists(path) {
		return path
	}
	extension := filepath.Ext(path)
	base := strings.TrimSuffix(path, extension)
	for index := 1; ; index++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, index, extension)
		if !pathExists(candidate) {
			return candidate
		}
	}
}
