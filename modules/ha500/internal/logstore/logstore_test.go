package logstore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRotatesReadsBoundedTailAndClears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "toolbox.log")
	store, err := New(path, 256, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for index := 0; index < 20; index++ {
		line := "2026-08-09T18:00:00+08:00 INFO component=fan msg=sample-" + strings.Repeat("x", 30) + "\n"
		if _, err := store.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated log missing: %v", err)
	}
	snapshot, err := store.Read(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Items) != 3 || snapshot.Limit != 3 || snapshot.MaxBytes != 256 || snapshot.BackupCount != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("rotated log still exists after clear: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || len(bytes.TrimSpace(data)) != 0 {
		t.Fatalf("active log after clear = %q, %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("active log permissions = %v, %v", info.Mode().Perm(), err)
	}
}

func TestReadCapsLimitAndTruncatesOversizedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "toolbox.log")
	store, err := New(path, 16<<10, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Write([]byte(strings.Repeat("private-path", 1000) + "\n")); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Read(5000)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Limit != MaxItems || len(snapshot.Items) != 1 || len(snapshot.Items[0].Message) > MaxLineBytes {
		t.Fatalf("bounded snapshot = %+v", snapshot)
	}
}
