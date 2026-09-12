package crawler

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/yuukiy/javsp-go/internal/httpclient"
)

var diagnosticHTTPStatus = regexp.MustCompile(`http status ([1-5][0-9]{2})(?:\b|:)`)
var diagnosticSourceID = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// Diagnostics exposes categories and field availability, never raw error text,
// URLs, response bodies, titles, account cookies, or proxy credentials.
func Diagnostics(results []Result) string {
	if len(results) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(results))
	for _, result := range results {
		id := string(result.CrawlerID)
		if !diagnosticSourceID.MatchString(id) {
			id = "unknown"
		}
		status := diagnosticError(result.Err)
		if result.Err == nil {
			status = "empty_response"
			if info := result.Info; info != nil {
				title, cover := "no", "no"
				if info.Title != "" {
					title = "yes"
				}
				if info.Cover != "" {
					cover = "yes"
				}
				status = fmt.Sprintf("partial(title=%s,cover=%s)", title, cover)
				if title == "yes" && cover == "yes" {
					status = "ok(title=yes,cover=yes)"
				}
			}
		}
		parts = append(parts, id+"="+status)
	}
	return strings.Join(parts, "; ")
}

func diagnosticError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, ErrMovieNotFound):
		return "not_found"
	case errors.Is(err, ErrMovieDuplicate):
		return "ambiguous_match"
	case errors.Is(err, ErrCredential):
		return "authentication_required"
	case errors.Is(err, ErrSitePermission):
		return "permission_denied"
	case errors.Is(err, ErrSiteBlocked), errors.Is(err, httpclient.ErrSiteBlocked):
		return "blocked"
	case errors.Is(err, ErrWebsite):
		return "parse_error"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	if err != nil {
		if match := diagnosticHTTPStatus.FindStringSubmatch(err.Error()); len(match) == 2 {
			return "http_" + match[1]
		}
	}
	return "request_error"
}
