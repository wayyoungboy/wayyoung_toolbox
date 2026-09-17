package service

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func autoVideo(t *testing.T, root, name, contents string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAutomaticDuplicatesKeepLargestAndOneOnTies(t *testing.T) {
	root := t.TempDir()
	large := autoVideo(t, root, "a/MIDA-734-C.mp4", "largest-video")
	small := autoVideo(t, root, "b/MIDA-734.mp4", "small")
	tie := autoVideo(t, root, "c/MIDA-734-U.mp4", "different-tie")
	single := autoVideo(t, root, "single/VENX-345.mp4", "single")
	nfo := autoVideo(t, root, "b/MIDA-734.nfo", "metadata")
	plan, err := planAutomaticDuplicates(root, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{large, small, tie} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("preview mutated files", err)
		}
	}
	result, err := executeAutomaticDuplicates(plan)
	if err != nil || result.Deleted != 2 || result.ReclaimedBytes != 18 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, path := range []string{large, single, nfo} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("keeper removed: %s %v", path, err)
		}
	}
	for _, path := range []string{small, tie} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("duplicate remains: %s %v", path, err)
		}
	}
	plan, err = planAutomaticDuplicates(root, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	result, err = executeAutomaticDuplicates(plan)
	if err != nil || result.Deleted != 0 {
		t.Fatalf("repeat deleted keeper: %+v %v", result, err)
	}
}

func TestAutomaticDuplicatesSkipMultipartRecentZeroAndLinks(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a/ABC-123.mp4", "b/ABC-123-C.mp4", "c/ABC-123-CD1.mp4", "d/ABC-123-CD2.mp4", "a/ABC-124.mp4", "b/ABC-124-C.mp4", "a/ABC-125.mp4", "-646-C.mp4", "a/ABC-126.strm", "b/ABC-126.mp4"} {
		autoVideo(t, root, name, "content")
	}
	autoVideo(t, root, "b/ABC-125-C.mp4", "")
	now := time.Now()
	if err := os.Chtimes(filepath.Join(root, "b/ABC-124-C.mp4"), now, now); err != nil {
		t.Fatal(err)
	}
	outside := autoVideo(t, t.TempDir(), "ABC-127.mp4", "outside")
	if err := os.Symlink(outside, filepath.Join(root, "ABC-127.mp4")); err != nil {
		t.Fatal(err)
	}
	plan, err := planAutomaticDuplicates(root, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executeAutomaticDuplicates(plan)
	if err != nil || result.Deleted != 0 {
		t.Fatalf("unsafe cleanup=%+v %v", result, err)
	}
}

func TestAutomaticDuplicatesRevalidateEveryMember(t *testing.T) {
	for _, change := range []string{"keeper-missing", "keeper-replaced", "candidate-changed", "parent-symlink", "root-replaced"} {
		t.Run(change, func(t *testing.T) {
			root, err := canonicalDirectory(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			keep := autoVideo(t, root, "a/ABC-123.mp4", "largest")
			drop := autoVideo(t, root, "b/ABC-123-C.mp4", "small")
			plan, err := planAutomaticDuplicates(root, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "keeper-missing":
				err = os.Remove(keep)
			case "keeper-replaced":
				err = os.Rename(keep, keep+".old")
				if err == nil {
					err = os.WriteFile(keep, []byte("largest"), 0600)
				}
			case "candidate-changed":
				err = os.WriteFile(drop, []byte("changed"), 0600)
			case "parent-symlink":
				err = os.Rename(filepath.Dir(drop), filepath.Join(root, "moved"))
				if err == nil {
					err = os.Symlink(filepath.Join(root, "moved"), filepath.Dir(drop))
				}
			case "root-replaced":
				err = os.Rename(root, root+"-moved")
				t.Cleanup(func() { _ = os.Rename(root+"-moved", root) })
				if err == nil {
					err = os.Symlink(root+"-moved", root)
				}
				t.Cleanup(func() { _ = os.Remove(root) })
			}
			if err != nil {
				t.Fatal(err)
			}
			result, err := executeAutomaticDuplicates(plan)
			if err == nil || result.Deleted != 0 {
				t.Fatalf("changed files deleted: %+v %v", result, err)
			}
			if _, err := os.Stat(drop); err != nil {
				t.Fatalf("candidate lost: %v", err)
			}
		})
	}
}

func TestAutomaticDuplicateScheduleAndAPI(t *testing.T) {
	s := testService(t)
	root := t.TempDir()
	config := defaultMaintenanceConfig()
	config.RootDirectory = root
	config.DuplicateCleanupEnabled = true
	saved, err := s.db.UpdateMaintenanceConfig(config)
	if err != nil || !saved.DuplicateCleanupEnabled || saved.NextRunAt == nil {
		t.Fatalf("config=%+v err=%v", saved, err)
	}
	if _, err := s.db.db.Exec(`UPDATE settings SET value=? WHERE key='maintenance_next_run_at'`, encodeJSON(time.Now().Add(-time.Minute).UTC().Format(time.RFC3339))); err != nil {
		t.Fatal(err)
	}
	if due, err := s.db.MaintenanceDue(); err != nil || !due {
		t.Fatalf("due=%v err=%v", due, err)
	}
	keep := autoVideo(t, root, "a/ABC-123.mp4", "large")
	autoVideo(t, root, "b/ABC-123.mp4", "tiny")
	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(empty, old, old); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		route string
		admin bool
		code  int
	}{
		{"cleanup", false, 403}, {"preview", true, 200}, {"cleanup", true, 200},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/maintenance/duplicates/"+step.route, nil)
		if step.admin {
			req.Header.Set("X-Trim-Isadmin", "true")
		}
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, req)
		if response.Code != step.code {
			t.Fatalf("%s: %d %s", step.route, response.Code, response.Body.String())
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal(err)
	}
	if err := s.runScheduledMaintenance(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(empty); err != nil {
		t.Fatal("disabled empty cleanup ran", err)
	}
	if due, err := s.db.MaintenanceDue(); err != nil || due {
		t.Fatalf("schedule not advanced: %v %v", due, err)
	}
	if !s.acquireMediaFileWriter() {
		t.Fatal("lock")
	}
	_, err = s.runAutomaticDuplicateCleanup("manual")
	s.mediaFiles.Unlock()
	if err != errMaintenanceBusy {
		t.Fatalf("must not overlap other media writes: %v", err)
	}
}
