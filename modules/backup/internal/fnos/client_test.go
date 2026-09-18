package fnos

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSharedFoldersUsesLiveTokenAndOfficialShape(t *testing.T) {
	t.Setenv("TRIM_APPNAME", "")
	token := "first-test-token"
	calls := 0
	c := NewClient(func() string { return token })
	c.http = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+token || r.URL.Path != "/api/v1/trimapp" {
			t.Fatal("wrong auth or API path")
		}
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), "trim.file.getSharedAccessibleFolders") || !strings.Contains(string(b), "backup_center") {
			t.Fatal("wrong request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"paths":["/data/authorized"]}}`))}, nil
	})}
	for i := 0; i < 2; i++ {
		paths, err := c.SharedFolders(context.Background())
		if err != nil || len(paths) != 1 {
			t.Fatalf("%v %v", paths, err)
		}
		token = "second-test-token"
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}

func TestSharedFoldersUsesInstalledApplicationIdentity(t *testing.T) {
	for _, app := range []string{"wayyoung_toolbox", "backup_center", ""} {
		t.Run("app="+app, func(t *testing.T) {
			t.Setenv("TRIM_APPNAME", app)
			want := app
			if want == "" {
				want = "backup_center"
			}
			c := NewClient(func() string { return "test-token" })
			c.http = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				var request struct {
					AppName string `json:"appName"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				if request.AppName != want {
					t.Errorf("grant query appName = %q, want installed app %q", request.AppName, want)
					return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(`{"code":200003}`))}, nil
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"paths":[]}}`))}, nil
			})}
			if _, err := c.SharedFolders(context.Background()); err != nil {
				t.Fatalf("installed application cannot query its own grants: %v", err)
			}
		})
	}
}
func TestSharedFoldersErrorsNeverExposeToken(t *testing.T) {
	for _, body := range []string{`{"code":1030,"msg":"secret-value"}`, `invalid`, `{"code":0,"data":{"paths":["/"]}}`} {
		c := NewClient(func() string { return "secret-value" })
		c.http = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		_, err := c.SharedFolders(context.Background())
		if err == nil || strings.Contains(err.Error(), "secret-value") {
			t.Fatalf("unsafe error %v", err)
		}
	}
	if _, err := NewClient(func() string { return "" }).SharedFolders(context.Background()); err == nil {
		t.Fatal("empty token accepted")
	}
}
