package webapp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wayyoungboy/wayyoung_toolbox/internal/supervisor"
)

type fakeManager struct {
	items        []supervisor.Info
	setID        string
	setEnabled   bool
	confirmation string
	definition   supervisor.Definition
	handler      http.Handler
}

func (manager *fakeManager) List() []supervisor.Info { return manager.items }
func (manager *fakeManager) SetEnabled(id string, enabled bool, confirmation string) error {
	manager.setID, manager.setEnabled, manager.confirmation = id, enabled, confirmation
	return nil
}
func (manager *fakeManager) Route(string) (supervisor.Definition, http.Handler, error) {
	return manager.definition, manager.handler, nil
}

func testServer(t *testing.T, manager ModuleManager) *Server {
	t.Helper()
	public := t.TempDir()
	if err := os.WriteFile(filepath.Join(public, "index.html"), []byte("toolbox"), 0o600); err != nil {
		t.Fatal(err)
	}
	server, err := New("/app/wayyoung_toolbox", public, manager, "0.6.0")
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestModuleToggleRequiresAdminAndUpdatesOneModule(t *testing.T) {
	manager := &fakeManager{items: []supervisor.Info{{ID: "music", Name: "Music"}}}
	server := testServer(t, manager)

	denied := httptest.NewRecorder()
	server.ServeHTTP(denied, httptest.NewRequest(http.MethodPut, "/app/wayyoung_toolbox/api/modules/music", strings.NewReader(`{"enabled":true}`)))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", denied.Code)
	}

	request := httptest.NewRequest(http.MethodPut, "/app/wayyoung_toolbox/api/modules/music", strings.NewReader(`{"enabled":true,"confirmation":""}`))
	request.Header.Set("X-Trim-Isadmin", "true")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if manager.setID != "music" || !manager.setEnabled {
		t.Fatalf("unexpected toggle: id=%q enabled=%v", manager.setID, manager.setEnabled)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["process_model"] != "single" {
		t.Fatalf("process_model=%v", payload["process_model"])
	}
}

func TestModuleDispatchesToInProcessHandler(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/app/wayyoung_toolbox/modules/media/api/health" {
			t.Errorf("unexpected module path: %s", request.URL.Path)
		}
		if !strings.Contains(request.Referer(), "/app/wayyoung_toolbox/modules/media/") {
			t.Errorf("referer changed: %s", request.Referer())
		}
		writer.Header().Set("Location", "/app/wayyoung_toolbox/modules/media/settings")
		writer.WriteHeader(http.StatusTemporaryRedirect)
	})

	manager := &fakeManager{
		definition: supervisor.Definition{ID: "media", Prefix: "/app/wayyoung_toolbox/modules/media"},
		handler:    handler,
	}
	server := testServer(t, manager)
	request := httptest.NewRequest(http.MethodGet, "/app/wayyoung_toolbox/modules/media/api/health", nil)
	request.Header.Set("Referer", "http://nas/app/wayyoung_toolbox/modules/media/")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected redirect, got %d: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Location"); got != "/app/wayyoung_toolbox/modules/media/settings" {
		t.Fatalf("unexpected location: %s", got)
	}
}

func TestModuleInventoryRequiresAdminAndExposesPackageVersion(t *testing.T) {
	server := testServer(t, &fakeManager{})
	denied := httptest.NewRecorder()
	server.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/app/wayyoung_toolbox/api/modules", nil))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("inventory leaked without admin: %d", denied.Code)
	}
	request := httptest.NewRequest(http.MethodGet, "/app/wayyoung_toolbox/api/modules", nil)
	request.Header.Set("X-Trim-Isadmin", "true")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || payload["version"] != "0.6.0" || payload["is_admin"] != true {
		t.Fatalf("missing version/admin metadata: %v", payload)
	}
}
