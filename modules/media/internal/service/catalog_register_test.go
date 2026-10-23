package service

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

func registrationRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func registrationTarget(t *testing.T, filename string) catalog.RegistrationTarget {
	t.Helper()
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	return catalog.RegistrationTarget{Path: filename, Identity: intakeFileIdentity(info), Size: info.Size(), ModifiedNS: info.ModTime().UnixNano()}
}

func TestCatalogRegistrationTargetsOnlyOutputsAndNeverSweeps(t *testing.T) {
	s := testService(t)
	root := registrationRoot(t)
	writeIntakeFixture(t, filepath.Join(root, "existing"), map[string]string{"TEST-001.mp4": "old video", "TEST-001.nfo": "<movie><title>Old title</title></movie>"})
	lib := catalogLibrary(t, s, root)
	catalogScan(t, s, lib)
	before, err := s.db.catalog.Libraries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// An unrelated subtree has disappeared since the last full scan. Registering
	// a new output must not inventory or mark anything in that subtree missing.
	if err := os.Rename(filepath.Join(root, "existing"), filepath.Join(registrationRoot(t), "offline")); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "new", "TEST-002.mp4")
	writeIntakeFixture(t, filepath.Dir(output), map[string]string{"TEST-002.mp4": "new video", "TEST-002.nfo": "<movie><title>New title</title><actor><name>New actor</name></actor></movie>"})
	result, err := s.db.catalog.RegisterFiles(context.Background(), []catalog.RegistrationTarget{registrationTarget(t, output)}, nil)
	if err != nil || result.Assets != 1 || result.Works != 1 || !reflect.DeepEqual(result.LibraryIDs, []int64{lib.ID}) {
		t.Fatalf("registration=%+v %v", result, err)
	}
	works := catalogWorks(t, s, "")
	if len(works) != 2 {
		t.Fatalf("unrelated inventory changed: %+v", works)
	}
	for _, work := range works {
		detail := catalogDetail(t, s, work.ID)
		if len(detail.Assets) != 1 || !detail.Assets[0].Available {
			t.Fatalf("partial update performed a missing sweep: %+v", detail)
		}
	}
	after, err := s.db.catalog.Libraries(context.Background())
	if err != nil || before[0].ScannedAt != after[0].ScannedAt {
		t.Fatalf("targeted registration claimed a full scan: before=%+v after=%+v %v", before, after, err)
	}
	if matches := catalogWorks(t, s, "query=New+actor"); len(matches) != 1 || matches[0].Title != "New title" {
		t.Fatalf("new metadata/actor not searchable: %+v", matches)
	}
	if _, err := s.db.catalog.RegisterFiles(context.Background(), []catalog.RegistrationTarget{registrationTarget(t, output)}, nil); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.db.QueryRow("SELECT count(*) FROM catalog_assets").Scan(&count); err != nil || count != 2 {
		t.Fatalf("retry duplicated assets: %d %v", count, err)
	}
}

func TestCatalogRegistrationRejectsUnprovenTargetsAtomically(t *testing.T) {
	for _, kind := range []string{"changed", "symlink", "outside", "relative", "parent", "non-video", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			s := testService(t)
			root := registrationRoot(t)
			catalogLibrary(t, s, root)
			writeIntakeFixture(t, root, map[string]string{"TEST-001.mp4": "first", "TEST-002.mp4": "second", "TEST-003.nfo": "<movie/>"})
			good := registrationTarget(t, filepath.Join(root, "TEST-001.mp4"))
			bad := registrationTarget(t, filepath.Join(root, "TEST-002.mp4"))
			switch kind {
			case "changed":
				catalogWrite(t, root, "TEST-002.mp4", "replacement contents")
			case "symlink":
				if err := os.Symlink(good.Path, filepath.Join(root, "link.mp4")); err != nil {
					t.Fatal(err)
				}
				bad.Path = filepath.Join(root, "link.mp4")
			case "outside":
				other := registrationRoot(t)
				catalogWrite(t, other, "TEST-002.mp4", "outside")
				bad = registrationTarget(t, filepath.Join(other, "TEST-002.mp4"))
			case "relative":
				bad.Path = "TEST-002.mp4"
			case "parent":
				bad.Path = root + "/../" + filepath.Base(root) + "/TEST-002.mp4"
			case "non-video":
				bad = registrationTarget(t, filepath.Join(root, "TEST-003.nfo"))
			case "duplicate":
				bad = good
			}
			if result, err := s.db.catalog.RegisterFiles(context.Background(), []catalog.RegistrationTarget{good, bad}, nil); err == nil {
				t.Fatalf("unproven %s accepted: %+v", kind, result)
			}
			if works := catalogWorks(t, s, ""); len(works) != 0 {
				t.Fatalf("failed batch published files: %+v", works)
			}
		})
	}
}

