package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func localIndexFixture(t *testing.T) (JavspConfig, IntakeConfig) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := defaultJavspConfig()
	cfg.InputDirectory = filepath.Join(base, "pending")
	cfg.OutputDirectory = filepath.Join(base, "complete")
	intake := defaultIntakeConfig()
	intake.DestinationDirectory = filepath.Join(base, "incoming")
	for _, path := range []string{cfg.InputDirectory, cfg.OutputDirectory, intake.DestinationDirectory} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return cfg, intake
}

func putIndexFile(t *testing.T, root, name, data string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLocalMediaIndexCoversPendingIntakeAndCompleted(t *testing.T) {
	cfg, intake := localIndexFixture(t)
	wants := []struct{ title, source, path string }{
		{"TEST-001", "pending", putIndexFile(t, cfg.InputDirectory, "nested/TEST-001-C.mp4", "video")},
		{"TEST-002", "pending", putIndexFile(t, intake.DestinationDirectory, "TEST-002.mp4", "video")},
		{"FC2-PPV-123456", "completed", putIndexFile(t, cfg.OutputDirectory, "actor/FC2-123456 sample title.MP4", "video")},
	}
	index, err := buildLocalMediaIndex(context.Background(), cfg, intake)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range wants {
		match, err := index.MatchTitle(context.Background(), want.title)
		if err != nil || match == nil || match.Path != want.path || match.Source != want.source || match.CheckedAt == "" {
			t.Fatalf("missing local evidence for %s: %+v %v", want.title, match, err)
		}
	}
}

func TestLocalMediaIndexRejectsNonVideoAndAmbiguousEvidence(t *testing.T) {
	cfg, intake := localIndexFixture(t)
	putIndexFile(t, cfg.InputDirectory, "TEST-001.nfo", "metadata")
	putIndexFile(t, cfg.InputDirectory, "TEST-002.mp4", "")
	putIndexFile(t, cfg.InputDirectory, "TEST-003 TEST-004.mp4", "video")
	putIndexFile(t, cfg.InputDirectory, "TEST-005.mp4.part", "partial")
	putIndexFile(t, cfg.InputDirectory, "TEST-006.mp4", "video")
	out := putIndexFile(t, t.TempDir(), "TEST-007.mp4", "video")
	if err := os.Symlink(out, filepath.Join(cfg.InputDirectory, "TEST-007.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(cfg.InputDirectory, "TEST-008.mp4"), 0o700); err != nil {
		t.Fatal(err)
	}
	index, err := buildLocalMediaIndex(context.Background(), cfg, intake)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"TEST-001", "TEST-002", "TEST-003", "TEST-004", "TEST-005", "TEST-007", "TEST-008", "TEST-006 TEST-009"} {
		if match, err := index.MatchTitle(context.Background(), title); err != nil || match != nil {
			t.Fatalf("false presence for %s: %+v %v", title, match, err)
		}
	}
}

func TestLocalMediaIndexRechecksFilePresenceWithoutScanningAgain(t *testing.T) {
	cfg, intake := localIndexFixture(t)
	path := putIndexFile(t, cfg.InputDirectory, "TEST-001.mp4", "video")
	index, err := buildLocalMediaIndex(context.Background(), cfg, intake)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if match, err := index.MatchTitle(context.Background(), "TEST-001"); err != nil || match != nil {
		t.Fatalf("deleted video still blocks download: %+v %v", match, err)
	}
	// No implicit refresh is allowed during matching.
	putIndexFile(t, cfg.InputDirectory, "TEST-002.mp4", "new video")
	if match, err := index.MatchTitle(context.Background(), "TEST-002"); err != nil || match != nil {
		t.Fatalf("matching unexpectedly scanned the media directory: %+v %v", match, err)
	}
}

func TestLocalMediaIndexCanceledAndUnavailableAreNotEmpty(t *testing.T) {
	cfg, intake := localIndexFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := buildLocalMediaIndex(ctx, cfg, intake); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled scan should stop: %v", err)
	}
	intake.DestinationDirectory = filepath.Join(t.TempDir(), "unavailable")
	if _, err := buildLocalMediaIndex(context.Background(), cfg, intake); err == nil {
		t.Fatal("unavailable configured media root was treated as an empty library")
	}
}

