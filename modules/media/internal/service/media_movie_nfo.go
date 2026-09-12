package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Publish only a complete, synced NFO, without replacing another writer's
// destination. An interrupted in-process write removes only its own temp file;
// a process crash can leave a hidden temp, but never a partial final NFO.
func publishQueuedNFO(ctx context.Context, root *os.Root, destination string, content io.Reader) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsLocal(destination) || destination == "." {
		return fmt.Errorf("%w：NFO 路径不在整理目录内", errIntakeConflict)
	}
	if err := rejectIntakeLinks(root, destination, true); err != nil {
		return err
	}
	temporary := filepath.Join(filepath.Dir(destination), ".media-center-nfo-"+rand.Text()+".tmp")
	f, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	identity, err := f.Stat()
	if err != nil {
		return errors.Join(err, f.Close())
	}
	defer func() {
		// A successful no-replace rename has removed the temp already. Never
		// clean a replaced entry if another process changes this private name.
		current, statErr := root.Lstat(temporary)
		if os.IsNotExist(statErr) {
			return
		}
		if statErr != nil {
			resultErr = errors.Join(resultErr, statErr)
			return
		}
		if !os.SameFile(identity, current) {
			resultErr = errors.Join(resultErr, fmt.Errorf("%w：NFO 临时文件身份变化", errIntakeConflict))
			return
		}
		resultErr = errors.Join(resultErr, root.Remove(temporary))
	}()
	_, writeErr := io.Copy(f, queuedNFOContextReader{ctx: ctx, reader: content})
	if err := errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := renameIntakeNoReplace(root, temporary, root, destination); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%w：同名 NFO 在发布前已存在", errIntakeConflict)
		}
		return err
	}
	return syncIntakePathParents(root, destination)
}

type queuedNFOContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r queuedNFOContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
