package webapp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	backupmodule "fnos-backup-center/module"
	"github.com/wayyoungboy/wayyoung_toolbox/internal/supervisor"
)

func TestBackupWriteThroughToolboxHTTPGateway(t *testing.T) {
	const prefix = "/app/wayyoung_toolbox/modules/backup"
	module, err := backupmodule.New(context.Background(), backupmodule.Options{
		DataDir: t.TempDir(), PublicDir: t.TempDir(), Prefix: prefix,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := module.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	server := testServer(t, &fakeManager{definition: supervisor.Definition{ID: "backup", Prefix: prefix}, handler: module.Handler()})
	request := func(method, path, body, token, referer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost"+prefix+path, strings.NewReader(body))
		r.Header.Set("X-Trim-Isadmin", "true")
		r.Header.Set("X-Trim-Userid", "1000")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://nas.test:8001")
		r.Header.Set("Referer", "http://nas.test:8001"+referer)
		r.Header.Set("X-Backup-CSRF", token)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	w := request("GET", "/api/state", "", "", prefix+"/")
	var state struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || w.Code != 200 || state.CSRF == "" {
		t.Fatalf("state status=%d: %v", w.Code, err)
	}
	for _, tc := range []struct {
		name, ref, token string
		want             int
	}{
		{"configured module", prefix + "/", state.CSRF, 200},
		{"old standalone", "/app/backup_center/", state.CSRF, 403},
		{"missing CSRF", prefix + "/", "", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := request("PUT", "/api/settings", `{"retain":100}`, tc.token, tc.ref)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}
