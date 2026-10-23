package supervisor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestBackupRootsIncludeAuthorizedUserDirectories(t *testing.T) {
	base := t.TempDir()
	shared := filepath.Join(base, "shared")
	authorized := filepath.Join(base, "authorized")
	for _, directory := range []string{shared, authorized} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	shared, _ = filepath.EvalSymlinks(shared)
	authorized, _ = filepath.EvalSymlinks(authorized)

	roots, err := backupRoots(shared, authorized+string(os.PathListSeparator)+shared)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(roots, []string{shared, authorized}) {
		t.Fatalf("backup roots = %q, want shared and authorized paths once", roots)
	}
}

func TestBackupRootsIgnoreRevokedAccessibleDirectory(t *testing.T) {
	shared := t.TempDir()
	shared, _ = filepath.EvalSymlinks(shared)
	roots, err := backupRoots(shared, filepath.Join(t.TempDir(), "missing")+string(os.PathListSeparator)+"/")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(roots, []string{shared}) {
		t.Fatalf("backup roots = %q, want existing shared directory only", roots)
	}
}

func TestBackupRootsRejectMissingSharedDirectory(t *testing.T) {
	_, err := backupRoots(filepath.Join(t.TempDir(), "missing"), t.TempDir())
	if err == nil {
		t.Fatal("missing application share must not be ignored")
	}
}

func TestManagerDefaultsEveryModuleToDisabled(t *testing.T) {
	app := t.TempDir()
	data := t.TempDir()
	definitions := []Definition{{ID: "one", Name: "One"}, {ID: "two", Name: "Two"}}
	manager, err := New(app, data, definitions)
	if err != nil {
		t.Fatal(err)
	}
	items := manager.List()
	if len(items) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(items))
	}
	for _, item := range items {
		if item.Enabled || item.Running {
			t.Fatalf("module must default to stopped: %+v", item)
		}
	}
}

func TestDefaultDefinitionsUseUnifiedInProcessRoutes(t *testing.T) {
	definitions := DefaultDefinitions("/opt/wayyoung")
	if len(definitions) != 5 {
		t.Fatalf("expected 5 definitions, got %d", len(definitions))
	}
	for _, definition := range definitions {
		want := "/app/wayyoung_toolbox/modules/" + definition.ID
		if definition.Prefix != want {
			t.Fatalf("module %s prefix=%q want=%q", definition.ID, definition.Prefix, want)
		}
	}
}

func TestMediaDefinitionVersionMatchesRunningHealth(t *testing.T) {
	app, data := t.TempDir(), t.TempDir()
	var definition Definition
	for _, candidate := range DefaultDefinitions(app) {
		if candidate.ID == "media" {
			definition = candidate
		}
	}
	if definition.ID == "" {
		t.Fatal("media definition missing")
	}
	manager, err := New(app, data, []Definition{definition})
	if err != nil {
		t.Fatal(err)
	}
	manager.Start(context.Background())
	t.Cleanup(manager.Close)
	if err := manager.SetEnabled("media", true, ""); err != nil {
		t.Fatal(err)
	}
	_, handler, err := manager.Route("media")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, definition.Prefix+"/api/health", nil)
	request.Header.Set("X-Trim-Isadmin", "true")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", response.Code, response.Body.String())
	}
	var health struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if definition.Version != health.Version || health.Version == "" {
		t.Fatalf("media module list version %q differs from running health %q", definition.Version, health.Version)
	}
}

func TestMusicModuleIsServedByManagerProcess(t *testing.T) {
	app := t.TempDir()
	data := t.TempDir()
	public := filepath.Join(app, "modules", "music", "public")
	if err := os.MkdirAll(public, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(public, "index.html"), []byte("music"), 0o600); err != nil {
		t.Fatal(err)
	}
	definition := Definition{
		ID: "music", Name: "Music", Version: "test",
		Prefix: "/app/wayyoung_toolbox/modules/music", PublicDir: public,
	}
	manager, err := New(app, data, []Definition{definition})
	if err != nil {
		t.Fatal(err)
	}
	manager.Start(context.Background())
	defer manager.Close()
	if err := manager.SetEnabled("music", true, ""); err != nil {
		t.Fatal(err)
	}
	_, handler, err := manager.Route("music")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, definition.Prefix+"/api/health", nil)
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", response.Code, response.Body.String())
	}
	if err := manager.SetEnabled("music", false, ""); err != nil {
		t.Fatal(err)
	}
}
