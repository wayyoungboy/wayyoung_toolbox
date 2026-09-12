package service

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestScanDuplicateVideosGroupsNormalizedMediaID(t *testing.T) {
	root := t.TempDir()
	writeTestVideo := func(relative, content string) string {
		t.Helper()
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	largeContent := strings.Repeat("sample-content", 20000)
	writeTestVideo("actor-a/MIDA-734.mp4", largeContent)
	writeTestVideo("actor-b/MIDA-734-C.mp4", largeContent)
	writeTestVideo("actor-c/MIDA-735.mp4", "same-size-a")
	writeTestVideo("actor-d/MIDA-735-C.mp4", "same-size-b")
	writeTestVideo("actor-e/MIDA-736.mp4", "short")
	writeTestVideo("actor-f/MIDA-736-C.mp4", "different-content")
	writeTestVideo("actor-e/VENX-345.mp4", "single")
	writeTestVideo("@eaDir/MIDA-734-U.mp4", largeContent)
	outside := filepath.Join(t.TempDir(), "MIDA-734-U.mp4")
	if err := os.WriteFile(outside, []byte("same-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.mp4")); err != nil {
		t.Fatal(err)
	}

	result, err := scanDuplicateVideos(root)
	if err != nil {
		t.Fatal(err)
	}
	if result.ScannedVideos != 7 || len(result.Groups) != 3 {
		t.Fatalf("result = %+v", result)
	}
	groups := map[string]DuplicateVideoGroup{}
	for _, group := range result.Groups {
		groups[group.MediaID] = group
	}
	if group := groups["MIDA-734"]; len(group.Files) != 2 || group.MatchType != "fingerprint" || !group.SameSize {
		t.Fatalf("MIDA-734 = %+v", group)
	}
	if group := groups["MIDA-735"]; len(group.Files) != 2 || group.MatchType != "media_id" || !group.SameSize {
		t.Fatalf("MIDA-735 = %+v", group)
	}
	if group := groups["MIDA-736"]; len(group.Files) != 2 || group.MatchType != "media_id" || group.SameSize {
		t.Fatalf("MIDA-736 = %+v", group)
	}
}

func TestPreviewEmptyDirectoriesIsReadOnlyAndRecursive(t *testing.T) {
	root := t.TempDir()
	oldParent := filepath.Join(root, "old-parent")
	oldChild := filepath.Join(oldParent, "old-child")
	recent := filepath.Join(root, "recent")
	nonEmpty := filepath.Join(root, "non-empty")
	protected := filepath.Join(root, ".snapshot", "old-empty")
	for _, directory := range []string{oldChild, recent, nonEmpty, protected} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(nonEmpty, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked-empty")); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, directory := range []string{oldChild, oldParent, protected} {
		if err := os.Chtimes(directory, old, old); err != nil {
			t.Fatal(err)
		}
	}

	result, err := inspectEmptyDirectories(root, 24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := canonicalDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	paths := append([]string(nil), result.Directories...)
	sort.Strings(paths)
	want := []string{filepath.Join(canonicalRoot, "old-parent", "old-child"), filepath.Join(canonicalRoot, "old-parent")}
	sort.Strings(want)
	if len(paths) != len(want) || paths[0] != want[0] || paths[1] != want[1] {
		t.Fatalf("directories = %v, want %v", paths, want)
	}
	for _, directory := range []string{root, oldParent, oldChild, recent, nonEmpty, protected, outside} {
		if info, statErr := os.Stat(directory); statErr != nil || !info.IsDir() {
			t.Fatalf("preview changed %s: %v", directory, statErr)
		}
	}
}

func TestCleanupEmptyDirectoriesNeverRemovesRootRecentNonEmptyOrSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	oldParent := filepath.Join(root, "old-parent")
	oldChild := filepath.Join(oldParent, "old-child")
	recent := filepath.Join(root, "recent")
	nonEmpty := filepath.Join(root, "non-empty")
	for _, directory := range []string{oldChild, recent, nonEmpty} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(nonEmpty, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	link := filepath.Join(root, "linked-empty")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, directory := range []string{oldChild, oldParent} {
		if err := os.Chtimes(directory, old, old); err != nil {
			t.Fatal(err)
		}
	}

	result, err := inspectEmptyDirectories(root, 24*time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 2 || result.Found != 2 {
		t.Fatalf("result = %+v", result)
	}
	for _, removed := range []string{oldChild, oldParent} {
		if _, statErr := os.Stat(removed); !os.IsNotExist(statErr) {
			t.Fatalf("old empty directory still exists: %s (%v)", removed, statErr)
		}
	}
	for _, kept := range []string{root, recent, nonEmpty, outside} {
		if info, statErr := os.Stat(kept); statErr != nil || !info.IsDir() {
			t.Fatalf("protected directory changed %s: %v", kept, statErr)
		}
	}
	if info, statErr := os.Lstat(link); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink changed: %v", statErr)
	}
}

func TestSafeRemoveEmptyDirectoryRefusesReplacedDirectoryAndSymlinkedAncestor(t *testing.T) {
	root, err := canonicalDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	replaced := filepath.Join(root, "replaced")
	if err := os.Mkdir(replaced, 0o700); err != nil {
		t.Fatal(err)
	}
	expected, err := os.Stat(replaced)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(replaced); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(replaced, 0o700); err != nil {
		t.Fatal(err)
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	removed, _, err := safeRemoveEmptyDirectory(root, replaced, expected, rootInfo)
	if err != nil || removed {
		t.Fatalf("replaced directory removed=%v err=%v", removed, err)
	}

	parent := filepath.Join(root, "parent")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	expectedChild, err := os.Stat(child)
	if err != nil {
		t.Fatal(err)
	}
	expectedParent, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(child); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(parent); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	outsideChild := filepath.Join(outside, "child")
	if err := os.Mkdir(outsideChild, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, parent); err != nil {
		t.Fatal(err)
	}
	removed, _, err = safeRemoveEmptyDirectory(root, child, expectedChild, expectedParent)
	if err != nil || removed {
		t.Fatalf("symlinked ancestor removed=%v err=%v", removed, err)
	}
	if _, err := os.Stat(outsideChild); err != nil {
		t.Fatalf("outside directory changed: %v", err)
	}
}

func TestValidateMaintenanceConfigRejectsUnsafeValues(t *testing.T) {
	root := t.TempDir()
	valid := defaultMaintenanceConfig()
	valid.RootDirectory = root
	if _, err := validateMaintenanceConfig(valid); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	for _, config := range []MaintenanceConfig{
		{RootDirectory: "/", ScheduleType: "daily", ScheduleMinutes: 60, ScheduleTime: "05:00", MinimumAgeHours: 24},
		{RootDirectory: root, ScheduleType: "daily", ScheduleMinutes: 60, ScheduleTime: "invalid", MinimumAgeHours: 24},
		{RootDirectory: root, ScheduleType: "interval", ScheduleMinutes: 5, ScheduleTime: "05:00", MinimumAgeHours: 24},
		{RootDirectory: root, ScheduleType: "daily", ScheduleMinutes: 60, ScheduleTime: "05:00", MinimumAgeHours: 0},
	} {
		if _, err := validateMaintenanceConfig(config); err == nil {
			t.Fatalf("unsafe config accepted: %+v", config)
		}
	}
}

func TestDeleteDuplicateVideosPermanentlyDeletesOnlySelectedCopies(t *testing.T) {
	root := t.TempDir()
	left := filepath.Join(root, "MIDA-734.mp4")
	right := filepath.Join(root, "MIDA-734-C.mp4")
	content := strings.Repeat("duplicate-video", 10000)
	for _, path := range []string{left, right} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	scan, err := scanDuplicateVideos(root)
	if err != nil || len(scan.Groups) != 1 {
		t.Fatalf("scan=%+v err=%v", scan, err)
	}
	selected := scan.Groups[0].Files[0]
	result, err := deleteDuplicateVideos(root, []DuplicateDeleteSelection{{
		Path: selected.Path, Size: selected.Size, Fingerprint: selected.Fingerprint,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 1 || result.ReclaimedBytes != selected.Size {
		t.Fatalf("result=%+v", result)
	}
	if _, err := os.Stat(selected.Path); !os.IsNotExist(err) {
		t.Fatalf("selected file still exists: %v", err)
	}
	kept := left
	if selected.Path == left {
		kept = right
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("last copy was changed: %v", err)
	}
}

func TestDeleteDuplicateVideosRejectsAllCopiesChangedAndOutsideFiles(t *testing.T) {
	for _, test := range []struct {
		name      string
		selection func(root string, group DuplicateVideoGroup) []DuplicateDeleteSelection
		mutate    func(group DuplicateVideoGroup) error
	}{
		{
			name: "all copies",
			selection: func(_ string, group DuplicateVideoGroup) []DuplicateDeleteSelection {
				items := make([]DuplicateDeleteSelection, 0, len(group.Files))
				for _, file := range group.Files {
					items = append(items, DuplicateDeleteSelection{Path: file.Path, Size: file.Size, Fingerprint: file.Fingerprint})
				}
				return items
			},
		},
		{
			name: "changed after scan",
			selection: func(_ string, group DuplicateVideoGroup) []DuplicateDeleteSelection {
				file := group.Files[0]
				return []DuplicateDeleteSelection{{Path: file.Path, Size: file.Size, Fingerprint: file.Fingerprint}}
			},
			mutate: func(group DuplicateVideoGroup) error {
				return os.WriteFile(group.Files[0].Path, []byte("changed"), 0o600)
			},
		},
		{
			name: "outside root",
			selection: func(_ string, group DuplicateVideoGroup) []DuplicateDeleteSelection {
				file := group.Files[0]
				file.Path = filepath.Join(filepath.Dir(filepath.Dir(file.Path)), "outside.mp4")
				return []DuplicateDeleteSelection{{Path: file.Path, Size: file.Size, Fingerprint: file.Fingerprint}}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"MIDA-734.mp4", "MIDA-734-C.mp4"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(strings.Repeat("same", 20000)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			scan, err := scanDuplicateVideos(root)
			if err != nil || len(scan.Groups) != 1 {
				t.Fatalf("scan=%+v err=%v", scan, err)
			}
			if test.mutate != nil {
				if err := test.mutate(scan.Groups[0]); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := deleteDuplicateVideos(root, test.selection(root, scan.Groups[0])); err == nil {
				t.Fatal("unsafe deletion was accepted")
			}
		})
	}
}
