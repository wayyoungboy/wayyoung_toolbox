package service

import (
	"net/http"
	"net/url"
	"path"
	"strings"
)

// Run ONLY after gateway identity, administrator, JSON and secret CSRF checks.
// fnOS can rewrite Host. HTTP LAN browsers may omit Fetch Metadata, so matching
// application Referer is a compatibility hint, NOT independent authorization.
// The unguessable token and absence of credentialed CORS remain essential.
func validRequestOrigin(r *http.Request, trusted bool) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "same-origin" {
		return false
	}
	raw := r.Header.Get("Origin")
	if raw == "" {
		return site == "" && r.Referer() == ""
	} // privileged non-browser client
	origin, err := url.Parse(raw)
	if err != nil || origin.Host == "" || origin.User != nil || origin.Opaque != "" || origin.Path != "" || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || (origin.Scheme != "http" && origin.Scheme != "https") {
		return false
	}
	if strings.ContainsAny(raw, "#, \t\r\n") {
		return false
	}
	appReferrer := false
	if rawReferrer := r.Referer(); rawReferrer != "" {
		ref, err := url.Parse(rawReferrer)
		if err != nil || ref.User != nil || ref.Host != origin.Host || ref.Scheme != origin.Scheme {
			return false
		}
		clean := path.Clean(ref.Path)
		appReferrer = (ref.Path == clean || ref.Path == clean+"/") && (clean == Prefix || strings.HasPrefix(clean, Prefix+"/"))
	}
	if origin.Host == r.Host {
		return true
	}
	return trusted && (site == "same-origin" || (site == "" && appReferrer))
}