func TestLocalMediaIndexOverlappingRootsKeepCompletedEvidence(t *testing.T) {
	cfg, intake := localIndexFixture(t)
	cfg.OutputDirectory = filepath.Join(cfg.InputDirectory, "complete")
	if err := os.Mkdir(cfg.OutputDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	intake.DestinationDirectory = cfg.InputDirectory
	path := putIndexFile(t, cfg.OutputDirectory, "TEST-001.mp4", "video")
	putIndexFile(t, cfg.InputDirectory, "TEST-002.mp4", "video")
	index, err := buildLocalMediaIndex(context.Background(), cfg, intake)
	if err != nil {
		t.Fatal(err)
	}
	match, err := index.MatchTitle(context.Background(), "TEST-001")
	if err != nil || match == nil || match.Source != "completed" || match.Path != path || len(index.files["TEST-001"]) != 1 || len(index.files["TEST-002"]) != 1 {
		t.Fatalf("overlapping roots duplicated or misclassified evidence: %+v %v %+v", match, err, index.files)
	}
}

func TestLocalMediaIndexChangedCandidatesAreNotTrusted(t *testing.T) {
	for _, change := range []string{"content_changed", "file_link", "ancestor_link", "root_missing", "candidate_removed_with_alternative"} {
		t.Run(change, func(t *testing.T) {
			cfg, intake := localIndexFixture(t)
			path := putIndexFile(t, cfg.InputDirectory, "nested/TEST-001.mp4", "video")
			var alternative string
			if change == "candidate_removed_with_alternative" {
				alternative = putIndexFile(t, intake.DestinationDirectory, "TEST-001-C.mp4", "other video")
			}
			index, err := buildLocalMediaIndex(context.Background(), cfg, intake)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "content_changed":
				if err := os.WriteFile(path, []byte("different video"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "file_link":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				target := putIndexFile(t, t.TempDir(), "outside.mp4", "video")
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "ancestor_link":
				directory := filepath.Dir(path)
				if err := os.Rename(directory, directory+"-moved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(directory+"-moved", directory); err != nil {
					t.Fatal(err)
				}
			case "root_missing":
				if err := os.Rename(cfg.InputDirectory, cfg.InputDirectory+"-moved"); err != nil {
					t.Fatal(err)
				}
			case "candidate_removed_with_alternative":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			match, err := index.MatchTitle(context.Background(), "TEST-001")
			if alternative != "" {
				if err != nil || match == nil || match.Path != alternative {
					t.Fatalf("another existing file was lost: %+v %v", match, err)
				}
			} else if err == nil || match != nil {
				t.Fatalf("changed candidate silently became trusted or absent: %+v %v", match, err)
			}
		})
	}
}

func TestLocalMediaIndexRootReplacementIsNotAnEmptyLibrary(t *testing.T) {
	for _, link := range []bool{false, true} {
		t.Run(map[bool]string{false: "new_directory", true: "redirected_root"}[link], func(t *testing.T) {
			cfg, intake := localIndexFixture(t)
			putIndexFile(t, cfg.InputDirectory, "TEST-001.mp4", "video")
			index, err := buildLocalMediaIndex(context.Background(), cfg, intake)
			if err != nil {
				t.Fatal(err)
			}
			moved := cfg.InputDirectory + "-moved"
			if err := os.Rename(cfg.InputDirectory, moved); err != nil {
				t.Fatal(err)
			}
			if link {
				err = os.Symlink(moved, cfg.InputDirectory)
			} else {
				err = os.Mkdir(cfg.InputDirectory, 0o700)
			}
			if err != nil {
				t.Fatal(err)
			}
			match, err := index.MatchTitle(context.Background(), "TEST-001")
			if err == nil || match != nil {
				t.Fatalf("replaced root trusted as present or absent: %+v %v", match, err)
			}
		})
	}
}
