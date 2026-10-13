package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"

	"fnos-backup-center/internal/securefs"
)

type Engine struct {
	Mounts MountProvider
	// Internal syscall seam for fault tests; never supplied by API or config.
	commitDir func(*securefs.Root, string) error
}

func (e *Engine) Snapshots(ctx context.Context, s Selection) ([]Manifest, error) {
	r, err := e.repository(ctx, s, false)
	if errors.Is(err, ErrNoRepository) {
		return []Manifest{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer r.v.Close()
	return r.snapshots(ctx)
}

func (e *Engine) Preview(ctx context.Context, s Selection) (Preview, error) {
	if s.Mode == ModeDirect {
		return e.previewDirect(ctx, s)
	}
	var out Preview
	if err := s.Validate(true); err != nil {
		return out, err
	}
	v, err := Pin(e.Mounts, s.Mount, s.Target)
	if err != nil {
		return out, err
	}
	defer v.Close()
	src, err := securefs.OpenRoot(s.Source)
	if err != nil {
		return out, err
	}
	defer src.Close()
	files, _, err := scan(ctx, src, nil)
	if err != nil {
		return out, err
	}
	all, err := e.Snapshots(ctx, s)
	if err != nil {
		return out, err
	}
	old := map[string]Entry{}
	if len(all) > 0 {
		for _, f := range all[len(all)-1].Files {
			old[f.Path] = f
		}
	}
	for _, f := range files {
		out.Files++
		out.Bytes += f.Size
		prev, ok := old[f.Path]
		if !ok {
			out.New++
		} else if prev.SHA256 == f.SHA256 && prev.Size == f.Size {
			out.Unchanged++
		} else {
			out.Changed++
		}
		delete(old, f.Path)
	}
	out.Removed = len(old)
	return out, v.Check()
}

func scan(ctx context.Context, src *securefs.Root, progress func(Progress)) ([]Entry, []string, error) {
	files := []Entry{}
	dirs := []string{}
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
		for _, d := range entries {
			if len(files)+len(dirs) >= MaxEntries {
				return errors.New("file count limit exceeded")
			}
			name := path.Join(dir, d.Name())
			if err := securefs.ValidPath(name); err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("%s: 不跟随符号链接", name)
			}
			if d.IsDir() {
				dirs = append(dirs, name)
				if err := walk(name, depth+1); err != nil {
					return err
				}
				continue
			}
			if !d.Type().IsRegular() {
				return fmt.Errorf("%s: 不支持特殊文件", name)
			}
			entry, err := inspect(ctx, src, name)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			files = append(files, entry)
			if progress != nil {
				progress(Progress{Phase: "scanning", Current: name, Files: len(files)})
			}
		}
		return nil
	}
	if err := walk(".", 0); err != nil {
		return nil, nil, err
	}
	return files, dirs, nil
}

func inspect(ctx context.Context, src *securefs.Root, name string) (Entry, error) {
	var out Entry
	f, err := src.Open(name)
	if err != nil {
		return out, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return out, err
	}
	if !before.Mode().IsRegular() {
		return out, errors.New("not a regular file")
	}
	h := sha256.New()
	n, err := stream(ctx, h, io.LimitReader(f, before.Size()+1), nil)
	if err != nil {
		return out, err
	}
	after, err := f.Stat()
	if err != nil {
		return out, err
	}
	if n != before.Size() || !sameFileState(before, after) {
		return out, errors.New("扫描期间源文件发生变化")
	}
	return Entry{Path: name, Size: n, SHA256: hex.EncodeToString(h.Sum(nil)), Modified: before.ModTime().UTC()}, nil
}
func sameFileState(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && a.Mode() == b.Mode()
}

