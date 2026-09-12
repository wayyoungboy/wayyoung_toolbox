package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) (*Engine, Selection, *fakeMounts) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source, target := filepath.Join(base, "source"), filepath.Join(base, "cloud")
	for _, p := range []string{source, target} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	m := Mount{ID: 77, Point: target, Source: "fixture:cloud", Type: "fuse.rclone", Root: "/"}
	p := &fakeMounts{items: []Mount{m}, fileID: 77}
	return &Engine{Mounts: p}, Selection{Source: source, Target: target, RepoID: "0123456789abcdef0123456789abcdef", Mount: m}, p
}

func write(t *testing.T, root, name, data string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func TestIncrementalTwoBackupsAndRestore(t *testing.T) {
	e, s, _ := fixture(t)
	ctx := context.Background()
	write(t, s.Source, "unchanged.txt", "same")
	write(t, s.Source, "nested/changed.txt", "old")
	write(t, s.Source, "removed.txt", "keep historical")
	p, err := e.Preview(ctx, s)
	if err != nil || p.New != 3 {
		t.Fatalf("preview: %+v %v", p, err)
	}
	if _, err := os.Stat(filepath.Join(s.Target, RepositoryPath(s.RepoID))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preview wrote remote files")
	}
	a, err := e.Backup(ctx, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.State != "pending" || a.Copied != 3 || a.SnapshotID == "" {
		t.Fatalf("first %+v", a)
	}
	write(t, s.Source, "nested/changed.txt", "new")
	write(t, s.Source, "new.txt", "added")
	if err := os.Remove(filepath.Join(s.Source, "removed.txt")); err != nil {
		t.Fatal(err)
	}
	p, err = e.Preview(ctx, s)
	if err != nil || p.New != 1 || p.Changed != 1 || p.Unchanged != 1 || p.Removed != 1 {
		t.Fatalf("preview B: %+v %v", p, err)
	}
	b, err := e.Backup(ctx, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.Copied != 2 || b.Unchanged != 1 || b.State != "pending" {
		t.Fatalf("second %+v", b)
	}
	all, err := e.Snapshots(ctx, s)
	if err != nil || len(all) != 2 {
		t.Fatalf("snapshots: %+v %v", all, err)
	}
	dest := filepath.Join(filepath.Dir(s.Source), "restored")
	r, err := e.Restore(ctx, s, b.SnapshotID, dest, nil)
	if err != nil || r.State != "restored" {
		t.Fatalf("restore: %+v %v", r, err)
	}
	for name, value := range map[string]string{"unchanged.txt": "same", "nested/changed.txt": "new", "new.txt": "added"} {
		actual, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil || digest(string(actual)) != digest(value) {
			t.Fatalf("mismatch %s: %v", name, err)
		}
	}
	marker, err := os.ReadFile(filepath.Join(dest, ".fnos-restore.complete.json"))
	var completed map[string]string
	if err != nil || json.Unmarshal(marker, &completed) != nil || completed["state"] != "restored" || completed["snapshot_id"] != b.SnapshotID {
		t.Fatalf("completed restore marker is inconsistent: %s %v", marker, err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".fnos-restore.partial.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("successful restore left partial marker")
	}
	oldDest := filepath.Join(filepath.Dir(s.Source), "restored-old")
	if _, err = e.Restore(ctx, s, a.SnapshotID, oldDest, nil); err != nil {
		t.Fatal(err)
	}
	bak, err := os.ReadFile(filepath.Join(oldDest, "removed.txt"))
	if err != nil || string(bak) != "keep historical" {
		t.Fatal("deleted source lost historical version")
	}
	if _, err = e.Restore(ctx, s, b.SnapshotID, dest, nil); err == nil {
		t.Fatal("restore reused existing directory")
	}
}

func TestCancelAndMountLossDoNotCommitSnapshots(t *testing.T) {
	for _, reason := range []string{"cancel", "lost", "replace"} {
		t.Run(reason, func(t *testing.T) {
			e, s, p := fixture(t)
			write(t, s.Source, "large", strings.Repeat("x", 1024*1024))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r, err := e.Backup(ctx, s, func(v Progress) {
				if v.Phase == "copying" && v.Bytes > 0 {
					if reason == "cancel" {
						cancel()
					} else if reason == "lost" {
						p.lose()
					} else {
						p.mu.Lock()
						p.items[0].ID++
						p.mu.Unlock()
					}
				}
			})
			if err == nil || r.SnapshotID != "" || r.State == "pending" {
				t.Fatalf("unsafe completion %+v %v", r, err)
			}
			files, _ := filepath.Glob(filepath.Join(s.Target, RepositoryPath(s.RepoID), "snapshots", "*.json"))
			if len(files) != 0 {
				t.Fatal("failed run has committed snapshot")
			}
			actual, err := os.ReadFile(filepath.Join(s.Source, "large"))
			if err != nil || len(actual) != 1024*1024 {
				t.Fatal("source modified")
			}
		})
	}
}

func TestSourceMutationAndSymlinksFailClosed(t *testing.T) {
	for _, kind := range []string{"mutation", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			e, s, _ := fixture(t)
			write(t, s.Source, "file", "original")
			if kind == "symlink" {
				if err := os.Symlink("file", filepath.Join(s.Source, "link")); err != nil {
					t.Fatal(err)
				}
			}
			r, err := e.Backup(context.Background(), s, func(p Progress) {
				if kind == "mutation" && p.Phase == "copying" && p.Bytes > 0 {
					write(t, s.Source, "file", "changed")
				}
			})
			if err == nil || r.State == "pending" {
				t.Fatalf("accepted %s: %+v %v", kind, r, err)
			}
		})
	}
}

func TestCorruptObjectAndUploadFailure(t *testing.T) {
	for _, kind := range []string{"corrupt", "permission"} {
		t.Run(kind, func(t *testing.T) {
			e, s, _ := fixture(t)
			write(t, s.Source, "a", "same")
			if _, err := e.Backup(context.Background(), s, nil); err != nil {
				t.Fatal(err)
			}
			repo := filepath.Join(s.Target, RepositoryPath(s.RepoID))
			if kind == "corrupt" {
				manifests, err := e.Snapshots(context.Background(), s)
				if err != nil {
					t.Fatal(err)
				}
				write(t, repo, "objects/"+manifests[0].Files[0].ObjectID, "wrong")
			} else {
				if os.Geteuid() == 0 {
					t.Skip("permission denial needs unprivileged OS user")
				}
				if err := os.Chmod(filepath.Join(repo, "objects"), 0500); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(filepath.Join(repo, "objects"), 0700)
				write(t, s.Source, "a", "changed")
			}
			r, err := e.Backup(context.Background(), s, nil)
			if err == nil || r.State == "pending" {
				t.Fatalf("accepted %s %+v %v", kind, r, err)
			}
			all, _ := e.Snapshots(context.Background(), s)
			if len(all) != 1 {
				t.Fatal("failure committed extra snapshot")
			}
		})
	}
}

func TestHostileManifestAndRepositoryMarker(t *testing.T) {
	e, s, _ := fixture(t)
	write(t, s.Source, "file", "test")
	r, err := e.Backup(context.Background(), s, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(s.Target, RepositoryPath(s.RepoID), "snapshots", r.SnapshotID+".json")
	original, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../outside", "/absolute", "a/../outside", `a\b`} {
		var m Manifest
		if err := json.Unmarshal(original, &m); err != nil {
			t.Fatal(err)
		}
		m.Files[0].Path = bad
		data, _ := json.Marshal(m)
		if err := os.WriteFile(p, data, 0600); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(filepath.Dir(s.Source), "malicious-restore")
		if _, err := e.Restore(context.Background(), s, r.SnapshotID, dest, nil); err == nil {
			t.Fatalf("hostile path accepted %q", bad)
		}
		if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("created destination before validating manifest")
		}
	}
	if err := os.WriteFile(p, original, 0600); err != nil {
		t.Fatal(err)
	}
	write(t, s.Target, RepositoryPath(s.RepoID)+"/repository.json", `{"schema":99}`)
	if _, err := e.Backup(context.Background(), s, nil); err == nil {
		t.Fatal("foreign marker accepted")
	}
}

func TestOverlapInvalidIDAndMissingSource(t *testing.T) {
	e, s, _ := fixture(t)
	for _, tc := range []Selection{
		{Source: s.Source, Target: s.Target, RepoID: "../escape", Mount: s.Mount},
		{Source: s.Target, Target: s.Target, RepoID: s.RepoID, Mount: s.Mount},
		{Source: "/nonexistent-fixture", Target: s.Target, RepoID: s.RepoID, Mount: s.Mount},
	} {
		if _, err := e.Backup(context.Background(), tc, nil); err == nil {
			t.Fatal("invalid selection accepted")
		}
	}
}
