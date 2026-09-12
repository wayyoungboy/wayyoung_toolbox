package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fnos-backup-center/internal/securefs"
	"io"
	"os"
	"path"
	"strings"
	"time"
)

var ErrNoRepository = errors.New("repository does not exist")
var ErrPublicationUnknown = errors.New("提交标记创建结果不确定，请刷新版本列表核对；未自动重试")

type repository struct {
	v          *Volume
	prefix, id string
	commitDir  func(*securefs.Root, string) error
}
type marker struct {
	Schema int    `json:"schema"`
	ID     string `json:"id"`
}

func (e *Engine) repository(ctx context.Context, s Selection, create bool) (*repository, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.Validate(false); err != nil {
		return nil, err
	}
	v, err := Pin(e.Mounts, s.Mount, s.Target)
	if err != nil {
		return nil, err
	}
	r := &repository{v: v, prefix: RepositoryPath(s.RepoID), id: s.RepoID}
	r.commitDir = e.commitDir
	if r.commitDir == nil {
		r.commitDir = (*securefs.Root).MkdirNew
	}
	if err := r.initialize(ctx, create); err != nil {
		v.Close()
		return nil, err
	}
	return r, nil
}
func (r *repository) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.v.Check()
}
func (r *repository) name(name string) string { return path.Join(r.prefix, name) }
func (r *repository) initialize(ctx context.Context, create bool) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	b, err := r.v.Root.ReadFile(r.name("repository.json"), 4096)
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return ErrNoRepository
		}
		if err := r.v.Root.MkdirAll("fnos-backup-center"); err != nil {
			return err
		}
		if err := r.check(ctx); err != nil {
			return err
		}
		if err := r.v.Root.MkdirNew(r.prefix); err != nil {
			return errors.New("备份仓库已存在但缺少有效标记；拒绝覆盖或接管")
		}
		for _, d := range []string{"objects", "snapshots", "commits"} {
			if err := r.check(ctx); err != nil {
				return err
			}
			if err := r.v.Root.MkdirNew(r.name(d)); err != nil {
				return err
			}
		}
		b, err = json.Marshal(marker{Schema: Schema, ID: r.id})
		if err != nil {
			return err
		}
		return r.writeMetadata(ctx, "repository.json", b)
	}
	if err != nil {
		return err
	}
	var m marker
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	if m.Schema != Schema || m.ID != r.id {
		return errors.New("备份仓库标记不匹配，拒绝操作")
	}
	return r.check(ctx)
}

func (r *repository) writeMetadata(ctx context.Context, name string, b []byte) (err error) {
	if err = r.check(ctx); err != nil {
		return err
	}
	// Exclusive immutable creation; a metadata file is NOT a committed snapshot.
	// Publication is a separate atomic directory creation after verified readback.
	target := r.name(name)
	f, err := r.v.Root.Create(target)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = r.check(ctx); err != nil {
		return err
	}
	return r.verify(ctx, target, int64(len(b)), hashBytes(b), nil)
}
func hashBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func (r *repository) verify(ctx context.Context, name string, size int64, hash string, tick func(int64)) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	f, err := r.v.Root.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() != size {
		return errors.New("object size/type verification failed")
	}
	h := sha256.New()
	n, err := stream(ctx, h, io.LimitReader(f, size+1), func(n int64) error {
		if tick != nil {
			tick(n)
		}
		return r.check(ctx)
	})
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != hash {
		return errors.New("SHA-256 integrity verification failed")
	}
	return r.check(ctx)
}

func (r *repository) snapshots(ctx context.Context) ([]Manifest, error) {
	if err := r.check(ctx); err != nil {
		return nil, err
	}
	entries, err := r.v.Root.ReadDir(r.name("commits"))
	if err != nil {
		return nil, err
	}
	result := make([]Manifest, 0, len(entries))
	if len(entries) > 10000 {
		return nil, errors.New("snapshot limit exceeded")
	}
	for _, entry := range entries {
		parts := strings.Split(entry.Name(), ".")
		if len(parts) != 3 || !idPattern.MatchString(parts[0]) || !hashPattern.MatchString(parts[1]) || parts[2] != "commit" || !entry.IsDir() {
			return nil, errors.New("invalid snapshot commit marker")
		}
		m, err := r.manifest(ctx, parts[0])
		if err != nil {
			return nil, err
		}
		b, err := r.v.Root.ReadFile(r.name("snapshots/"+m.ID+".json"), MaxManifestBytes)
		if err != nil || hashBytes(b) != parts[1] {
			return nil, errors.New("snapshot seal mismatch")
		}
		result = append(result, m)
	}
	sortManifests(result)
	return result, r.check(ctx)
}
func (r *repository) manifest(ctx context.Context, id string) (Manifest, error) {
	var m Manifest
	if !idPattern.MatchString(id) {
		return m, errors.New("invalid snapshot ID")
	}
	if err := r.check(ctx); err != nil {
		return m, err
	}
	b, err := r.v.Root.ReadFile(r.name("snapshots/"+id+".json"), MaxManifestBytes)
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	if m.ID != id {
		return m, errors.New("snapshot identity mismatch")
	}
	if err = m.Validate(r.id); err != nil {
		return m, err
	}
	seal, err := r.v.Root.OpenDir(r.name("commits/" + id + "." + hashBytes(b) + ".commit"))
	if err != nil {
		return m, errors.New("备份清单缺少匹配的完整提交标记")
	}
	entries, readErr := seal.ReadDir(1)
	seal.Close()
	if readErr != io.EOF || len(entries) != 0 {
		return m, errors.New("invalid non-empty snapshot seal")
	}
	return m, r.check(ctx)
}
func (r *repository) publish(ctx context.Context, files []Entry, dirs []string) (Manifest, error) {
	m := Manifest{Schema: Schema, ID: NewID(), RepoID: r.id, Created: time.Now().UTC(), Files: files, Directories: dirs}
	m.Digest = m.checksum()
	if err := m.Validate(r.id); err != nil {
		return m, err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return m, err
	}
	if len(b) > MaxManifestBytes {
		return m, errors.New("manifest size limit exceeded")
	}
	if err = r.writeMetadata(ctx, "snapshots/"+m.ID+".json", b); err != nil {
		return Manifest{}, err
	}
	if err = r.check(ctx); err != nil {
		return Manifest{}, err
	}
	// Atomic namespace operation is the linearization point. A successfully
	// created empty seal has no half-written body. Never report post-commit
	// cancellation as a failed backup; all published versions remain cloud-pending.
	if err = r.commitDir(r.v.Root, r.name("commits/"+m.ID+"."+hashBytes(b)+".commit")); err != nil {
		return m, ErrPublicationUnknown
	}
	return m, nil
}

// stream checks cancellation and mount identity before/after each bounded chunk.
func stream(ctx context.Context, w io.Writer, rd io.Reader, check func(int64) error) (int64, error) {
	buf := make([]byte, 256*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		if check != nil {
			if err := check(total); err != nil {
				return total, err
			}
		}
		n, re := rd.Read(buf)
		if n > 0 {
			if err := ctx.Err(); err != nil {
				return total, err
			}
			written, we := w.Write(buf[:n])
			total += int64(written)
			if we != nil {
				return total, we
			}
			if written != n {
				return total, io.ErrShortWrite
			}
			if check != nil {
				if err := check(total); err != nil {
					return total, err
				}
			}
		}
		if re == io.EOF {
			return total, nil
		}
		if re != nil {
			return total, re
		}
		if n == 0 {
			return total, io.ErrNoProgress
		}
	}
}
