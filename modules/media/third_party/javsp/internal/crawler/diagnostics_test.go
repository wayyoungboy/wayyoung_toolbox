package crawler

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
)

func TestDiagnosticsCategoriesAndPrivacy(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, "timeout"}, {context.Canceled, "canceled"},
		{ErrMovieNotFound, "not_found"}, {ErrMovieDuplicate, "ambiguous_match"},
		{ErrCredential, "authentication_required"}, {ErrSitePermission, "permission_denied"},
		{ErrSiteBlocked, "blocked"}, {httpclient.ErrSiteBlocked, "blocked"},
		{ErrWebsite, "parse_error"}, {&net.DNSError{IsTimeout: true}, "timeout"},
		{errors.New("http status 526: https://user:secret@example.test/?token=private"), "http_526"},
		{errors.New("private cookie and secret"), "request_error"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := Diagnostics([]Result{{CrawlerID: "fc2db", Err: fmt.Errorf("context: %w", tt.err)}})
			if got != "fc2db="+tt.want {
				t.Fatalf("got %q", got)
			}
		})
	}
	if got := Diagnostics(nil); got != "none" {
		t.Fatalf("nil results: %q", got)
	}
	got := Diagnostics([]Result{
		{CrawlerID: "fc2db"},
		{CrawlerID: "javmenu", Info: &model.MovieInfo{Title: "secret", Cover: "https://example.test/?private"}},
		{CrawlerID: "javdb", Info: &model.MovieInfo{}},
		{CrawlerID: model.CrawlerID("evil\nsecret"), Err: ErrCredential},
	})
	for _, want := range []string{"fc2db=empty_response", "javmenu=ok(title=yes,cover=yes)", "javdb=partial(title=no,cover=no)", "unknown=authentication_required"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	for _, secret := range []string{"secret", "private", "example.test", "\n"} {
		if strings.Contains(got, secret) {
			t.Fatalf("leaked %q", secret)
		}
	}
}