func TestCatalogRegistrationSharesAtomicCompletionTransaction(t *testing.T) {
	s := testService(t)
	root := registrationRoot(t)
	catalogLibrary(t, s, root)
	catalogWrite(t, root, "TEST-001.mp4", "video")
	finalErr := errors.New("stale producer attempt")
	called := false
	_, err := s.db.catalog.RegisterFiles(context.Background(), []catalog.RegistrationTarget{registrationTarget(t, filepath.Join(root, "TEST-001.mp4"))}, func(tx *sql.Tx, result catalog.RegistrationResult) error {
		called = true
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM catalog_assets").Scan(&count); err != nil || count != 1 || result.Assets != 1 {
			t.Fatalf("not inside publication transaction: %d %+v %v", count, result, err)
		}
		return finalErr
	})
	if !called || !errors.Is(err, finalErr) {
		t.Fatalf("finalizer=%t %v", called, err)
	}
	if works := catalogWorks(t, s, ""); len(works) != 0 {
		t.Fatalf("completion failure did not roll back: %+v", works)
	}
}

func TestCatalogRegistrationMoveProofKeepsAssetProgressAndPersonalState(t *testing.T) {
	s := testService(t)
	sourceRoot, targetRoot := registrationRoot(t), registrationRoot(t)
	source := filepath.Join(sourceRoot, "TEST-001.mp4")
	catalogWrite(t, sourceRoot, "TEST-001.mp4", "original video")
	oldLib := catalogLibrary(t, s, sourceRoot)
	newLib := catalogLibrary(t, s, targetRoot)
	catalogScan(t, s, oldLib)
	work := catalogDetail(t, s, catalogWorks(t, s, "")[0].ID)
	assetID := work.Assets[0].ID
	if _, err := s.db.db.Exec("INSERT INTO catalog_progress(asset_id,position,duration,revision,completed,played_at) VALUES(?,12,120,1,0,'2026-01-01T00:00:00Z')", assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.db.Exec("INSERT INTO catalog_personal(work_id,favorite,watch_later) VALUES(?,1,1)", work.ID); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(targetRoot, "TEST-001-C.mp4")
	proof := registrationTarget(t, source)
	proof.Path, proof.PreviousPath = target, source
	if err := os.Rename(source, target); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.db.catalog.RegisterFiles(context.Background(), []catalog.RegistrationTarget{proof}, nil); err != nil {
			t.Fatal(err)
		}
	}
	detail, err := s.db.catalog.Work(context.Background(), work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Assets) != 1 || detail.Assets[0].ID != assetID || detail.Assets[0].LibraryID != newLib.ID || detail.Assets[0].Progress.Position != 12 || !detail.Favorite || !detail.WatchLater {
		t.Fatalf("proven move lost intent or duplicated file: %+v", detail)
	}
}

func TestCatalogRegistrationDoesNotFallBackFromChangedSpecificRoot(t *testing.T) {
	s := testService(t)
	parent := registrationRoot(t)
	child := filepath.Join(parent, "specific")
	writeIntakeFixture(t, child, map[string]string{"TEST-001.mp4": "video"})
	catalogLibrary(t, s, parent)
	specific := catalogLibrary(t, s, child)
	target := registrationTarget(t, filepath.Join(child, "TEST-001.mp4"))
	result, err := s.db.catalog.RegisterFiles(context.Background(), []catalog.RegistrationTarget{target}, nil)
	if err != nil || !reflect.DeepEqual(result.LibraryIDs, []int64{specific.ID}) {
		t.Fatalf("wrong root: %+v %v", result, err)
	}
	if err := os.Rename(child, child+"-old"); err != nil {
		t.Fatal(err)
	}
	writeIntakeFixture(t, child, map[string]string{"TEST-002.mp4": "replacement root"})
	if _, err := s.db.catalog.RegisterFiles(context.Background(), []catalog.RegistrationTarget{registrationTarget(t, filepath.Join(child, "TEST-002.mp4"))}, nil); !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("silently fell back to ancestor: %v", err)
	}
	if works := catalogWorks(t, s, ""); len(works) != 1 {
		t.Fatalf("changed root published: %+v", works)
	}
}
