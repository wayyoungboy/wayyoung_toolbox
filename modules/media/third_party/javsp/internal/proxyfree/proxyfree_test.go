package proxyfree

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCandidateURLParsers(t *testing.T) {
	cases := []struct {
		name string
		fn   func(string) []string
		html string
		want []string
	}{
		{
			name: "avsox",
			fn:   parseAVSoxURLs,
			html: `<h4><strong><a href="https://avsox.example">x</a></strong></h4>`,
			want: []string{"https://avsox.example"},
		},
		{
			name: "javbus",
			fn:   parseJavBusURLs,
			html: `防屏蔽地址：https://www.bus-example.com`,
			want: []string{"https://www.bus-example.com"},
		},
		{
			name: "javlib",
			fn:   parseJavLibURLs,
			html: `<div class="p-note user-profile-bio mb-3 js-user-profile-bio f4">y78k</div>`,
			want: []string{"https://www.y78k.com"},
		},
		{
			name: "javdb js",
			fn:   parseJavDBJSURLs,
			html: `$officialUrl = "https://javdb123.com";`,
			want: []string{"https://javdb123.com"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.fn(tc.html)
			if len(got) != len(tc.want) || got[0] != tc.want[0] {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestChooseConnectable(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer down.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer up.Close()

	got := ChooseConnectable(context.Background(), up.Client(), []string{down.URL, up.URL})
	if got != up.URL {
		t.Fatalf("ChooseConnectable = %q, want %q", got, up.URL)
	}
}

func TestParseJavLibURLsMissingBio(t *testing.T) {
	if got := parseJavLibURLs(`<html></html>`); got != nil {
		t.Fatalf("parseJavLibURLs = %#v, want nil", got)
	}
}

func TestParseScriptURLsResolvesRelativeURLs(t *testing.T) {
	got := parseScriptURLs(`<script src="/js/index.123.js"></script>`, "https://jav524.app")
	want := []string{"https://jav524.app/js/index.123.js"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("parseScriptURLs = %#v, want %#v", got, want)
	}
}
