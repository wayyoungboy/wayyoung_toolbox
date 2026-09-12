package updatecheck

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckDetectsNewRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"tag_name": "v1.2.0",
			"published_at": "2026-07-01T12:30:00Z",
			"html_url": "https://example.com/releases/v1.2.0",
			"body": "Intro\n## Changes\n- Fix A\nplain ignored",
			"assets": [{"name":"javsp.zip","browser_download_url":"https://example.com/javsp.zip"}]
		}`))
	}))
	defer server.Close()

	result, err := Check(context.Background(), Options{
		CurrentVersion: "v1.1.0",
		APIURL:         server.URL,
		ReleaseURL:     "https://example.com/releases/latest",
		Client:         server.Client(),
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if result.Status != StatusNewVersion {
		t.Fatalf("Status = %s, want new version", result.Status)
	}
	if result.LatestVersion != "v1.2.0" || result.ReleaseDate != "2026-07-01" {
		t.Fatalf("version/date = %q / %q", result.LatestVersion, result.ReleaseDate)
	}
	if result.AssetName != "javsp.zip" || result.AssetURL != "https://example.com/javsp.zip" {
		t.Fatalf("asset = %q / %q", result.AssetName, result.AssetURL)
	}
	if len(result.ChangeLog) != 4 || result.ChangeLog[0] != "更新时间: 2026-07-01" || result.ChangeLog[1] != "Intro" || result.ChangeLog[2] != "Changes" || result.ChangeLog[3] != "- Fix A" {
		t.Fatalf("ChangeLog = %#v", result.ChangeLog)
	}
}

func TestCheckReportsAlreadyLatestAndDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.1.0","published_at":"2026-07-01T00:00:00Z"}`))
	}))
	defer server.Close()

	result, err := Check(context.Background(), Options{
		CurrentVersion: "v1.1.0",
		APIURL:         server.URL,
		Client:         server.Client(),
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if result.Status != StatusAlreadyLatest {
		t.Fatalf("Status = %s, want already latest", result.Status)
	}

	disabled := false
	result, err = Check(context.Background(), Options{CurrentVersion: "v1.1.0", AllowCheck: &disabled})
	if err != nil {
		t.Fatalf("disabled Check returned error: %v", err)
	}
	if result.Status != StatusDisabled {
		t.Fatalf("disabled Status = %s", result.Status)
	}
}

func TestCheckDefaultsToGoProjectReleaseFeed(t *testing.T) {
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got, want := req.URL.String(), "https://api.github.com/repos/Yuukiy/javsp-go/releases/latest"; got != want {
			t.Fatalf("request URL = %q, want %q", got, want)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"tag_name":"v1.0.0"}`)),
			Request:    req,
		}, nil
	})
	client := &http.Client{Transport: transport}

	result, err := Check(context.Background(), Options{CurrentVersion: "v1.0.0", Client: client})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if result.ReleaseURL != "https://github.com/Yuukiy/javsp-go/releases/latest" {
		t.Fatalf("ReleaseURL = %q, want Go project release URL", result.ReleaseURL)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestFormatResult(t *testing.T) {
	out := Format(Result{
		CurrentVersion: "v1.1.0",
		LatestVersion:  "v1.2.0",
		ReleaseURL:     "https://example.com/releases/latest",
		Status:         StatusNewVersion,
		ChangeLog:      []string{"更新时间: 2026-07-01", "- Fix A"},
	})
	if !strings.Contains(out, "JavSP-Go: v1.1.0") || !strings.Contains(out, "v1.2.0") || !strings.Contains(out, "- Fix A") {
		t.Fatalf("Format output = %s", out)
	}
}
