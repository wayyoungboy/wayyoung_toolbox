package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"fnos-backup-center/internal/securefs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSealCreatedBeforeCancelOrAmbiguousReply(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprint(ambiguous), func(t *testing.T) {
			e, s, _ := fixture(t)
			write(t, s.Source, "a", "one complete object")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			e.commitDir = func(root *securefs.Root, name string) error {
				calls++
				if err := root.MkdirNew(name); err != nil {
					return err
				}
				cancel()
				if ambiguous {
					return errors.New("simulated lost syscall reply")
				}
				return nil
			}
			result, err := e.Backup(ctx, s, nil)
			want := "pending"
			if ambiguous {
				want = "publication_unknown"
			}
			if err != nil || result.State != want || result.SnapshotID == "" || calls != 1 {
				t.Fatalf("wrong post-seal outcome: %+v %v calls=%d", result, err, calls)
			}
			versions, err := e.Snapshots(context.Background(), s)
			if err != nil || len(versions) != 1 || versions[0].ID != result.SnapshotID {
				t.Fatalf("committed version lost: %+v %v", versions, err)
			}
		})
	}
}

func TestUnsealedManifestIsNeverListedOrRestored(t *testing.T) {
	e, s, _ := fixture(t)
	r, err := e.repository(context.Background(), s, true)
	if err != nil {
		t.Fatal(err)
	}
	defer r.v.Close()
	m := Manifest{Schema: Schema, ID: NewID(), RepoID: s.RepoID, Created: time.Now().UTC(), Files: []Entry{}, Directories: []string{}}
	m.Digest = m.checksum()
	b, _ := json.Marshal(m)
	if err := r.writeMetadata(context.Background(), "snapshots/"+m.ID+".json", b); err != nil {
		t.Fatal(err)
	}
	all, err := e.Snapshots(context.Background(), s)
	if err != nil || len(all) != 0 {
		t.Fatalf("unsealed metadata visible: %+v %v", all, err)
	}
	if _, err := e.Restore(context.Background(), s, m.ID, filepath.Join(filepath.Dir(s.Source), "unsealed-restore"), nil); err == nil {
		t.Fatal("unsealed metadata was restorable")
	}
}

func TestCommitBindsRawManifestBytes(t *testing.T) {
	e, s, _ := fixture(t)
	write(t, s.Source, "a", "original")
	b, err := e.Backup(context.Background(), s, nil)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(s.Target, RepositoryPath(s.RepoID), "snapshots", b.SnapshotID+".json")
	original, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	// Same decoded JSON and embedded digest, different actual stored bytes.
	if err := os.WriteFile(name, append(original, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Snapshots(context.Background(), s); err == nil {
		t.Fatal("seal did not bind exact bytes")
	}
}

func TestOrphanObjectDoesNotBlockRetryOrGetReused(t *testing.T) {
	e, s, _ := fixture(t)
	r, err := e.repository(context.Background(), s, true)
	if err != nil {
		t.Fatal(err)
	}
	r.v.Close()
	orphan := RepositoryPath(s.RepoID) + "/objects/" + digest("content") + "." + NewID()
	write(t, s.Target, orphan, "partial")
	write(t, s.Source, "a", "content")
	result, err := e.Backup(context.Background(), s, nil)
	if err != nil || result.State != "pending" {
		t.Fatalf("orphan blocked new backup: %+v %v", result, err)
	}
	all, err := e.Snapshots(context.Background(), s)
	if err != nil || len(all) != 1 {
		t.Fatal(err)
	}
	entry := all[0].Files[0]
	if !strings.HasPrefix(entry.ObjectID, entry.SHA256+".") || len(entry.ObjectID) != 97 {
		t.Fatal("object lacks unique validated instance", entry)
	}
	if strings.HasSuffix(orphan, entry.ObjectID) {
		t.Fatal("uncommitted orphan reused")
	}
	data, err := os.ReadFile(filepath.Join(s.Target, orphan))
	if err != nil || string(data) != "partial" {
		t.Fatal("orphan was modified or removed")
	}
}

func TestPublicationErrorIsUnknownAndPreservesID(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires unprivileged permission enforcement")
	}
	e, s, _ := fixture(t)
	r, err := e.repository(context.Background(), s, true)
	if err != nil {
		t.Fatal(err)
	}
	r.v.Close()
	p := filepath.Join(s.Target, RepositoryPath(s.RepoID), "commits")
	if err := os.Chmod(p, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(p, 0700)
	result, err := e.Backup(context.Background(), s, nil)
	if err != nil || result.State != "publication_unknown" || result.SnapshotID == "" {
		t.Fatalf("ambiguous publication reported final success/failure: %+v %v", result, err)
	}
	all, err := e.Snapshots(context.Background(), s)
	if err != nil || len(all) != 0 {
		t.Fatal("unpublished snapshot visible", err)
	}
}

func TestMetadataNeverOverwritesExistingPath(t *testing.T) {
	e, s, _ := fixture(t)
	r, err := e.repository(context.Background(), s, true)
	if err != nil {
		t.Fatal(err)
	}
	defer r.v.Close()
	if err := r.writeMetadata(context.Background(), "repository.json", []byte("replace")); !errors.Is(err, os.ErrExist) {
		t.Fatal("existing path not rejected", err)
	}
	data, err := os.ReadFile(filepath.Join(s.Target, RepositoryPath(s.RepoID), "repository.json"))
	if err != nil || string(data) == "replace" {
		t.Fatal("existing data overwritten")
	}
}
