package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"fnos-backup-center/internal/securefs"
)

// Restore validates before creating an exclusive new directory. Failure preserves
// partial output with an explicit marker; it never overwrites or removes originals.
func (e *Engine) Restore(ctx context.Context, s Selection, id, destination string, progress func(Progress)) (out Result, err error) {
	out.State = "failed"
	defer func() {
		if err != nil {
			out.Failed++
			if errors.Is(err, context.Canceled) {
				out.State = "canceled"
			}
		}
	}()
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination || destination == "/" {
		return out, errors.New("invalid restore directory")
	}
	if Within(s.Target, destination) || Within(destination, s.Target) || s.Source != "" && (Within(s.Source, destination) || Within(destination, s.Source)) {
		return out, errors.New("恢复目录不能与源目录或备份目标重叠")
	}
	r, err := e.repository(ctx, s, false)
	if err != nil {
		return out, err
	}
	defer r.v.Close()
	m, err := r.manifest(ctx, id)
	if err != nil {
		return out, err
	}
	// Reserved bookkeeping names cannot collide with restored user files.
	for _, f := range m.Files {
		if reservedRestorePath(f.Path) {
			return out, errors.New("reserved restore marker collision")
		}
	}
	for _, d := range m.Directories {
		if reservedRestorePath(d) {
			return out, errors.New("reserved restore marker collision")
		}
	}
	parent, err := securefs.OpenRoot(filepath.Dir(destination))
	if err != nil {
		return out, err
	}
	defer parent.Close()
	if err = r.check(ctx); err != nil {
		return out, err
	}
	name := filepath.Base(destination)
	if err = parent.MkdirNew(name); err != nil {
		return out, fmt.Errorf("恢复目标必须是新目录: %w", err)
	}
	// Open from pinned parent rather than resolving the absolute path again.
	dir, err := parent.SubRoot(name)
	if err != nil {
		return out, err
	}
	defer dir.Close()
	meta, _ := json.Marshal(map[string]string{"snapshot_id": id, "state": "partial"})
	f, err := dir.Create(".fnos-restore.partial.json")
	if err != nil {
		return out, err
	}
	_, err = f.Write(meta)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return out, err
	}
	for _, d := range m.Directories {
		if err = r.check(ctx); err != nil {
			return out, err
		}
		if err = dir.MkdirAll(d); err != nil {
			return out, err
		}
	}
	var total int64
	for _, f := range m.Files {
		total += f.Size
	}
	for i, entry := range m.Files {
		if err = r.check(ctx); err != nil {
			return out, err
		}
		base := out.Bytes
		if err = r.restoreFile(ctx, dir, entry, func(n int64) {
			if progress != nil {
				progress(Progress{Phase: "restoring", Current: entry.Path, Files: i, Total: len(m.Files), Bytes: base + n, TotalBytes: total})
			}
		}); err != nil {
			return out, fmt.Errorf("restore %s: %w", entry.Path, err)
		}
		out.Copied++
		out.Bytes += entry.Size
	}
	if err = r.check(ctx); err != nil {
		return out, err
	}
	meta, _ = json.Marshal(map[string]string{"snapshot_id": id, "state": "restored"})
	if err = writeRestoreCompletion(dir, meta); err != nil {
		return out, err
	}
	out.SnapshotID = id
	out.State = "restored"
	out.Notice = "已恢复到新目录并逐文件核对 SHA-256；此结果不代表原云端持久化已独立确认。"
	return out, nil
}

func reservedRestorePath(name string) bool {
	root := strings.SplitN(name, "/", 2)[0]
	return root == ".fnos-restore.partial.json" || root == ".fnos-restore.complete.json" || root == ".fnos-restore.ready.json"
}

func writeRestoreCompletion(dir *securefs.Root, meta []byte) error {
	f, err := dir.Create(".fnos-restore.ready.json")
	if err != nil {
		return err
	}
	_, err = f.Write(meta)
	if err == nil {
		err = f.Sync()
	}
	if ce := f.Close(); err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	if err = dir.RenameNew(".fnos-restore.ready.json", ".fnos-restore.complete.json"); err != nil {
		return err
	}
	if err = dir.File().Sync(); err != nil {
		return err
	}
	if err = dir.Remove(".fnos-restore.partial.json"); err != nil {
		return err
	}
	return dir.File().Sync()
}

func (r *repository) restoreFile(ctx context.Context, dst *securefs.Root, entry Entry, progress func(int64)) error {
	in, err := r.v.Root.Open(r.name("objects/" + entry.ObjectID))
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() != entry.Size {
		return errors.New("restore object size/type mismatch")
	}
	out, err := dst.Create(entry.Path)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := stream(ctx, io.MultiWriter(out, h), io.LimitReader(in, entry.Size+1), func(n int64) error {
		if progress != nil {
			progress(n)
		}
		return r.check(ctx)
	})
	if err == nil {
		err = out.Sync()
	}
	ce := out.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	if n != entry.Size || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
		return errors.New("restored object SHA-256 mismatch")
	}
	verify, err := dst.Open(entry.Path)
	if err != nil {
		return err
	}
	defer verify.Close()
	h.Reset()
	n, err = stream(ctx, h, io.LimitReader(verify, entry.Size+1), func(int64) error { return r.check(ctx) })
	if err != nil {
		return err
	}
	if n != entry.Size || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
		return errors.New("restore readback SHA-256 mismatch")
	}
	return nil
}