func (e *Engine) Backup(ctx context.Context, s Selection, progress func(Progress)) (out Result, err error) {
	if s.Mode == ModeDirect {
		return e.backupDirect(ctx, s, progress)
	}
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
	preflight, err := Pin(e.Mounts, s.Mount, s.Target)
	if err != nil {
		return out, err
	}
	defer preflight.Close()
	src, err := securefs.OpenRoot(s.Source)
	if err != nil {
		return out, err
	}
	defer src.Close()
	// Scan before creating repository artifacts. Hash every source, not just metadata.
	files, dirs, err := scan(ctx, src, progress)
	if err != nil {
		return out, err
	}
	r, err := e.repository(ctx, s, true)
	if err != nil {
		return out, err
	}
	defer r.v.Close()
	all, err := r.snapshots(ctx)
	if err != nil {
		return out, err
	}
	previous := map[string]Entry{}
	committedObjects := map[string]Entry{}
	for _, snapshot := range all {
		for _, entry := range snapshot.Files {
			committedObjects[entry.SHA256] = entry
		}
	}
	if len(all) > 0 {
		for _, f := range all[len(all)-1].Files {
			previous[f.Path] = f
		}
	}
	var totalBytes int64
	for _, f := range files {
		totalBytes += f.Size
	}
	for i, entry := range files {
		if err = r.check(ctx); err != nil {
			return out, err
		}
		old, unchanged := previous[entry.Path]
		unchanged = unchanged && old.SHA256 == entry.SHA256 && old.Size == entry.Size
		// Revalidate even reused content. A truncated cached object is not a backup.
		reference, reusable := committedObjects[entry.SHA256]
		if reusable {
			if reference.Size != entry.Size {
				return out, errors.New("committed object size conflict")
			}
			entry.ObjectID = reference.ObjectID
			if verifyErr := r.verify(ctx, r.name("objects/"+entry.ObjectID), entry.Size, entry.SHA256, nil); verifyErr != nil {
				return out, fmt.Errorf("verify %s: %w", entry.Path, verifyErr)
			}
		}
		if !reusable {
			entry.ObjectID = entry.SHA256 + "." + NewID()
			baseBytes := out.Bytes
			err = r.copyObject(ctx, src, entry, func(n int64) {
				if progress != nil {
					progress(Progress{Phase: "copying", Current: entry.Path, Files: i, Total: len(files), Bytes: baseBytes + n, TotalBytes: totalBytes})
				}
			})
			if err != nil {
				return out, fmt.Errorf("copy %s: %w", entry.Path, err)
			}
			out.Bytes += entry.Size
			out.Copied++
		} else if unchanged {
			out.Unchanged++
		} else {
			out.Copied++
		}
		files[i].ObjectID = entry.ObjectID
		committedObjects[entry.SHA256] = entry
		if progress != nil {
			progress(Progress{Phase: "verifying", Current: entry.Path, Files: i + 1, Total: len(files), Bytes: out.Bytes, TotalBytes: totalBytes})
		}
	}
	if err = r.check(ctx); err != nil {
		return out, err
	}
	m, err := r.publish(ctx, files, dirs)
	if errors.Is(err, ErrPublicationUnknown) {
		out.SnapshotID, out.State, out.Notice = m.ID, "publication_unknown", ErrPublicationUnknown.Error()
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.SnapshotID = m.ID
	out.State = "pending"
	out.Notice = CloudNotice
	return out, nil
}

func (r *repository) copyObject(ctx context.Context, src *securefs.Root, entry Entry, progress func(int64)) (err error) {
	if err = r.check(ctx); err != nil {
		return err
	}
	in, err := src.Open(entry.Path)
	if err != nil {
		return err
	}
	defer in.Close()
	before, err := in.Stat()
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() || before.Size() != entry.Size || !before.ModTime().Equal(entry.Modified) {
		return errors.New("源文件在扫描后发生变化")
	}
	object := r.name("objects/" + entry.ObjectID)
	out, err := r.v.Root.Create(object)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, copyErr := stream(ctx, io.MultiWriter(out, h), io.LimitReader(in, entry.Size+1), func(n int64) error {
		if progress != nil {
			progress(n)
		}
		return r.check(ctx)
	})
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return copyErr
	}
	after, err := in.Stat()
	if err != nil {
		return err
	}
	if n != entry.Size || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 || !sameFileState(before, after) {
		return errors.New("复制期间源文件发生变化，未提交备份")
	}
	return r.verify(ctx, object, entry.Size, entry.SHA256, nil)
}
