package service

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	javspengine "github.com/yuukiy/javsp-go/engine"
)

func TestIntakeDiscoveryRetainsRegisteredPendingRootAfterConfigChange(t *testing.T) {
	f := newIntakeQueueFixture(t)
	cfg := configureIntakeJavsp(t, f)
	cfg.OutputDirectory, cfg.OutputFolderPattern = t.TempDir(), "{num}"
	if _, err := f.s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	oldRoot, err := canonicalDirectory(filepath.Dir(f.target))
	if err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(oldRoot, "TEST-001.mp4")
	writeIntakeFixture(t, oldRoot, map[string]string{"TEST-001.mp4": "video", "TEST-002.mp4": "unrelated"})
	parent := MediaJob{ID: 123, Kind: "torrent", Phase: "qb_cleanup", IntakeTargetRoot: oldRoot, TargetPath: video, PlannedPaths: []string{video}}
	paths, err := f.s.db.MediaPathSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	paths.PendingDirectory = t.TempDir()
	writeIntakeFixture(t, paths.PendingDirectory, map[string]string{"TEST-003.mp4": "new directory"})
	if _, err := f.s.db.UpdateMediaPathSettings(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	restartIntakeQueueFixture(t, f)
	f.s.discoverMovies = func(ctx context.Context, options javspengine.Options) (javspengine.DiscoveryResult, error) {
		if options.InputDirectory != oldRoot || !reflect.DeepEqual(options.InputFiles, []string{video}) {
			t.Errorf("handoff changed registered paths: %+v", options.InputFiles)
		}
		return javspengine.Discover(ctx, options)
	}
	children, err := f.s.discoverIntakeMovies(context.Background(), parent)
	if err != nil || len(children) != 1 {
		t.Fatalf("registered handoff lost its movie: %+v %v", children, err)
	}
	if children[0].MovieSourceRoot != oldRoot || children[0].SourcePath != video || children[0].ParentID != parent.ID {
		t.Fatalf("child lost its original source boundary: %+v", children[0])
	}
	current, err := f.s.db.JavspConfig()
	newRoot, rootErr := canonicalDirectory(paths.PendingDirectory)
	if err != nil || rootErr != nil || current.InputDirectory != newRoot {
		t.Fatal("old task rewrote current settings")
	}
	assertIntakeFixture(t, oldRoot, map[string]string{"TEST-001.mp4": "video", "TEST-002.mp4": "unrelated"})
}

func TestIntakeDiscoveryRejectsMissingOrEscapedRegisteredRoot(t *testing.T) {
	for _, scenario := range []string{"missing", "outside", "root"} {
		t.Run(scenario, func(t *testing.T) {
			f := newIntakeQueueFixture(t)
			cfg := configureIntakeJavsp(t, f)
			writeIntakeFixture(t, cfg.InputDirectory, map[string]string{"TEST-001.mp4": "video"})
			parent := MediaJob{IntakeTargetRoot: cfg.InputDirectory, TargetPath: f.target, PlannedPaths: []string{f.target}}
			switch scenario {
			case "missing":
				parent.IntakeTargetRoot = ""
			case "outside":
				parent.PlannedPaths = []string{f.source}
			case "root":
				parent.IntakeTargetRoot = "/"
			}
			called := false
			f.s.discoverMovies = func(context.Context, javspengine.Options) (javspengine.DiscoveryResult, error) {
				called = true
				return javspengine.DiscoveryResult{}, nil
			}
			if _, err := f.s.discoverIntakeMovies(context.Background(), parent); !errors.Is(err, errIntakeConflict) || called {
				t.Fatalf("unsafe handoff reached scanner: called=%v err=%v", called, err)
			}
		})
	}
}
