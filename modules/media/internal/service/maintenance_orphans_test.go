package service

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func oldOrphanTree(t *testing.T, root string) {
	t.Helper()
	old := time.Now().Add(-48 * time.Hour)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		return os.Chtimes(path, old, old)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOrphanFoldersPreviewAndDeleteOnlyMovieAttachments(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"actor/ABC-123/movie.nfo":  "<movie><title>Example</title></movie>",
		"actor/ABC-123/poster.jpg": "poster", "actor/ABC-123/extrafanart/1.png": "art",
		"actor/ABC-123/.actors/name.jpg": "actor", "actor/ABC-123/subs/en.srt": "subtitle",
		"actor/ABC-124/movie.nfo": "<movie/>", "actor/ABC-124/ABC-124.mp4": "video",
		"actor/ABC-124/extrafanart/1.png": "keep", "actor/ABC-124/.actors/name.jpg": "keep",
		".actors/name.jpg": "shared", "movie.nfo": "<movie/>", "poster.jpg": "root",
	} {
		autoVideo(t, root, name, content)
	}
	oldOrphanTree(t, root)
	plan, err := planOrphanFolders(root, 24*time.Hour)
	if err != nil || plan.Found != 1 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	if _, err := os.Stat(filepath.Join(root, "actor/ABC-123/movie.nfo")); err != nil {
		t.Fatal("preview deleted", err)
	}
	result, err := executeOrphanFolders(plan)
	if err != nil || result.Removed != 1 || result.DeletedFiles != 5 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "actor/ABC-123")); !os.IsNotExist(err) {
		t.Fatal("orphan remains", err)
	}
	for _, name := range []string{"actor/ABC-124/ABC-124.mp4", "actor/ABC-124/extrafanart/1.png", "actor/ABC-124/.actors/name.jpg", ".actors/name.jpg", "movie.nfo", "poster.jpg", "actor"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("protected %s: %v", name, err)
		}
	}
}

func TestOrphanFoldersSkipRecentUnknownSymlinkNestedVideoAndNonMovieNFO(t *testing.T) {
	root := t.TempDir()
	for _, folder := range []string{"recent", "unknown", "link", "nested", "stream", "invalid", "shared", "protected", "nested-movie"} {
		autoVideo(t, root, folder+"/movie.nfo", "<movie/>")
	}
	autoVideo(t, root, "unknown/important.pdf", "document")
	autoVideo(t, root, "nested/part/ABC-123.mp4", "movie")
	autoVideo(t, root, "stream/ABC-123.strm", "url")
	autoVideo(t, root, "invalid/movie.nfo", "not xml")
	autoVideo(t, root, "shared/movie.nfo", "<tvshow/>")
	autoVideo(t, root, "protected/#recycle/poster.jpg", "keep")
	autoVideo(t, root, "nested-movie/another/movie.nfo", "<movie/>")
	outside := autoVideo(t, t.TempDir(), "poster.jpg", "outside")
	if err := os.Symlink(outside, filepath.Join(root, "link/poster.jpg")); err != nil {
		t.Fatal(err)
	}
	oldOrphanTree(t, root)
	now := time.Now()
	if err := os.Chtimes(filepath.Join(root, "recent"), now, now); err != nil {
		t.Fatal(err)
	}
	plan, err := planOrphanFolders(root, time.Hour)
	if err != nil || plan.Found != 0 {
		t.Fatalf("unsafe plan=%+v err=%v", plan, err)
	}
	result, err := executeOrphanFolders(plan)
	if err != nil || result.DeletedFiles != 0 {
		t.Fatalf("unsafe deletion=%+v %v", result, err)
	}
}

func TestOrphanFoldersRejectChangesAfterPreview(t *testing.T) {
	for _, change := range []string{"new-video", "changed-poster", "new-unknown", "symlink-parent", "missing-nfo", "replacement-directory"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			nfo := autoVideo(t, root, "actor/ABC-123/movie.nfo", "<movie/>")
			poster := autoVideo(t, root, "actor/ABC-123/poster.jpg", "poster")
			oldOrphanTree(t, root)
			plan, err := planOrphanFolders(root, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "new-video":
				autoVideo(t, root, "actor/ABC-123/new.mp4", "new video")
			case "changed-poster":
				err = os.WriteFile(poster, []byte("changed"), 0600)
			case "new-unknown":
				autoVideo(t, root, "actor/ABC-123/keep.zip", "zip")
			case "missing-nfo":
				err = os.Remove(nfo)
			case "symlink-parent":
				err = os.Rename(filepath.Join(root, "actor"), filepath.Join(root, "moved"))
				if err == nil {
					err = os.Symlink(filepath.Join(root, "moved"), filepath.Join(root, "actor"))
				}
			case "replacement-directory":
				err = os.Rename(filepath.Dir(nfo), filepath.Dir(nfo)+"-old")
				if err == nil {
					autoVideo(t, root, "actor/ABC-123/movie.nfo", "<movie/>")
					autoVideo(t, root, "actor/ABC-123/poster.jpg", "poster")
					oldOrphanTree(t, root)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			result, err := executeOrphanFolders(plan)
			if err == nil || result.DeletedFiles != 0 {
				t.Fatalf("changed tree removed: %+v %v", result, err)
			}
			if _, err := os.Stat(poster); err != nil {
				t.Fatal("poster lost", err)
			}
		})
	}
}

