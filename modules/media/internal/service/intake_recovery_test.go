package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMediaWritersShareExclusion(t *testing.T) {
	f := newMovieQueueFixture(t)
	s := f.s
	s.mediaFiles.Lock()
	defer s.mediaFiles.Unlock()
	if _, err := s.runMaintenanceCleanup("manual"); !errors.Is(err, errMaintenanceBusy) {
		t.Fatalf("maintenance bypassed file guard: %v", err)
	}
	if err := s.drainPendingMovies(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := s.db.MediaJob(f.job.ID)
	if err != nil || f.runs != 0 || job.Status != "pending" {
		t.Fatalf("queued JAVSP bypassed file guard: %+v %v", job, err)
	}
	w := httptest.NewRecorder()
	s.handleAnimeNFO(w, httptest.NewRequest(http.MethodPost, "/api/anime/nfo", strings.NewReader(`{}`)))
	if w.Code != http.StatusConflict {
		t.Fatalf("NFO writer bypassed file guard: %d", w.Code)
	}
}

func TestIntakeRestoredCancellationCanRetrySameJob(t *testing.T) {
	src, dst, item := intakeRecoveryFixture(t)
	item.JobID = 42
	ctx := &intakeCancelAfter{Context: context.Background()}
	if _, err := moveIntakeCandidateContext(ctx, item, src, dst); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
	assertIntakeFixture(t, src, map[string]string{"movie.mp4": "same"})
	if result, err := moveIntakeCandidate(item, src, dst); err != nil || result != "duplicate_removed" {
		t.Fatalf("retry permanently stuck: %q %v", result, err)
	}
}

type intakeCancelAfter struct {
	context.Context
	calls int
}

func (c *intakeCancelAfter) Err() error {
	c.calls++
	if c.calls >= 3 {
		return context.Canceled
	}
	return nil
}

func TestIntakeMoveCompletionSurvivesRetry(t *testing.T) {
	src, dst, item := intakeRecoveryFixture(t)
	if err := os.Remove(item.Destination); err != nil {
		t.Fatal(err)
	}
	if outcome, err := moveIntakeCandidate(item, src, dst); err != nil || outcome != "moved" {
		t.Fatalf("move: %q %v", outcome, err)
	}
	if err := os.Rename(item.Destination, item.Destination+".organized"); err != nil {
		t.Fatal(err)
	}
	if outcome, err := moveIntakeCandidate(item, src, dst); err != nil || outcome != "moved" {
		t.Fatalf("lost move receipt: %q %v", outcome, err)
	}
}

func TestIntakeReceiptsAreBoundToJobIdentity(t *testing.T) {
	src, dst, item := intakeRecoveryFixture(t)
	item.JobID = 1
	if _, err := moveIntakeCandidate(item, src, dst); err != nil {
		t.Fatal(err)
	}
	writeIntakeFixture(t, src, map[string]string{"movie.mp4": "new content"})
	item.JobID = 2
	if _, err := moveIntakeCandidate(item, src, dst); !errors.Is(err, errIntakeConflict) {
		t.Fatalf("old receipt consumed by new job: %v", err)
	}
	assertIntakeFixture(t, src, map[string]string{"movie.mp4": "new content"})
}

func TestIntakeMoveCrashBetweenRenameAndReceipt(t *testing.T) {
	src, dst, item := intakeRecoveryFixture(t)
	if err := os.Remove(item.Destination); err != nil {
		t.Fatal(err)
	}
	if _, err := moveIntakeCandidate(item, src, dst); err != nil {
		t.Fatal(err)
	}
	canonicalDst, err := filepath.EvalSymlinks(dst)
	if err != nil {
		t.Fatal(err)
	}
	dstRoot, err := os.OpenRoot(canonicalDst)
	if err != nil {
		t.Fatal(err)
	}
	defer dstRoot.Close()
	op, err := os.OpenRoot(filepath.Join(src, ".media-center-intake", intakeOperationName(dstRoot, "movie.mp4", 0)))
	if err != nil {
		t.Fatal(err)
	}
	defer op.Close()
	f, err := op.Open("operation.json")
	if err != nil {
		t.Fatal(err)
	}
	var journal intakeCleanupJournal
	err = json.NewDecoder(f).Decode(&journal)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	journal.State = "moving"
	if err := saveIntakeJournal(op, journal); err != nil {
		t.Fatal(err)
	}
	if outcome, err := moveIntakeCandidate(item, src, dst); err != nil || outcome != "moved" {
		t.Fatalf("cannot recover committed rename: %q %v", outcome, err)
	}
}

func TestBuildIntakeCandidatesRejectsInRootSymlinks(t *testing.T) {
	base := t.TempDir()
	src, dst := filepath.Join(base, "source"), filepath.Join(base, "destination")
	writeIntakeFixture(t, src, map[string]string{"real/movie.mp4": "content"})
	writeIntakeFixture(t, dst, map[string]string{"placeholder": ""})
	for _, name := range []string{"file", "directory"} {
		t.Run(name, func(t *testing.T) {
			link, target := filepath.Join(src, "link.mp4"), "real/movie.mp4"
			if name == "directory" {
				link, target = filepath.Join(src, "linkdir"), "real"
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if name == "directory" {
				link = filepath.Join(link, "movie.mp4")
			}
			items, err := buildIntakeCandidates([]QBTorrent{{Hash: "hash", ContentPath: link, Progress: 1, State: "stalledUP"}}, src, dst)
			if err != nil || len(items) != 0 {
				t.Fatalf("symlink candidate accepted: %+v %v", items, err)
			}
		})
	}
}

func TestIntakeDuplicateCompletionSurvivesRetry(t *testing.T) {
	src, dst, item := intakeRecoveryFixture(t)
	if _, err := moveIntakeCandidate(item, src, dst); err != nil {
		t.Fatal(err)
	}
	// The organizer may already have moved the destination. A durable receipt,
	// rather than the public path's existence, must establish prior completion.
	if err := os.Rename(item.Destination, item.Destination+".organized"); err != nil {
		t.Fatal(err)
	}
	outcome, err := moveIntakeCandidate(item, src, dst)
	if err != nil || outcome != "duplicate_removed" {
		t.Fatalf("lost completion: %q %v", outcome, err)
	}
}

func TestIntakeCapturedRecoveryDoesNotDependOnTargetExistence(t *testing.T) {
	src, dst, item := intakeRecoveryFixture(t)
	op := intakeRecoveryOperation(t, src, dst, "captured")
	if err := os.Rename(item.Source, filepath.Join(op.Name(), "content")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(item.Destination); err != nil {
		t.Fatal(err)
	}
	_, err := moveIntakeCandidate(item, src, dst)
	if !errors.Is(err, errIntakePending) {
		t.Fatalf("must not resume qB after capture: %v", err)
	}
	assertIntakeFixture(t, op.Name(), map[string]string{"content": "same"})
}

func TestIntakeRestoreRenameCrashIsRecoverable(t *testing.T) {
	src, dst, item := intakeRecoveryFixture(t)
	_ = intakeRecoveryOperation(t, src, dst, "restoring")
	_, err := moveIntakeCandidate(item, src, dst)
	if !errors.Is(err, errIntakeConflict) || errors.Is(err, errIntakePending) {
		t.Fatalf("restored source stuck: %v", err)
	}
	assertIntakeFixture(t, src, map[string]string{"movie.mp4": "same"})
}

func TestIntakeCleaningDetectsTargetChangeDuringSourceRead(t *testing.T) {
	src, dst, _ := intakeRecoveryFixture(t)
	op := intakeRecoveryOperation(t, src, dst, "captured")
	writeIntakeFixture(t, op.Name(), map[string]string{"content": "same"})
	destination, err := os.OpenRoot(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	entries, err := intakeContentManifest(context.Background(), op, "content")
	if err != nil {
		t.Fatal(err)
	}
	ctx := &intakeMutatingContext{Context: context.Background(), action: func() {
		if err := os.WriteFile(filepath.Join(dst, "movie.mp4"), []byte("lost"), 0600); err != nil {
			t.Fatal(err)
		}
	}}
	if err := cleanIntakeCaptured(ctx, op, destination, "movie.mp4", entries); err == nil {
		t.Fatal("changed target accepted")
	}
	assertIntakeFixture(t, op.Name(), map[string]string{"content": "same"})
}

// Trigger after destination hashing and as captured source hashing begins.
type intakeMutatingContext struct {
	context.Context
	calls  int
	action func()
}

func (c *intakeMutatingContext) Err() error {
	c.calls++
	if c.calls == 6 {
		c.action()
	}
	return nil
}

func intakeRecoveryFixture(t *testing.T) (string, string, IntakeCandidate) {
	t.Helper()
	base := t.TempDir()
	src, dst := filepath.Join(base, "source"), filepath.Join(base, "destination")
	writeIntakeFixture(t, src, map[string]string{"movie.mp4": "same"})
	writeIntakeFixture(t, dst, map[string]string{"movie.mp4": "same"})
	return src, dst, IntakeCandidate{Source: filepath.Join(src, "movie.mp4"), Destination: filepath.Join(dst, "movie.mp4")}
}

func intakeRecoveryOperation(t *testing.T, src, dst, state string) *os.Root {
	t.Helper()
	dst, err := filepath.EvalSymlinks(dst)
	if err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256([]byte(dst + "\x00movie.mp4"))
	path := filepath.Join(src, ".media-center-intake", hex.EncodeToString(key[:]))
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	op, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { op.Close() })
	if err := saveIntakeJournal(op, intakeCleanupJournal{Relative: "movie.mp4", DestinationRoot: dst, State: state}); err != nil {
		t.Fatal(err)
	}
	return op
}
