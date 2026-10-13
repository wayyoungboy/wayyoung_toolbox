package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"time"

	"fnos-backup-center/internal/securefs"
)

type directFile struct {
	name     string
	size     int64
	modified time.Time
	copy     bool
}

// planDirect reads directory entries and file metadata only. It deliberately
// does not hash source data or read back files from the remote mount.
func planDirect(ctx context.Context, src, dst *securefs.Root) ([]directFile, []string, Preview, error) {
	var files []directFile
	var dirs []string
	var preview Preview
	var walk func(string, int) error
	walk = func(dir string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > 128 {
			return errors.New("directory depth limit exceeded")
		}
		entries, err := src.ReadDir(dir)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, item := range entries {
			if len(files)+len(dirs) >= MaxEntries {
				return errors.New("file count limit exceeded")
			}
			name := path.Join(dir, item.Name())
			if err := securefs.ValidPath(name); err != nil {
				return err
			}
			if item.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("%s: 不跟随符号链接", name)
			}
			if item.IsDir() {
				dirs = append(dirs, name)
				if err := walk(name, depth+1); err != nil {
					return err
				}
				continue
			}
			if !item.Type().IsRegular() {
				return fmt.Errorf("%s: 不支持特殊文件", name)
			}
			file, err := src.Open(name)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			st, err := file.Stat()
			file.Close()
			if err != nil {
				return fmt.Errorf("%s: 读取源文件状态: %w", name, err)
			}
			if !st.Mode().IsRegular() {
				return fmt.Errorf("%s: 源文件不是普通文件", name)
			}
			entry := directFile{name: name, size: st.Size(), modified: st.ModTime()}
			remote, err := dst.Open(name)
			if errors.Is(err, os.ErrNotExist) {
				entry.copy = true
				preview.New++
			} else if err != nil {
				return fmt.Errorf("%s: 读取目标文件状态: %w", name, err)
			} else {
				remoteStat, statErr := remote.Stat()
				remote.Close()
				if statErr != nil {
					return fmt.Errorf("%s: 读取目标文件状态: %w", name, statErr)
				}
				if !remoteStat.Mode().IsRegular() {
					return fmt.Errorf("%s: 目标不是普通文件", name)
				}
				entry.copy = remoteStat.Size() != entry.size || entry.modified.After(remoteStat.ModTime())
				if entry.copy {
					preview.Changed++
				} else {
					preview.Unchanged++
				}
			}
			files = append(files, entry)
			preview.Files++
			preview.Bytes += entry.size
		}
		return nil
	}
	if err := walk(".", 0); err != nil {
		return nil, nil, Preview{}, err
	}
	return files, dirs, preview, nil
}

func (e *Engine) previewDirect(ctx context.Context, s Selection) (Preview, error) {
	var out Preview
	if err := s.Validate(true); err != nil {
		return out, err
	}
	v, err := Pin(e.Mounts, s.Mount, s.Target)
	if err != nil {
		return out, fmt.Errorf("打开备份目标目录: %w", err)
	}
	defer v.Close()
	src, err := securefs.OpenRoot(s.Source)
	if err != nil {
		return out, fmt.Errorf("打开源目录: %w", err)
	}
	defer src.Close()
	_, _, out, err = planDirect(ctx, src, v.Root)
	if err != nil {
		return out, err
	}
	return out, v.Check()
}

func (e *Engine) backupDirect(ctx context.Context, s Selection, progress func(Progress)) (out Result, err error) {
	out.State = "failed"
	defer func() {
		if err != nil {
			out.Failed++
			if errors.Is(err, context.Canceled) {
				out.State = "canceled"
			}
		}
	}()
	if err = s.Validate(true); err != nil {
		return out, err
	}
	v, err := Pin(e.Mounts, s.Mount, s.Target)
	if err != nil {
		return out, fmt.Errorf("打开备份目标目录: %w", err)
	}
	defer v.Close()
	src, err := securefs.OpenRoot(s.Source)
	if err != nil {
		return out, fmt.Errorf("打开源目录: %w", err)
	}
	defer src.Close()
	files, dirs, _, err := planDirect(ctx, src, v.Root)
	if err != nil {
		return out, err
	}
	for _, dir := range dirs {
		if err = v.Check(); err != nil {
			return out, err
		}
		if err = v.Root.MkdirAll(dir); err != nil {
			return out, fmt.Errorf("创建目标目录 %s: %w", dir, err)
		}
	}
	var totalBytes int64
	for _, file := range files {
		if file.copy {
			totalBytes += file.size
		}
	}
	for i, file := range files {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		if err = v.Check(); err != nil {
			return out, err
		}
		if file.copy {
			base := out.Bytes
			err = copyDirectFile(ctx, v, src, file.name, func(n int64) {
				if progress != nil {
					progress(Progress{Phase: "copying_direct", Current: file.name, Files: i, Total: len(files), Bytes: base + n, TotalBytes: totalBytes})
				}
			})
			if err != nil {
				return out, fmt.Errorf("复制 %s: %w", file.name, err)
			}
			out.Copied++
			out.Bytes += file.size
		} else {
			out.Unchanged++
		}
		if progress != nil {
			progress(Progress{Phase: "copying_direct", Current: file.name, Files: i + 1, Total: len(files), Bytes: out.Bytes, TotalBytes: totalBytes})
		}
	}
	if err = v.Check(); err != nil {
		return out, err
	}
	out.State = "pending"
	out.Notice = DirectCloudNotice
	return out, nil
}

func copyDirectFile(ctx context.Context, v *Volume, src *securefs.Root, name string, progress func(int64)) (err error) {
	in, err := src.Open(name)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return fmt.Errorf("读取源文件状态: %w", err)
	}
	if !st.Mode().IsRegular() {
		return errors.New("源文件不是普通文件")
	}
	temp := path.Join(path.Dir(name), ".fnos-copy-"+NewID())
	out, err := v.Root.Create(temp)
	if err != nil {
		return err
	}
	defer func() { _ = v.Root.Remove(temp) }()
	buf := make([]byte, 1<<20)
	var written int64
	var sinceCheck int64
	for {
		if err = ctx.Err(); err != nil {
			break
		}
		if sinceCheck >= 64<<20 {
			if err = v.Check(); err != nil {
				break
			}
			sinceCheck = 0
		}
		var n int
		n, err = in.Read(buf)
		if n > 0 {
			var m int
			m, writeErr := out.Write(buf[:n])
			written += int64(m)
			sinceCheck += int64(m)
			if progress != nil {
				progress(written)
			}
			if writeErr != nil {
				err = writeErr
				break
			}
			if m != n {
				err = io.ErrShortWrite
				break
			}
		}
		if errors.Is(err, io.EOF) {
			err = nil
			break
		}
		if err != nil {
			break
		}
		if n == 0 {
			err = io.ErrNoProgress
			break
		}
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if written != st.Size() {
		return errors.New("源文件在复制期间大小发生变化")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = v.Check(); err != nil {
		return err
	}
	return v.Root.RenameReplace(temp, name)
}