func TestOrphanFoldersAPIAndIndependentSchedule(t *testing.T) {
	s := testService(t)
	root := t.TempDir()
	autoVideo(t, root, "ABC-123/movie.nfo", "<movie/>")
	oldOrphanTree(t, root)
	cfg := defaultMaintenanceConfig()
	cfg.RootDirectory = root
	cfg.OrphanCleanupEnabled = true
	saved, err := s.db.UpdateMaintenanceConfig(cfg)
	if err != nil || !saved.OrphanCleanupEnabled || saved.NextRunAt == nil {
		t.Fatalf("config=%+v %v", saved, err)
	}
	for _, step := range []struct {
		route string
		admin bool
		code  int
	}{{"preview", false, 403}, {"preview", true, 200}} {
		r := httptest.NewRequest(http.MethodPost, "/api/maintenance/orphan-folders/"+step.route, nil)
		if step.admin {
			r.Header.Set("X-Trim-Isadmin", "true")
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != step.code {
			t.Fatalf("%s: %d %s", step.route, w.Code, w.Body.String())
		}
	}
	if err := s.runScheduledMaintenance(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "ABC-123")); !os.IsNotExist(err) {
		t.Fatal("schedule did not remove orphan", err)
	}
	if cfg, err := s.db.MaintenanceConfig(); err != nil || cfg.CleanupEnabled || cfg.DuplicateCleanupEnabled {
		t.Fatalf("other switches modified: %+v %v", cfg, err)
	}
	if _, err := s.db.db.Exec(`UPDATE settings SET value=? WHERE key='maintenance_next_run_at'`, encodeJSON(time.Now().Add(-time.Minute).UTC().Format(time.RFC3339))); err != nil {
		t.Fatal(err)
	}
	if due, err := s.db.MaintenanceDue(); err != nil || !due {
		t.Fatalf("not due: %v %v", due, err)
	}
}

func TestOrphanFoldersEndpointsHandleBusyAndMissingRoot(t *testing.T) {
	s := testService(t)
	root := t.TempDir()
	cfg := defaultMaintenanceConfig()
	cfg.RootDirectory = root
	if _, err := s.db.UpdateMaintenanceConfig(cfg); err != nil {
		t.Fatal(err)
	}
	request := func(route string, want int) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/maintenance/orphan-folders/"+route, nil)
		r.Header.Set("X-Trim-Isadmin", "true")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", route, w.Code, w.Body.String())
		}
	}
	autoVideo(t, root, "ABC-123/movie.nfo", "<movie/>")
	autoVideo(t, root, "ABC-123/.DS_Store", "os metadata")
	oldOrphanTree(t, root)
	request("preview", 200)
	s.maintenanceLock.Lock()
	request("preview", 409)
	request("cleanup", 409)
	s.maintenanceLock.Unlock()
	if !s.acquireMediaFileWriter() {
		t.Fatal("lock")
	}
	request("cleanup", 409)
	s.mediaFiles.Unlock()
	request("cleanup", 200)
	if _, err := os.Stat(filepath.Join(root, "ABC-123")); !os.IsNotExist(err) {
		t.Fatal("not removed", err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	request("preview", 400)
	request("cleanup", 400)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	request("preview", 500)
	request("cleanup", 409)
}

func TestOrphanFoldersRejectInvalidInputsAndChangedRoot(t *testing.T) {
	if _, err := planOrphanFolders("/", time.Hour); err == nil {
		t.Fatal("root allowed")
	}
	if _, err := planOrphanFolders(t.TempDir(), time.Minute); err == nil {
		t.Fatal("unsafe minimum age")
	}
	root := t.TempDir()
	autoVideo(t, root, "ABC-123/movie.nfo", "<movie/>")
	oldOrphanTree(t, root)
	plan, err := planOrphanFolders(root, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(root+"-old", root) })
	if err := os.Symlink(root+"-old", root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(root) })
	if result, err := executeOrphanFolders(plan); err == nil || result.DeletedFiles != 0 {
		t.Fatalf("changed root deleted: %+v %v", result, err)
	}
	if isMovieNFO(filepath.Join(root, "missing.nfo")) {
		t.Fatal("missing NFO accepted")
	}
}
