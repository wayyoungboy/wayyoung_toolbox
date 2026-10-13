// Package securefs implements descriptor-relative, no-symlink filesystem access.
// Production support requires Linux openat2 and no-clobber renameat2 support.
package securefs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"golang.org/x/sys/unix"
)

type Root struct{ dir *os.File }

func valid(name string, dot bool) error {
	if dot && name == "." {
		return nil
	}
	if name == "" || name == "." || strings.ContainsAny(name, "\\\x00") || path.IsAbs(name) || path.Clean(name) != name {
		return fmt.Errorf("invalid relative path %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return errors.New("path escapes root")
		}
	}
	return nil
}

func ValidPath(name string) error { return valid(name, false) }

func OpenRoot(name string) (*Root, error) {
	fd, err := openAbsoluteRoot(name)
	if err != nil {
		return nil, fmt.Errorf("open root: %w", err)
	}
	return &Root{dir: os.NewFile(uintptr(fd), name)}, nil
}

func (r *Root) Close() error   { return r.dir.Close() }
func (r *Root) File() *os.File { return r.dir }

// SubRoot retains the already-validated directory handle, even if parents move.
func (r *Root) SubRoot(name string) (*Root, error) {
	f, err := r.OpenDir(name)
	if err != nil {
		return nil, err
	}
	return &Root{dir: f}, nil
}

func (r *Root) open(name string, flags int, mode uint32) (*os.File, error) {
	if err := valid(name, flags&unix.O_DIRECTORY != 0); err != nil {
		return nil, err
	}
	fd, err := openBeneath(int(r.dir.Fd()), name, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, mode)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}

func (r *Root) Open(name string) (*os.File, error) { return r.open(name, unix.O_RDONLY, 0) }
func (r *Root) OpenDir(name string) (*os.File, error) {
	return r.open(name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
}
func (r *Root) Create(name string) (*os.File, error) {
	return r.open(name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0600)
}

func (r *Root) ReadFile(name string, limit int64) ([]byte, error) {
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("not a regular file or exceeds read limit")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("read limit exceeded")
	}
	return b, err
}

func (r *Root) ReadDir(name string) ([]os.DirEntry, error) {
	f, err := r.OpenDir(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

func (r *Root) parent(name string) (*os.File, string, error) {
	if err := valid(name, false); err != nil {
		return nil, "", err
	}
	f, err := r.OpenDir(path.Dir(name))
	return f, path.Base(name), err
}

func (r *Root) MkdirAll(name string) error {
	if err := valid(name, true); err != nil {
		return err
	}
	if name == "." {
		return nil
	}
	current := ""
	for _, part := range strings.Split(name, "/") {
		current = path.Join(current, part)
		parent, leaf, err := r.parent(current)
		if err != nil {
			return err
		}
		err = unix.Mkdirat(int(parent.Fd()), leaf, 0700)
		parent.Close()
		if err != nil && !errors.Is(err, unix.EEXIST) {
			return err
		}
		dir, err := r.OpenDir(current)
		if err != nil {
			return err
		}
		dir.Close()
	}
	return nil
}

// MkdirNew creates an exclusive directory. It never accepts an existing path.
func (r *Root) MkdirNew(name string) error {
	parent, leaf, err := r.parent(name)
	if err != nil {
		return err
	}
	defer parent.Close()
	return unix.Mkdirat(int(parent.Fd()), leaf, 0700)
}

func (r *Root) RenameNew(old, name string) error {
	op, ol, err := r.parent(old)
	if err != nil {
		return err
	}
	defer op.Close()
	np, nl, err := r.parent(name)
	if err != nil {
		return err
	}
	defer np.Close()
	if err = renameNew(int(op.Fd()), ol, int(np.Fd()), nl); err != nil {
		return &os.LinkError{Op: "rename-no-replace", Old: old, New: name, Err: err}
	}
	return nil
}

// RenameReplace atomically publishes a completed file in the same root.
// An existing destination entry is replaced, never followed.
func (r *Root) RenameReplace(old, name string) error {
	op, ol, err := r.parent(old)
	if err != nil {
		return err
	}
	defer op.Close()
	np, nl, err := r.parent(name)
	if err != nil {
		return err
	}
	defer np.Close()
	if err = unix.Renameat(int(op.Fd()), ol, int(np.Fd()), nl); err != nil {
		return &os.LinkError{Op: "rename-replace", Old: old, New: name, Err: err}
	}
	return nil
}

// Remove removes a single directory entry, never recursively and never a directory.
func (r *Root) Remove(name string) error {
	p, leaf, err := r.parent(name)
	if err != nil {
		return err
	}
	defer p.Close()
	return unix.Unlinkat(int(p.Fd()), leaf, 0)
}
