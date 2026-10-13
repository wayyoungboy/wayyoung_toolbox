package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDirectCopyPreservesLayoutAndUnrelatedTargetFiles(t *testing.T) {
	e, s, _ := fixture(t)
	s.Mode = "direct"
	write(t, s.Source, "actor/movie/video.mp4", "first version")
	write(t, s.Target, "unrelated/keep.txt", "keep")

	first, err := e.Backup(context.Background(), s, nil)
	if err != nil || first.Copied != 1 || first.SnapshotID != "" {
		t.Fatalf("first direct copy: %+v %v", first, err)
	}
	for name, want := range map[string]string{
		"actor/movie/video.mp4": "first version",
		"unrelated/keep.txt":    "keep",
	} {
		got, err := os.ReadFile(filepath.Join(s.Target, name))
		if err != nil || string(got) != want {
			t.Fatalf("%s: %q %v", name, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(s.Target, RepositoryPath(s.RepoID))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("direct copy created a versioned repository")
	}

	second, err := e.Backup(context.Background(), s, nil)
	if err != nil || second.Copied != 0 || second.Unchanged != 1 {
		t.Fatalf("unchanged direct copy: %+v %v", second, err)
	}
	write(t, s.Source, "actor/movie/video.mp4", "new version!!")
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(s.Source, "actor/movie/video.mp4"), future, future); err != nil {
		t.Fatal(err)
	}
	third, err := e.Backup(context.Background(), s, nil)
	if err != nil || third.Copied != 1 {
		t.Fatalf("changed direct copy: %+v %v", third, err)
	}
	got, err := os.ReadFile(filepath.Join(s.Target, "actor/movie/video.mp4"))
	if err != nil || string(got) != "new version!!" {
		t.Fatalf("changed file not replaced: %q %v", got, err)
	}
	if err := os.Remove(filepath.Join(s.Source, "actor/movie/video.mp4")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Backup(context.Background(), s, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Target, "actor/movie/video.mp4")); err != nil {
		t.Fatal("deleted source removed target file", err)
	}
}

func TestDirectPreviewReadsMetadataWithoutCreatingFiles(t *testing.T) {
	e, s, _ := fixture(t)
	s.Mode = "direct"
	write(t, s.Source, "new/file.mp4", "source")
	write(t, s.Target, "unrelated/keep.txt", "target")
	p, err := e.Preview(context.Background(), s)
	if err != nil || p.New != 1 || p.Files != 1 {
		t.Fatalf("direct preview: %+v %v", p, err)
	}
	if _, err := os.Stat(filepath.Join(s.Target, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preview wrote to target")
	}
}

func TestDirectCopyCancellationKeepsOldTargetFile(t *testing.T) {
	e, s, _ := fixture(t)
	s.Mode = "direct"
	write(t, s.Source, "movie.mp4", string(make([]byte, 4<<20)))
	write(t, s.Target, "movie.mp4", "old good copy")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := e.Backup(ctx, s, func(p Progress) {
		if p.Phase == "copying_direct" && p.Bytes > 0 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(s.Target, "movie.mp4"))
	if err != nil || string(got) != "old good copy" {
		t.Fatalf("existing target was truncated: %q %v", got, err)
	}
}

func TestDirectCopyRejectsSourceSymlink(t *testing.T) {
	e, s, _ := fixture(t)
	s.Mode = ModeDirect
	write(t, s.Source, "real.mp4", "video")
	if err := os.Symlink("real.mp4", filepath.Join(s.Source, "link.mp4")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Backup(context.Background(), s, nil); err == nil {
		t.Fatal("source symlink was followed")
	}
	if _, err := os.Stat(filepath.Join(s.Target, "real.mp4")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed preflight copied data")
	}
}

func TestDirectCopyRejectsTargetSymlink(t *testing.T) {
	e, s, _ := fixture(t)
	s.Mode = ModeDirect
	write(t, s.Source, "movie.mp4", "video")
	outside := filepath.Join(t.TempDir(), "outside.mp4")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.Target, "movie.mp4")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Backup(context.Background(), s, nil); err == nil {
		t.Fatal("target symlink was accepted")
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "keep" {
		t.Fatalf("outside file changed: %q %v", got, err)
	}
}

func TestDirectCopyMountLossKeepsOldTargetFile(t *testing.T) {
	e, s, mounts := fixture(t)
	s.Mode = ModeDirect
	write(t, s.Source, "movie.mp4", string(make([]byte, 4<<20)))
	write(t, s.Target, "movie.mp4", "old good copy")
	_, err := e.Backup(context.Background(), s, func(p Progress) {
		if p.Phase == "copying_direct" && p.Bytes > 0 {
			mounts.lose()
		}
	})
	if !errors.Is(err, ErrMountLost) {
		t.Fatalf("mount loss: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(s.Target, "movie.mp4"))
	if err != nil || string(got) != "old good copy" {
		t.Fatalf("mount loss changed existing target: %q %v", got, err)
	}
}
