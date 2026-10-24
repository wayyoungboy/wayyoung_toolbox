package service

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

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

func TestCatalogRegistrationRejectsAmbiguousMoveReceipts(t *testing.T) {
	for _, mode := range []string{"existing-target", "legacy-target", "reused-source"} {
		t.Run(mode, func(t *testing.T) {
			s := testService(t)
			root := registrationRoot(t)
			source, target := filepath.Join(root, "TEST-001.mp4"), filepath.Join(root, "TEST-001-C.mp4")
			catalogWrite(t, root, "TEST-001.mp4", "video")
			lib := catalogLibrary(t, s, root)
			if mode != "reused-source" {
				if err := os.Link(source, target); err != nil {
					t.Fatal(err)
				}
			}
			catalogScan(t, s, lib)
			if mode == "legacy-target" {
				if _, err := s.db.db.Exec("UPDATE catalog_assets SET file_identity='' WHERE library_id=? AND path='TEST-001-C.mp4'", lib.ID); err != nil {
					t.Fatal(err)
				}
			}
			proof := registrationTarget(t, source)
			proof.Path, proof.PreviousPath = target, source
			if mode != "reused-source" {
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Rename(source, target); err != nil {
				t.Fatal(err)
			}
			targets := []catalog.RegistrationTarget{proof}
			if mode == "reused-source" {
				other := proof
				other.Path = filepath.Join(root, "TEST-001-CD2.mp4")
				if err := os.Link(target, other.Path); err != nil {
					t.Fatal(err)
				}
				targets = append(targets, other)
			}
			before, err := s.db.catalog.Work(context.Background(), catalogWorks(t, s, "")[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.catalog.RegisterFiles(context.Background(), targets, nil); !errors.Is(err, catalog.ErrConflict) {
				t.Fatalf("ambiguous move %s published: %v", mode, err)
			}
			after, err := s.db.catalog.Work(context.Background(), before.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("conflicting proof changed catalog: %v", err)
			}
		})
	}
}

func TestCatalogRegistrationCrossLibraryMetadataConflictIsDeterministic(t *testing.T) {
	s := testService(t)
	first, second := registrationRoot(t), registrationRoot(t)
	for index, root := range []string{first, second} {
		catalogLibrary(t, s, root)
		catalogWrite(t, root, "TEST-001.mp4", "video")
		title := []string{"Alpha title", "Beta title"}[index]
		catalogWrite(t, root, "TEST-001.nfo", "<movie><title>"+title+"</title></movie>")
	}
	targets := []catalog.RegistrationTarget{registrationTarget(t, filepath.Join(first, "TEST-001.mp4")), registrationTarget(t, filepath.Join(second, "TEST-001.mp4"))}
	for i := 0; i < 3; i++ {
		if _, err := s.db.catalog.RegisterFiles(context.Background(), targets, nil); err != nil {
			t.Fatal(err)
		}
		works := catalogWorks(t, s, "")
		if len(works) != 1 || works[0].Title != "Alpha title" {
			t.Fatalf("non-deterministic metadata selection: %+v", works)
		}
		detail := catalogDetail(t, s, works[0].ID)
		conflicts := 0
		for _, asset := range detail.Assets {
			for _, issue := range asset.Issues {
				if issue == "metadata_conflict" {
					conflicts++
				}
			}
		}
		if conflicts == 0 {
			t.Fatal("cross-library metadata conflict was hidden")
		}
		targets[0], targets[1] = targets[1], targets[0]
	}
}

func TestCatalogRegistrationRetiresOnlyReplacedTargetGeneration(t *testing.T) {
	s := testService(t)
	root := registrationRoot(t)
	catalogWrite(t, root, "TEST-001.mp4", "original video")
	catalogWrite(t, root, "TEST-002.mp4", "unrelated")
	lib := catalogLibrary(t, s, root)
	catalogScan(t, s, lib)
	old := catalogDetail(t, s, catalogWorks(t, s, "query=TEST-001")[0].ID).Assets[0]
	// A new inode at the same path is a new physical generation, not the
	// organizer-proven move case. Keep old history, but never call it playable.
	replacement := filepath.Join(root, "replacement.tmp")
	if err := os.WriteFile(replacement, []byte("replacement video"), 0o600); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(root, "TEST-001.mp4")
	if err := os.Rename(replacement, filename); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.catalog.RegisterFiles(context.Background(), []catalog.RegistrationTarget{registrationTarget(t, filename)}, nil); err != nil {
		t.Fatal(err)
	}
	detail := catalogDetail(t, s, catalogWorks(t, s, "query=TEST-001")[0].ID)
	if len(detail.Assets) != 2 {
		t.Fatalf("old generation was deleted: %+v", detail)
	}
	for _, asset := range detail.Assets {
		if asset.Available == (asset.ID == old.ID) {
			t.Fatalf("wrong active generation: %+v", detail.Assets)
		}
	}
	other := catalogDetail(t, s, catalogWorks(t, s, "query=TEST-002")[0].ID)
	if len(other.Assets) != 1 || !other.Assets[0].Available {
		t.Fatalf("unrelated path retired: %+v", other)
	}
}

func TestCatalogRegistrationSerializesNewLibraryRootSelection(t *testing.T) {
	s := testService(t)
	root := registrationRoot(t)
	child := filepath.Join(root, "nested")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	catalogWrite(t, child, "TEST-001.mp4", "video")
	catalogLibrary(t, s, root)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	target := registrationTarget(t, filepath.Join(child, "TEST-001.mp4"))
	go func() {
		_, err := s.db.catalog.RegisterFiles(context.Background(), []catalog.RegistrationTarget{target}, func(*sql.Tx, catalog.RegistrationResult) error { close(entered); <-release; return nil })
		done <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err := s.db.catalog.AddLibrary(ctx, "Nested", child)
	cancel()
	close(release)
	if runErr := <-done; runErr != nil {
		t.Fatal(runErr)
	}
	if !errors.Is(err, catalog.ErrBusy) {
		t.Fatalf("nested library raced active registration instead of refusing: %v", err)
	}
}
