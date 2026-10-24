package engine

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yuukiy/javsp-go/internal/avid"
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/crawler/sites"
	"github.com/yuukiy/javsp-go/internal/model"
)

// Resource searches never run scanner, organizer, translator or download code.
type ResourceSearchOptions struct {
	Options
	Code     string
	Provider string
}

type ResourceCandidate struct {
	InfoHash     string `json:"infohash"`
	Magnet       string `json:"magnet"`
	Name         string `json:"name"`
	Source       string `json:"source"`
	SourceURL    string `json:"source_url"`
	Verification string `json:"verification"`
}

type ResourceSearchResult struct {
	Code     string              `json:"code"`
	Provider string              `json:"provider"`
	State    string              `json:"state"`
	Items    []ResourceCandidate `json:"items"`
}

var ErrResourceInput = errors.New("invalid resource search input")

func SearchResources(ctx context.Context, options ResourceSearchOptions) (ResourceSearchResult, error) {
	code := strings.ToUpper(strings.TrimSpace(options.Code))
	result := ResourceSearchResult{Code: code, Provider: options.Provider, State: "unavailable", Items: []ResourceCandidate{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if options.Provider != "javdb" || !ValidResourceCode(code) {
		return result, ErrResourceInput
	}
	_, cfg, err := loadEmbeddedConfig(options.Options)
	if err != nil {
		return result, err
	}
	selection := selectCrawlerIDs(&model.Movie{DVDID: code, DataSource: avid.GuessAVType(code)}, cfg)
	enabled := false
	for _, site := range selection {
		enabled = enabled || string(site) == options.Provider
	}
	if !enabled {
		result.State = "disabled"
		return result, nil
	}
	// Use only the administrator's existing endpoint/proxy configuration. Never
	// discover mirrors, inspect browser profiles, or contact unrelated crawlers.
	base := cfg.Network.ProxyFree["javdb"]
	if base == "" {
		base = "https://javdb.com"
	}
	origin, err := url.Parse(base)
	if err != nil || origin.User != nil || origin.Hostname() == "" || (origin.Scheme != "https" && origin.Scheme != "http") || origin.RawQuery != "" || origin.Fragment != "" {
		return result, ErrResourceInput
	}
	client, err := buildHTTPClient(cfg)
	if err != nil {
		return result, err
	}
	httpClient := client.HTTPClient()
	transport := httpClient.Transport.(*http.Transport)
	if cfg.Network.ProxyServer == nil || strings.TrimSpace(*cfg.Network.ProxyServer) == "" {
		transport.Proxy = nil
	} // no inherited environment proxy
	defer transport.CloseIdleConnections()
	httpClient.Transport = &resourceTransport{next: transport, origin: origin}
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !sameResourceOrigin(req.URL, origin) {
			return errResourceBoundary
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	info, err := sites.NewJavDB(client, strings.TrimRight(base, "/")).ParseResources(ctx, crawler.MovieRef{DVDID: code})
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		switch {
		case errors.Is(err, crawler.ErrCredential), errors.Is(err, crawler.ErrSitePermission):
			result.State = "restricted"
		case errors.Is(err, crawler.ErrSiteBlocked):
			result.State = "blocked"
		case errors.Is(err, crawler.ErrMovieNotFound):
			result.State = "not_found"
		case errors.Is(err, crawler.ErrMovieDuplicate):
			result.State = "identity_mismatch"
		}
		return result, nil // no raw HTTP URLs, cookies or proxy errors cross this boundary
	}
	if !strings.EqualFold(info.DVDID, code) {
		result.State = "identity_mismatch"
		return result, nil
	}
	page, err := url.Parse(info.URL)
	if err != nil || !sameResourceOrigin(page, origin) {
		return result, nil
	}
	page.RawQuery, page.Fragment, page.User = "", "", nil
	seen := map[string]bool{}
	for _, raw := range info.Magnet {
		hash, name, ok := resourceMagnet(raw)
		if !ok || seen[hash] {
			continue
		}
		seen[hash] = true
		result.Items = append(result.Items, ResourceCandidate{InfoHash: hash, Magnet: "magnet:?xt=urn:btih:" + hash, Name: name, Source: options.Provider, SourceURL: page.String(), Verification: "unverified"})
		if len(result.Items) == 50 {
			break
		}
	}
	result.State = "empty"
	if len(result.Items) > 0 {
		result.State = "ready"
	}
	return result, nil
}

// Exact canonical identifiers only: never guess a missing prefix or extract a
// substring from a path, title or URI. The organizer's filename parser is not an
// authorization or search-input boundary.
func ValidResourceCode(code string) bool {
	if code == "" || len(code) > 128 {
		return false
	}
	for _, c := range code {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return avid.GetID(code+".mp4", nil) == code
}

var errResourceBoundary = errors.New("resource response exceeded boundary")

func sameResourceOrigin(a, b *url.URL) bool {
	return a != nil && a.User == nil && a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host)
}

type resourceTransport struct {
	next     http.RoundTripper
	origin   *url.URL
	requests atomic.Int32
}

func (t *resourceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !sameResourceOrigin(req.URL, t.origin) || t.requests.Add(1) > 6 {
		return nil, errResourceBoundary
	}
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	// Bound before HTML parsing and before the existing client's 403 inspection.
	data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if len(data) > 2<<20 {
		return nil, errResourceBoundary
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	return resp, nil
}

func resourceMagnet(raw string) (string, string, bool) {
	if len(raw) > 16<<10 {
		return "", "", false
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "magnet" || u.Host != "" || u.User != nil || u.Path != "" || u.Opaque != "" {
		return "", "", false
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query["xt"]) != 1 {
		return "", "", false
	}
	xt := query.Get("xt")
	if !strings.HasPrefix(strings.ToLower(xt), "urn:btih:") {
		return "", "", false
	}
	hash := xt[len("urn:btih:"):]
	var decoded []byte
	if len(hash) == 40 {
		decoded, err = hex.DecodeString(hash)
	} else if len(hash) == 32 {
		decoded, err = base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(hash))
	} else {
		return "", "", false
	}
	if err != nil || len(decoded) != 20 {
		return "", "", false
	}
	name := strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return -1
		}
		return c
	}, query.Get("dn"))
	if !utf8.ValidString(name) {
		name = ""
	}
	if len(name) > 500 {
		name = string([]rune(name)[:min(125, utf8.RuneCountInString(name))])
	}
	if strings.TrimSpace(name) == "" {
		name = "未提供资源名称"
	}
	return hex.EncodeToString(decoded), name, true
}
