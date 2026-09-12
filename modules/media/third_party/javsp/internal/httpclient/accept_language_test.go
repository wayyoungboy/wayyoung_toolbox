package httpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

const expectedDefaultAcceptLanguage = "zh-CN,zh;q=0.9,en;q=0.8"

func TestGetSendsDefaultAcceptLanguage(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Accept-Language")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	_ = response.Body.Close()

	if got != expectedDefaultAcceptLanguage {
		t.Fatalf("Accept-Language = %q, want %q", got, expectedDefaultAcceptLanguage)
	}
}

func TestGetPreservesExplicitAcceptLanguageCaseInsensitively(t *testing.T) {
	var got []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append([]string(nil), r.Header.Values("Accept-Language")...)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := New(Options{Headers: map[string]string{
		"accept-language": "ja-JP,ja;q=0.9",
	}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	_ = response.Body.Close()

	if len(got) != 1 || got[0] != "ja-JP,ja;q=0.9" {
		t.Fatalf("Accept-Language values = %#v, want only caller value", got)
	}
}

func TestPostFormUsesDefaultLanguageAndFormContentType(t *testing.T) {
	var language string
	var contentType string
	var form url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		language = r.Header.Get("Accept-Language")
		contentType = r.Header.Get("Content-Type")
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm returned error: %v", err)
		}
		form = r.PostForm
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.PostForm(context.Background(), server.URL, url.Values{"id": {"MIKR-069"}})
	if err != nil {
		t.Fatalf("PostForm returned error: %v", err)
	}
	_ = response.Body.Close()

	if language != expectedDefaultAcceptLanguage {
		t.Fatalf("Accept-Language = %q, want %q", language, expectedDefaultAcceptLanguage)
	}
	if contentType != "application/x-www-form-urlencoded" || form.Get("id") != "MIKR-069" {
		t.Fatalf("content type/form = %q / %#v", contentType, form)
	}
}

func TestNewRejectsMalformedProxyURL(t *testing.T) {
	if _, err := New(Options{ProxyURL: "://"}); err == nil {
		t.Fatal("New accepted malformed proxy URL")
	}
}
