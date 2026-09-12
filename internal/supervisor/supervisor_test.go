package supervisor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

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
