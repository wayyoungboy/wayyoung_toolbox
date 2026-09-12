package converter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// migrateMP3 stages an unchanged copy on the destination filesystem, so the
// same procedure works across volumes. Source deletion only follows a flushed,
// published copy; failed or canceled transfers never remove the original.
func (runner *Runner) migrateMP3(ctx context.Context, options RunOptions, item Item, destinationDir string) Result {
	result := Result{Source: item.Source, Format: "mp3", Status: StatusFailed}
	destination := filepath.Join(destinationDir, filepath.Base(item.Source))
	sourceInfo, err := os.Lstat(item.Source)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if targetInfo, err := os.Stat(destination); err == nil && os.SameFile(sourceInfo, targetInfo) {
		result.Output, result.Status = destination, StatusSkipped
		return result
	}
	if err := os.MkdirAll(destinationDir, 0o755); err != nil {
		result.Error = fmt.Sprintf("创建迁移目录失败: %v", err)
		return result
	}
	staging, err := os.CreateTemp(destinationDir, ".music-migrate-")
	if err != nil {
		result.Error = fmt.Sprintf("创建迁移临时文件失败: %v", err)
		return result
	}
	staged := staging.Name()
	defer os.Remove(staged)
	if err := copyMP3(ctx, item.Source, staging, sourceInfo); err != nil {
		if ctx.Err() != nil {
			result.Status = StatusCanceled
		}
		result.Error = fmt.Sprintf("复制 MP3 失败，源文件已保留: %v", err)
		return result
	}
	runner.commitMu.Lock()
	defer runner.commitMu.Unlock()
	if err := ctx.Err(); err != nil {
		result.Status, result.Error = StatusCanceled, err.Error()
		return result
	}
	if err := unchangedSource(item.Source, sourceInfo); err != nil {
		result.Error = err.Error()
		return result
	}

	// Hard-linking the staged file into place is atomic and cannot overwrite
	// another file under skip/rename, including files created by another process.
	if options.Conflict == ConflictOverwrite {
		err = os.Rename(staged, destination)
	} else {
		for {
			err = os.Link(staged, destination)
			if !errors.Is(err, os.ErrExist) {
				break
			}
			if options.Conflict == ConflictSkip {
				result.Output, result.Status = destination, StatusSkipped
				return result
			}
			destination = availableName(destination)
			if ctx.Err() != nil {
				result.Status, result.Error = StatusCanceled, ctx.Err().Error()
				return result
			}
		}
	}
	if err != nil {
		result.Error = fmt.Sprintf("保存 MP3 失败，源文件已保留: %v", err)
		return result
	}
	result.Output = destination
	if err := syncDirectory(destinationDir); err != nil {
		result.Error = fmt.Sprintf("目标目录同步失败，源文件已保留: %v", err)
		return result
	}
	if err := unchangedSource(item.Source, sourceInfo); err != nil {
		result.Error = err.Error()
		return result
	}
	if err := os.Remove(item.Source); err != nil {
		result.Error = fmt.Sprintf("目标已保存，但无法移除源 MP3（请检查目录写入权限）: %v", err)
		return result
	}
	result.Status = StatusCompleted
	return result
}

func copyMP3(ctx context.Context, source string, destination *os.File, info os.FileInfo) (err error) {
	defer func() {
		if closeErr := destination.Close(); err == nil {
			err = closeErr
		}
	}()
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	openedInfo, err := input.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, openedInfo) {
		return errors.New("源 MP3 已被替换")
	}
	n, err := io.Copy(destination, &contextReader{ctx: ctx, reader: input})
	if err != nil {
		return err
	}
	if n != info.Size() {
		return errors.New("源 MP3 大小发生变化")
	}
	if err := unchangedSource(source, info); err != nil {
		return err
	}
	if err := destination.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Chtimes(destination.Name(), info.ModTime(), info.ModTime()); err != nil {
		return err
	}
	return destination.Sync()
}

func unchangedSource(source string, before os.FileInfo) error {
	after, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return errors.New("迁移期间源 MP3 发生变化，未删除源文件")
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
