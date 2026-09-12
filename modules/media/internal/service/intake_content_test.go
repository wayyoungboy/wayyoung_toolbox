package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIntakeCanceledBeforeCapturePreservesBothFiles(t *testing.T) {
	root := t.TempDir()
	source, destination := filepath.Join(root, "source"), filepath.Join(root, "destination")
	files := map[string]string{"movie.mp4": "same"}
	writeIntakeFixture(t, source, files)
	writeIntakeFixture(t, destination, files)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := moveIntakeCandidateContext(ctx, IntakeCandidate{Source: filepath.Join(source, "movie.mp4"), Destination: filepath.Join(destination, "movie.mp4")}, source, destination)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	assertIntakeFixture(t, source, files)
	assertIntakeFixture(t, destination, files)
}

func TestIntakeRejectsSourceSymlink(t *testing.T) {
	root := t.TempDir()
	source, destination := filepath.Join(root, "source"), filepath.Join(root, "destination")
	writeIntakeFixture(t, source, map[string]string{"actual.mp4": "video"})
	writeIntakeFixture(t, destination, map[string]string{"actual.mp4": "video"})
	if err := os.Symlink("actual.mp4", filepath.Join(source, "movie.mp4")); err != nil {
		t.Fatal(err)
	}
	_, err := moveIntakeCandidate(IntakeCandidate{Source: filepath.Join(source, "movie.mp4"), Destination: filepath.Join(destination, "movie.mp4")}, source, destination)
	if err == nil {
		t.Fatal("source symlink accepted")
	}
	assertIntakeFixture(t, source, map[string]string{"actual.mp4": "video"})
}

func TestIntakeCollisionRequiresFullContentEquality(t *testing.T) {
	for _, tc := range []struct {
		name                string
		source, destination map[string]string
		identical           bool
	}{
		{"different_same_size", map[string]string{"movie.mp4": "left"}, map[string]string{"movie.mp4": "rite"}, false},
		{"empty_destination_directory", map[string]string{"bundle/movie.mp4": "video"}, map[string]string{"bundle/": ""}, false},
		{"different_nested_files", map[string]string{"bundle/sub/movie.mp4": "left"}, map[string]string{"bundle/sub/movie.mp4": "rite"}, false},
		{"source_extra_file", map[string]string{"bundle/movie.mp4": "video", "bundle/subtitle.srt": "text"}, map[string]string{"bundle/movie.mp4": "video"}, false},
		{"destination_extra_file", map[string]string{"bundle/movie.mp4": "video"}, map[string]string{"bundle/movie.mp4": "video", "bundle/subtitle.srt": "text"}, false},
		{"identical_file", map[string]string{"movie.mp4": "video"}, map[string]string{"movie.mp4": "video"}, true},
		{"identical_directory", map[string]string{"bundle/sub/movie.mp4": "video", "bundle/subtitle.srt": "text"}, map[string]string{"bundle/sub/movie.mp4": "video", "bundle/subtitle.srt": "text"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			source, destination := filepath.Join(root, "source"), filepath.Join(root, "destination")
			for base, files := range map[string]map[string]string{source: tc.source, destination: tc.destination} {
				writeIntakeFixture(t, base, files)
			}
			name := "movie.mp4"
			if _, ok := tc.source[name]; !ok {
				name = "bundle"
			}
			candidate := IntakeCandidate{Source: filepath.Join(source, name), Destination: filepath.Join(destination, name)}
			outcome, err := moveIntakeCandidate(candidate, source, destination)
			if tc.identical {
				if err != nil || outcome != "duplicate_removed" {
					t.Fatalf("outcome=%q error=%v", outcome, err)
				}
				if _, err := os.Lstat(candidate.Source); !os.IsNotExist(err) {
					t.Fatalf("verified duplicate remained: %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("different content was removed: outcome=%q", outcome)
				}
				assertIntakeFixture(t, source, tc.source)
			}
			assertIntakeFixture(t, destination, tc.destination)
		})
	}
}

func TestIntakeCollisionRejectsNestedSymlinks(t *testing.T) {
	root := t.TempDir()
	source, destination := filepath.Join(root, "source"), filepath.Join(root, "destination")
	files := map[string]string{"bundle/movie.mp4": "video"}
	writeIntakeFixture(t, source, files)
	writeIntakeFixture(t, destination, files)
	outside := filepath.Join(root, "outside")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{source, destination} {
		if err := os.Symlink(outside, filepath.Join(base, "bundle", "link")); err != nil {
			t.Fatal(err)
		}
	}
	_, err := moveIntakeCandidate(IntakeCandidate{Source: filepath.Join(source, "bundle"), Destination: filepath.Join(destination, "bundle")}, source, destination)
	if err == nil {
		t.Error("symlink-containing duplicate must require manual review")
	}
	assertIntakeFixture(t, source, files)
	assertIntakeFixture(t, destination, files)
	if b, err := os.ReadFile(outside); err != nil || string(b) != "private" {
		t.Fatalf("outside changed: %q %v", b, err)
	}
}

func writeIntakeFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, name)
		if name[len(name)-1] == '/' {
			if err := os.MkdirAll(path, 0700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func assertIntakeFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, name)
		if name[len(name)-1] == '/' {
			if info, err := os.Stat(path); err != nil || !info.IsDir() {
				t.Errorf("missing directory %s: %v", name, err)
			}
			continue
		}
		if b, err := os.ReadFile(path); err != nil || string(b) != content {
			t.Errorf("changed file %s: %q %v", name, b, err)
		}
	}
}
