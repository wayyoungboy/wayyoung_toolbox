package backup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMissingMountStopsBeforeSourceScan(t *testing.T) {
	e, s, p := fixture(t)
	write(t, s.Source, "file", "data")
	p.lose()
	scanned := false
	_, err := e.Backup(context.Background(), s, func(Progress) { scanned = true })
	if err == nil || scanned {
		t.Fatal("backup scanned sources despite missing target mount")
	}
}

func TestManifestStructuralValidation(t *testing.T) {
	base := Manifest{Schema: Schema, ID: NewID(), RepoID: NewID(), Created: time.Now().UTC(), Files: []Entry{{Path: "f", Size: 1, SHA256: digest("x"), ObjectID: digest("x") + "." + NewID()}}}
	for _, mutate := range []func(*Manifest){
		func(m *Manifest) { m.Files = append(m.Files, m.Files[0]) },
		func(m *Manifest) { m.Files[0].Path = "dir/f" },
		func(m *Manifest) { m.Files[0].SHA256 = "../../bad" },
		func(m *Manifest) { m.Files[0].Size = -1 },
		func(m *Manifest) { m.Directories = []string{"f"} },
		func(m *Manifest) { m.Directories = []string{"dir", "dir"} },
		func(m *Manifest) { m.Directories = []string{"../dir"} },
		func(m *Manifest) { m.Files = append(m.Files, Entry{Path: "f/nested", Size: 1, SHA256: digest("x")}) },
	} {
		b, _ := json.Marshal(base)
		var m Manifest
		json.Unmarshal(b, &m)
		mutate(&m)
		m.Digest = m.checksum()
		if err := m.Validate(base.RepoID); err == nil {
			t.Fatalf("accepted %+v", m)
		}
	}
}

func TestRestoreCancelCorruptionAndReadOnlyImport(t *testing.T) {
	for _, kind := range []string{"cancel", "corrupt", "import"} {
		t.Run(kind, func(t *testing.T) {
			e, s, _ := fixture(t)
			write(t, s.Source, "file", strings.Repeat("a", 1024))
			b, err := e.Backup(context.Background(), s, nil)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "corrupt" {
				all, err := e.Snapshots(context.Background(), s)
				if err != nil {
					t.Fatal(err)
				}
				write(t, s.Target, RepositoryPath(s.RepoID)+"/objects/"+all[0].Files[0].ObjectID, "bad")
			}
			if kind == "import" {
				s.Source = ""
			}
			dest := filepath.Join(filepath.Dir(s.Target), "restore-test")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r, err := e.Restore(ctx, s, b.SnapshotID, dest, func(p Progress) {
				if kind == "cancel" && p.Bytes > 0 {
					cancel()
				}
			})
			if kind == "import" {
				if err != nil || r.State != "restored" {
					t.Fatalf("import restore %+v %v", r, err)
				}
				return
			}
			if err == nil || r.State == "restored" {
				t.Fatal("failed restore marked complete")
			}
			if _, err := os.Stat(filepath.Join(dest, ".fnos-restore.complete.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("complete marker on partial restore")
			}
			if _, err := os.Stat(filepath.Join(dest, ".fnos-restore.partial.json")); err != nil {
				t.Fatal("missing partial marker")
			}
		})
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("read failure") }

type zeroReader struct{}

func (zeroReader) Read([]byte) (int, error) { return 0, nil }

type shortWriter struct{}

func (shortWriter) Write(b []byte) (int, error) { return len(b) - 1, nil }

func TestStreamErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := stream(ctx, io.Discard, strings.NewReader("x"), nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := stream(context.Background(), io.Discard, brokenReader{}, nil); err == nil {
		t.Fatal("reader failure ignored")
	}
	if _, err := stream(context.Background(), io.Discard, zeroReader{}, nil); !errors.Is(err, io.ErrNoProgress) {
		t.Fatal(err)
	}
	if _, err := stream(context.Background(), shortWriter{}, strings.NewReader("x"), nil); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
}

func TestEmptyBackupAndInvalidRestoreTargets(t *testing.T) {
	e, s, _ := fixture(t)
	if err := os.Mkdir(filepath.Join(s.Source, "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	r, err := e.Backup(context.Background(), s, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, dest := range []string{"relative", "/", s.Source, s.Target, filepath.Dir(s.Target)} {
		if _, err := e.Restore(context.Background(), s, r.SnapshotID, dest, nil); err == nil {
			t.Fatalf("restore accepted %q", dest)
		}
	}
	dest := filepath.Join(filepath.Dir(s.Source), "empty-restore")
	if _, err := e.Restore(context.Background(), s, r.SnapshotID, dest, nil); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(dest, "empty")); err != nil || !st.IsDir() {
		t.Fatal("empty directory lost")
	}
}
