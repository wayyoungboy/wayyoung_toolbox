package securefs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rootFixture(t *testing.T) (*Root, string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, err := OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, dir
}

func TestRootRoundTripAndNoClobber(t *testing.T) {
	r, _ := rootFixture(t)
	if err := r.MkdirAll("a/b"); err != nil {
		t.Fatal(err)
	}
	f, err := r.Create("a/b/pending")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(f, "backup"); err != nil {
		t.Fatal(err)
	}
	if err = f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if err = r.RenameNew("a/b/pending", "a/b/object"); err != nil {
		t.Fatal(err)
	}
	data, err := r.ReadFile("a/b/object", 10)
	if err != nil || string(data) != "backup" {
		t.Fatalf("read: %q %v", data, err)
	}
	if _, err := r.Create("a/b/object"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("create clobbers: %v", err)
	}
	f, err = r.Create("a/b/pending")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := r.RenameNew("a/b/pending", "a/b/object"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("rename clobbers: %v", err)
	}
	entries, err := r.ReadDir("a/b")
	if err != nil || len(entries) != 2 {
		t.Fatalf("listing %v %v", entries, err)
	}
	if err := r.Remove("a/b/pending"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadFile("a/b/object", 2); err == nil {
		t.Fatal("oversized read accepted")
	}
}

func TestRootCreateOrTruncateWritesDirectlyWithoutFollowingSymlink(t *testing.T) {
	r, dir := rootFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "movie.mp4"), []byte("old longer data"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := r.CreateOrTruncate("movie.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(f, "new"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "movie.mp4"))
	if err != nil || string(got) != "new" {
		t.Fatalf("direct overwrite: %q %v", got, err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if f, err := r.CreateOrTruncate("link"); err == nil {
		f.Close()
		t.Fatal("symlink target was followed")
	}
}

func TestRootRejectsTraversalAndSymlinks(t *testing.T) {
	r, dir := rootFixture(t)
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "secret"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := r.MkdirAll("inner"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inner", filepath.Join(dir, "inside-link")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../secret", "/etc/passwd", "escape/secret", "inside-link/new", `a\b`, "a/../x", "a//x", ""} {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			if f, err := r.Open(name); err == nil {
				f.Close()
				t.Fatalf("open accepted %q", name)
			}
			if f, err := r.Create(name); err == nil {
				f.Close()
				t.Fatalf("create accepted %q", name)
			}
		})
	}
	if err := r.MkdirAll("escape/created"); err == nil {
		t.Fatal("mkdir followed symlink")
	}
	if _, err := os.Stat(filepath.Join(out, "created")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("outside changed")
	}
	if _, err := OpenRoot(filepath.Join(dir, "inside-link")); err == nil {
		t.Fatal("symlink root accepted")
	}
}

func TestRootHandleSurvivesPathReplacement(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(base, "mounted")
	if err := os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	r, err := OpenRoot(original)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := os.Rename(original, filepath.Join(base, "detached")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := r.Create("probe")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := os.Stat(filepath.Join(original, "probe")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("wrote into fallback directory")
	}
	if _, err := os.Stat(filepath.Join(base, "detached", "probe")); err != nil {
		t.Fatal(err)
	}
}

func TestRootPermissionFailureAndNonRegular(t *testing.T) {
	r, dir := rootFixture(t)
	if err := r.MkdirAll("readonly"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "readonly"), 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(dir, "readonly"), 0700) })
	if os.Geteuid() != 0 {
		if f, err := r.Create("readonly/x"); err == nil {
			f.Close()
			t.Fatal("permission failure ignored")
		}
	}
	if _, err := r.ReadFile("readonly", 100); err == nil {
		t.Fatal("directory accepted as file")
	}
	if err := r.RenameNew("../bad", "ok"); err == nil {
		t.Fatal("rename traversal accepted")
	}
	if err := r.Remove("../bad"); err == nil {
		t.Fatal("remove traversal accepted")
	}
}

func TestSubRootAndExclusiveDirectories(t *testing.T) {
	r, _ := rootFixture(t)
	if err := r.MkdirNew("child"); err != nil {
		t.Fatal(err)
	}
	if err := r.MkdirNew("child"); !errors.Is(err, os.ErrExist) {
		t.Fatal("mkdir clobbered")
	}
	child, err := r.SubRoot("child")
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	if _, err := child.File().Stat(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SubRoot("missing"); err == nil {
		t.Fatal("opened missing dir")
	}
	if err := r.MkdirNew("../outside"); err == nil {
		t.Fatal("mkdir escaped root")
	}
	if err := ValidPath("ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadFile("missing", 10); err == nil {
		t.Fatal("read missing file")
	}
	if _, err := r.ReadDir("missing"); err == nil {
		t.Fatal("list missing dir")
	}
	if err := r.MkdirAll("."); err != nil {
		t.Fatal(err)
	}
}
