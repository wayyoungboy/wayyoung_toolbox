package backup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Explicit opt-in only. Creates one unique app-owned test directory and leaves
// it for inspection. Never scans existing cloud content or production sources.
func TestLiveMountedIncrementalBackup(t *testing.T) {
	point, source := os.Getenv("BACKUP_LIVE_MOUNT"), os.Getenv("BACKUP_LIVE_SOURCE")
	if point == "" || source == "" {
		t.Skip("live mounted tests are opt-in")
	}
	table := SystemMounts{}
	all, err := table.List()
	if err != nil {
		t.Fatal(err)
	}
	m, err := Covering(all, point)
	if err != nil || m.Point != point || m.Source != source || m.Type != "fuse.rclone" {
		t.Fatal("explicit mount identity does not match")
	}
	v, err := Pin(table, m, point)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	name := "backup-center-test-" + NewID()
	if err := v.Check(); err != nil {
		t.Fatal(err)
	}
	if err := v.Root.MkdirNew(name); err != nil {
		t.Fatal(err)
	}
	if err := v.Check(); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	src := filepath.Join(base, "source")
	if err := os.Mkdir(src, 0700); err != nil {
		t.Fatal(err)
	}
	s := Selection{Source: src, Target: filepath.Join(point, name), RepoID: NewID(), Mount: m}
	t.Log("dedicated cloud test directory:", s.Target, "repository:", s.RepoID)
	e := Engine{Mounts: table}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	write(t, src, "unchanged.txt", "persistent test content\n")
	write(t, src, "nested/changed.txt", "version one\n")
	write(t, src, "historical.txt", "preserve after source removal\n")
	a, err := e.Backup(ctx, s, nil)
	if err != nil || a.State != "pending" || a.Copied != 3 {
		t.Fatalf("first backup: %+v %v", a, err)
	}
	write(t, src, "nested/changed.txt", "version two\n")
	write(t, src, "new.txt", "new content\n")
	if err := os.Remove(filepath.Join(src, "historical.txt")); err != nil {
		t.Fatal(err)
	}
	p, err := e.Preview(ctx, s)
	if err != nil || p.New != 1 || p.Changed != 1 || p.Unchanged != 1 || p.Removed != 1 {
		t.Fatalf("incremental preview: %+v %v", p, err)
	}
	b, err := e.Backup(ctx, s, nil)
	if err != nil || b.State != "pending" || b.Copied != 2 || b.Unchanged != 1 {
		t.Fatalf("second backup: %+v %v", b, err)
	}
	for label, snapshot := range map[string]string{"first": a.SnapshotID, "second": b.SnapshotID} {
		dest := filepath.Join(base, "restore-"+label)
		r, err := e.Restore(ctx, s, snapshot, dest, nil)
		if err != nil || r.State != "restored" {
			t.Fatalf("restore %s: %+v %v", label, r, err)
		}
		want := map[string]string{"unchanged.txt": "persistent test content\n", "nested/changed.txt": "version one\n", "historical.txt": "preserve after source removal\n"}
		if label == "second" {
			delete(want, "historical.txt")
			want["nested/changed.txt"] = "version two\n"
			want["new.txt"] = "new content\n"
		}
		for path, content := range want {
			actual, err := os.ReadFile(filepath.Join(dest, path))
			if err != nil || digest(string(actual)) != digest(content) {
				t.Fatalf("SHA-256 mismatch %s/%s", label, path)
			}
		}
	}
	summary, _ := json.Marshal(map[string]any{"first": a, "preview_second": p, "second": b, "restored_versions": 2, "sha256_verified_files": 6, "cloud_durability": "pending"})
	t.Log(string(summary))
}
