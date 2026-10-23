package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func deletePreview(t *testing.T, s *Service, ids []string) map[string]any {
	t.Helper()
	response := catalogRequest(t, s, "POST", "/api/catalog/deletion/preview", map[string]any{"work_ids": ids})
	if response.Code != 200 {
		t.Fatalf("preview: %d %s", response.Code, response.Body.String())
	}
	var plan map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestCatalogPermanentDeletionRequiresPreviewAndKeepsOtherFiles(t *testing.T) {
	s := testService(t)
	root := t.TempDir()
	for name, content := range map[string]string{"DEMO-123.mp4": "video", "DEMO-123-C.mp4": "edition", "DEMO-123.nfo": "<movie><title>First</title></movie>", "DEMO-123.zh.srt": "subtitle", "DEMO-123-fanart.jpg": "image", "OTHER-999.mp4": "other", "poster.jpg": "shared", "notes.txt": "keep"} {
		catalogWrite(t, root, name, content)
	}
	lib := catalogLibrary(t, s, root)
	catalogScan(t, s, lib)
	ids := []string{catalogWorks(t, s, "query=DEMO-123")[0].ID}
	plan := deletePreview(t, s, ids)
	if _, err := os.Stat(filepath.Join(root, "DEMO-123.mp4")); err != nil {
		t.Fatal("preview deleted files")
	}
	for _, payload := range []map[string]any{{"work_ids": ids}, {"work_ids": ids, "token": plan["token"], "confirmation": "yes"}} {
		if w := catalogRequest(t, s, "POST", "/api/catalog/deletion/execute", payload); w.Code != 400 {
			t.Fatalf("missing confirmation: %d", w.Code)
		}
	}
	w := catalogRequest(t, s, "POST", "/api/catalog/deletion/execute", map[string]any{"work_ids": ids, "token": plan["token"], "confirmation": "永久删除"})
	if w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	for _, name := range []string{"DEMO-123.mp4", "DEMO-123-C.mp4", "DEMO-123.nfo", "DEMO-123.zh.srt", "DEMO-123-fanart.jpg"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Errorf("not deleted %s: %v", name, err)
		}
	}
	for _, name := range []string{"OTHER-999.mp4", "poster.jpg", "notes.txt"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("unrelated file removed %s", name)
		}
	}
	catalogScan(t, s, lib)
	if got := catalogWorks(t, s, ""); len(got) != 1 || got[0].Code != "OTHER-999" {
		t.Fatalf("deleted work returned: %+v", got)
	}
	if w := catalogRequest(t, s, "POST", "/api/catalog/deletion/execute", map[string]any{"work_ids": ids, "token": plan["token"], "confirmation": "永久删除"}); w.Code == 200 {
		t.Fatal("deletion replay accepted")
	}
}

func TestCatalogDeletionRejectsStalePlansAndSymlinks(t *testing.T) {
	for _, mode := range []string{"video", "sidecar", "symlink", "root"} {
		t.Run(mode, func(t *testing.T) {
			s := testService(t)
			root := t.TempDir()
			catalogWrite(t, root, "DEMO-123.mp4", "video")
			catalogWrite(t, root, "DEMO-123.nfo", "<movie><title>Title</title></movie>")
			lib := catalogLibrary(t, s, root)
			catalogScan(t, s, lib)
			ids := []string{catalogWorks(t, s, "")[0].ID}
			plan := deletePreview(t, s, ids)
			switch mode {
			case "video":
				catalogWrite(t, root, "DEMO-123.mp4", "replaced content")
			case "sidecar":
				catalogWrite(t, root, "DEMO-123.nfo", "changed")
			case "symlink":
				outside := filepath.Join(t.TempDir(), "outside.nfo")
				if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(root, "DEMO-123.nfo")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "DEMO-123.nfo")); err != nil {
					t.Fatal(err)
				}
			case "root":
				if err := os.Rename(root, root+"-old"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.RemoveAll(root + "-old") })
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
				catalogWrite(t, root, "DEMO-123.mp4", "replacement")
			}
			w := catalogRequest(t, s, "POST", "/api/catalog/deletion/execute", map[string]any{"work_ids": ids, "token": plan["token"], "confirmation": "永久删除"})
			if w.Code != 409 {
				t.Fatalf("stale %s: %d %s", mode, w.Code, w.Body.String())
			}
			if _, err := os.Stat(filepath.Join(root, "DEMO-123.mp4")); err != nil {
				t.Fatal("partial delete on stale plan")
			}
		})
	}
}

func TestCatalogDeletionNeedsAdministratorAndCSRF(t *testing.T) {
	s := testService(t)
	for _, endpoint := range []string{"preview", "execute"} {
		w := queueAPIRequest(s, "POST", "/api/catalog/deletion/"+endpoint, `{"work_ids":["x"]}`, true)
		if w.Code != 403 {
			t.Fatalf("no csrf %s: %d", endpoint, w.Code)
		}
		w = queueAPIRequest(s, "POST", "/api/catalog/deletion/"+endpoint, `{}`, false)
		if w.Code != 403 {
			t.Fatalf("no admin: %d", w.Code)
		}
	}
}
