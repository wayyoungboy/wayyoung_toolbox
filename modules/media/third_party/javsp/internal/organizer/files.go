package organizer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/yuukiy/javsp-go/internal/model"
)

type sourceMoveFile interface {
	io.Reader
	Stat() (os.FileInfo, error)
	Close() error
}

type destinationMoveFile interface {
	io.Writer
	Chmod(os.FileMode) error
	Close() error
}

var (
	renameFile     = os.Rename
	openSourceFile = func(path string) (sourceMoveFile, error) {
		return os.Open(path)
	}
	openDestinationFile = func(path string, flag int, mode os.FileMode) (destinationMoveFile, error) {
		return os.OpenFile(path, flag, mode)
	}
	copyFile   = io.Copy
	removeFile = os.Remove
)

func MoveFiles(movie *model.Movie, hardLink bool) error {
	return MoveFilesContext(context.Background(), movie, hardLink)
}

func MoveFilesContext(ctx context.Context, movie *model.Movie, hardLink bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(movie.Files) == 0 {
		return fmt.Errorf("movie files are required")
	}
	if movie.SaveDir == "" || movie.BaseName == "" {
		return fmt.Errorf("movie save directory and basename are required")
	}

	oldDir := filepath.Dir(movie.Files[0])
	newPaths := make([]string, 0, len(movie.Files))
	for idx, source := range movie.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		ext := filepath.Ext(source)
		base := movie.BaseName
		if len(movie.Files) > 1 {
			base = fmt.Sprintf("%s-CD%d", movie.BaseName, idx+1)
		}
		destination := filepath.Join(movie.SaveDir, base+ext)
		if _, err := os.Stat(destination); err == nil {
			return fmt.Errorf("file exists: %s", destination)
		} else if !os.IsNotExist(err) {
			return err
		}
		if hardLink {
			if err := os.Link(source, destination); err != nil {
				return err
			}
		} else {
			if err := moveFileContext(ctx, source, destination); err != nil {
				return err
			}
		}
		newPaths = append(newPaths, destination)
		movie.NewPaths = newPaths
	}

	entries, err := os.ReadDir(oldDir)
	if err == nil && len(entries) == 0 {
		if removeErr := os.Remove(oldDir); removeErr != nil {
			return removeErr
		}
	}
	return nil
}

func moveFile(source, destination string) error {
	return moveFileContext(context.Background(), source, destination)
}

func moveFileContext(ctx context.Context, source, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := renameFile(source, destination); err != nil {
		if !isCrossDeviceError(err) {
			return err
		}
		return copyAndRemoveContext(ctx, source, destination)
	}
	return nil
}

func copyAndRemove(source, destination string) error {
	return copyAndRemoveContext(context.Background(), source, destination)
}

func copyAndRemoveContext(ctx context.Context, source, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sourceFile, err := openSourceFile(source)
	if err != nil {
		return fmt.Errorf("open source %q: %w", source, err)
	}
	sourceCloseAttempted := false
	closeSource := func() error {
		if sourceCloseAttempted {
			return nil
		}
		sourceCloseAttempted = true
		if err := sourceFile.Close(); err != nil {
			return fmt.Errorf("close source %q: %w", source, err)
		}
		return nil
	}

	info, err := sourceFile.Stat()
	if err != nil {
		return errors.Join(
			fmt.Errorf("stat source %q: %w", source, err),
			closeSource(),
		)
	}
	destinationFile, err := openDestinationFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.Join(
			fmt.Errorf("open destination %q: %w", destination, err),
			closeSource(),
		)
	}
	destinationCloseAttempted := false
	closeDestination := func() error {
		if destinationCloseAttempted {
			return nil
		}
		destinationCloseAttempted = true
		if err := destinationFile.Close(); err != nil {
			return fmt.Errorf("close destination %q: %w", destination, err)
		}
		return nil
	}
	cleanupFailure := func(primary error) error {
		cleanupErrors := []error{
			primary,
			closeDestination(),
			closeSource(),
		}
		if err := removeFile(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove partial destination %q: %w", destination, err))
		}
		return errors.Join(cleanupErrors...)
	}

	if _, err := copyFile(destinationFile, &contextReader{ctx: ctx, reader: sourceFile}); err != nil {
		return cleanupFailure(fmt.Errorf("copy %q to %q: %w", source, destination, err))
	}
	if err := ctx.Err(); err != nil {
		return cleanupFailure(err)
	}
	if err := destinationFile.Chmod(info.Mode().Perm()); err != nil {
		return cleanupFailure(fmt.Errorf("chmod destination %q: %w", destination, err))
	}
	if err := closeDestination(); err != nil {
		return cleanupFailure(err)
	}
	if err := closeSource(); err != nil {
		return cleanupFailure(err)
	}
	if err := removeFile(source); err != nil {
		return cleanupFailure(fmt.Errorf("remove source %q: %w", source, err))
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
