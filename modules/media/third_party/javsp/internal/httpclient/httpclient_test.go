package httpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/antchfx/htmlquery"
)

func TestHTTPClientReturnsConfiguredClient(t *testing.T) {
	client, err := New(Options{
		ProxyURL: "http://proxy.example:8080",
		Timeout:  3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	got := client.HTTPClient()
	if got != client.httpClient {
		t.Fatalf("HTTPClient() = %p, want configured client %p", got, client.httpClient)
	}
	if got.Timeout != 3*time.Second {
		t.Fatalf("timeout = %s, want 3s", got.Timeout)
	}
	transport, ok := got.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", got.Transport)
	}
	proxyURL, err := transport.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: "translate.example"}})
	if err != nil {
		t.Fatalf("proxy lookup returned error: %v", err)
	}
	if proxyURL == nil || proxyURL.String() != "http://proxy.example:8080" {
		t.Fatalf("proxy URL = %v, want http://proxy.example:8080", proxyURL)
	}
}

func TestGetHTMLSendsUserAgentAndParsesXPath(t *testing.T) {
	var userAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.UserAgent()
		_, _ = w.Write([]byte(`<html><body><h1>Hello</h1></body></html>`))
	}))
	defer server.Close()

	client, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := client.GetHTML(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("GetHTML returned error: %v", err)
	}
	if userAgent == "" {
		t.Fatalf("server did not receive user agent")
	}
	node := htmlquery.FindOne(doc, "//h1")
	if node == nil || htmlquery.InnerText(node) != "Hello" {
		t.Fatalf("h1 = %v, want Hello", node)
	}
}

func TestDownloadLocalAndRemote(t *testing.T) {
	ctx := context.Background()
	client, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	localSource := filepath.Join(root, "source.txt")
	localDest := filepath.Join(root, "local-dest.txt")
	if err := os.WriteFile(localSource, []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := Download(ctx, client, localSource, localDest)
	if err != nil {
		t.Fatalf("local Download returned error: %v", err)
	}
	if info.Total != 5 {
		t.Fatalf("local total = %d, want 5", info.Total)
	}
	if data, _ := os.ReadFile(localDest); string(data) != "local" {
		t.Fatalf("local dest = %q", data)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("remote"))
	}))
	defer server.Close()
	remoteDest := filepath.Join(root, "remote-dest.txt")
	info, err = Download(ctx, client, server.URL, remoteDest)
	if err != nil {
		t.Fatalf("remote Download returned error: %v", err)
	}
	if info.Total != 6 {
		t.Fatalf("remote total = %d, want 6", info.Total)
	}
	if data, _ := os.ReadFile(remoteDest); string(data) != "remote" {
		t.Fatalf("remote dest = %q", data)
	}
}

func TestGetDetectsCloudflareChallenge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<html><title>Just a moment...</title><body>Just a moment...</body></html>`))
	}))
	defer server.Close()

	client, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Get(context.Background(), server.URL)
	if !errors.Is(err, ErrSiteBlocked) {
		t.Fatalf("err = %v, want ErrSiteBlocked", err)
	}
}
