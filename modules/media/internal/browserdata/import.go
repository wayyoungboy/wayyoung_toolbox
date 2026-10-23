// Package browserdata decodes explicitly uploaded browser exports. It never
// discovers profiles, follows URLs, executes HTML, or opens caller-chosen paths.
package browserdata

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxUpload   = 32 << 20
	MaxExpanded = 64 << 20
	MaxEntries  = 50000
	maxText     = 8192
)

var (
	ErrFormat = errors.New("浏览器资料格式无效或不受支持")
	ErrLimit  = errors.New("浏览器资料超过安全限制，请拆分导出")
)

type Entry struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	VisitedAt string `json:"visited_at"`
}
type Snapshot struct {
	Format     string  `json:"format"`
	Entries    []Entry `json:"entries"`
	Read       int     `json:"read"`
	Skipped    int     `json:"skipped"`
	Duplicates int     `json:"duplicates"`
	Digest     string  `json:"digest"`
}
type parser struct {
	ctx       context.Context
	kind      string
	snapshot  Snapshot
	expanded  int
	textBytes int
	jsonBytes int
}

func Parse(ctx context.Context, name, kind string, data []byte) (Snapshot, error) {
	p := parser{ctx: ctx, kind: kind, snapshot: Snapshot{Entries: []Entry{}}}
	if err := ctx.Err(); err != nil {
		return p.snapshot, err
	}
	if len(data) > MaxUpload {
		return p.snapshot, ErrLimit
	}
	if len(data) == 0 || (kind != "history" && kind != "bookmarks") {
		return p.snapshot, ErrFormat
	}
	if err := p.decode(name, data, 0); err != nil {
		return Snapshot{}, err
	}
	seen := map[string]Entry{}
	for _, e := range p.snapshot.Entries {
		key := e.URL
		if kind == "history" {
			key += "\x00" + e.VisitedAt
		}
		if previous, ok := seen[key]; ok {
			p.snapshot.Duplicates++
			if previous.Title < e.Title {
				seen[key] = e
			}
		} else {
			seen[key] = e
		}
	}
	p.snapshot.Entries = p.snapshot.Entries[:0]
	for _, e := range seen {
		p.snapshot.Entries = append(p.snapshot.Entries, e)
	}
	sort.Slice(p.snapshot.Entries, func(i, j int) bool {
		a, b := p.snapshot.Entries[i], p.snapshot.Entries[j]
		if a.URL != b.URL {
			return a.URL < b.URL
		}
		return a.VisitedAt < b.VisitedAt
	})
	h := sha256.New()
	_, _ = h.Write([]byte(kind + "\x00"))
	_, _ = h.Write([]byte("["))
	for i, entry := range p.snapshot.Entries {
		if i > 0 {
			_, _ = h.Write([]byte(","))
		}
		encoded, err := json.Marshal(entry)
		if err != nil {
			return Snapshot{}, ErrFormat
		}
		_, _ = h.Write(encoded)
	}
	_, _ = h.Write([]byte("]"))
	p.snapshot.Digest = hex.EncodeToString(h.Sum(nil))
	return p.snapshot, nil
}

func cleanURL(raw string) (string, bool) {
	if len(raw) > maxText || !utf8.ValidString(raw) || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", false
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.User != nil || u.Opaque != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	// Keep only identifiers/search terms used by supported media sites, never
	// cookies, access tokens, fragments or arbitrary query parameters.
	if (u.Hostname() == "www.google.com" || u.Hostname() == "google.com") && u.Path == "/url" {
		target := u.Query().Get("q")
		if target == "" {
			target = u.Query().Get("url")
		}
		if target != "" {
			v, e := url.Parse(target)
			if e != nil || v.Hostname() == "google.com" || v.Hostname() == "www.google.com" {
				return "", false
			}
			return cleanURL(target)
		}
	}
	q := url.Values{}
	for _, key := range []string{"v", "id", "q"} {
		if len(u.Query()[key]) > 1 {
			return "", false
		}
		if v := u.Query().Get(key); v != "" && len(v) <= 500 {
			q.Set(key, v)
		}
	}
	u.RawQuery = q.Encode()
	u.Fragment = ""
	u.RawFragment = ""
	u.ForceQuery = false
	u.Host = strings.ToLower(u.Host)
	return u.String(), true
}
func (p *parser) add(raw, title, at string) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	p.snapshot.Read++
	if p.snapshot.Read > MaxEntries {
		return ErrLimit
	}
	u, ok := cleanURL(raw)
	if !ok {
		p.snapshot.Skipped++
		return nil
	}
	if len(title) > maxText || !utf8.ValidString(title) {
		return ErrLimit
	}
	p.textBytes += len(u) + len(title) + len(at) + 64
	if p.textBytes > 16<<20 {
		return ErrLimit
	}
	title = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\t' && r != '\n' {
			return -1
		}
		return r
	}, title))
	p.snapshot.Entries = append(p.snapshot.Entries, Entry{u, title, at})
	return nil
}

func stamp(value float64, unit float64, offset float64) string {
	seconds := value/unit + offset
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > 4133980800 {
		return ""
	}
	whole, fraction := math.Modf(seconds)
	return time.Unix(int64(whole), int64(math.Round(fraction*1e9))).UTC().Format(time.RFC3339Nano)
}
func numeric(raw json.RawMessage) float64 {
	text := strings.Trim(string(raw), "\"")
	n, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return math.NaN()
	}
	return n
}

type jsonEntry struct {
	URL         string            `json:"url"`
	URI         string            `json:"uri"`
	Title       string            `json:"title"`
	Name        string            `json:"name"`
	DT          json.RawMessage   `json:"dt"`
	TimeUsec    json.RawMessage   `json:"time_usec"`
	DateAdded   json.RawMessage   `json:"dateAdded"`
	ChromeAdded json.RawMessage   `json:"date_added"`
	Type        string            `json:"type"`
	Children    []json.RawMessage `json:"children"`
	Metadata    *struct {
		Title string `json:"title"`
	} `json:"metadata"`
}

func (p *parser) jsonRecord(raw json.RawMessage, depth int) error {
	p.jsonBytes += len(raw)
	if p.jsonBytes > MaxExpanded {
		return ErrLimit
	}
	if depth > 48 {
		return ErrLimit
	}
	var e jsonEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		return ErrFormat
	}
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if len(e.Children) > 0 {
		if p.kind != "bookmarks" {
			return ErrFormat
		}
		for _, child := range e.Children {
			if err := p.jsonRecord(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if e.URL == "" {
		e.URL = e.URI
	}
	if e.URL == "" {
		if e.Type == "folder" || e.Type == "text/x-moz-place-container" {
			return nil
		}
		return ErrFormat
	}
	title := e.Title
	if title == "" {
		title = e.Name
	}
	if e.Metadata != nil && e.Metadata.Title != "" {
		title = e.Metadata.Title
	}
	at := ""
	if p.kind == "history" {
		if len(e.TimeUsec) > 0 {
			at = stamp(numeric(e.TimeUsec), 1e6, 0)
		} else if len(e.DT) > 0 {
			if bytes.HasPrefix(bytes.TrimSpace(e.DT), []byte(`"`)) {
				return ErrFormat
			}
			at = stamp(numeric(e.DT), 1, 0)
		} else {
			return ErrFormat
		}
		if at == "" {
			return ErrFormat
		}
	} else if len(e.ChromeAdded) > 0 {
		at = stamp(numeric(e.ChromeAdded), 1e6, -11644473600)
	} else if len(e.DateAdded) > 0 {
		at = stamp(numeric(e.DateAdded), 1e6, 0)
	}
	return p.add(e.URL, title, at)
}
func decodeSingle(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	if err := d.Decode(target); err != nil {
		return ErrFormat
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrFormat
	}
	return nil
}
func (p *parser) json(data []byte, jsonl bool) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	p.jsonBytes += len(data)
	if p.jsonBytes > MaxExpanded {
		return ErrLimit
	}
	p.snapshot.Format = "json"
	if !utf8.Valid(data) {
		return ErrFormat
	}
	if jsonl {
		p.snapshot.Format = "jsonl"
		s := bufio.NewScanner(bytes.NewReader(data))
		s.Buffer(make([]byte, 4096), 1<<20)
		for s.Scan() {
			line := bytes.TrimSpace(s.Bytes())
			if len(line) == 0 {
				continue
			}
			if err := p.jsonRecord(line, 0); err != nil {
				return err
			}
		}
		if err := s.Err(); err != nil {
			return ErrLimit
		}
		return nil
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return ErrFormat
	}
	if data[0] == '[' {
		d := json.NewDecoder(bytes.NewReader(data))
		if _, err := d.Token(); err != nil {
			return ErrFormat
		}
		for d.More() {
			var raw json.RawMessage
			if err := d.Decode(&raw); err != nil {
				return ErrFormat
			}
			if err := p.jsonRecord(raw, 0); err != nil {
				return err
			}
		}
		if _, err := d.Token(); err != nil {
			return ErrFormat
		}
		var extra any
		if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
			return ErrFormat
		}
		return nil
	}
	var object map[string]json.RawMessage
	if err := decodeSingle(data, &object); err != nil {
		return err
	}
	if history, ok := object["Browser History"]; ok {
		if p.kind != "history" {
			return ErrFormat
		}
		if !bytes.HasPrefix(bytes.TrimSpace(history), []byte("[")) {
			return ErrFormat
		}
		return p.json(history, false)
	}
	if roots, ok := object["roots"]; ok {
		if p.kind != "bookmarks" {
			return ErrFormat
		}
		var folders map[string]json.RawMessage
		if err := json.Unmarshal(roots, &folders); err != nil || folders == nil {
			return ErrFormat
		}
		for _, raw := range folders {
			if err := p.jsonRecord(raw, 0); err != nil {
				return err
			}
		}
		return nil
	}
	if _, ok := object["children"]; ok && p.kind == "bookmarks" {
		return p.jsonRecord(data, 0)
	}
	return ErrFormat
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}
func (p *parser) read(r io.Reader) ([]byte, error) {
	remaining := MaxExpanded - p.expanded
	data, err := io.ReadAll(io.LimitReader(contextReader{p.ctx, r}, int64(remaining)+1))
	if err != nil {
		if p.ctx.Err() != nil {
			return nil, p.ctx.Err()
		}
		return nil, invalid("compressed or archive stream")
	}
	if len(data) > remaining {
		return nil, ErrLimit
	}
	p.expanded += len(data)
	return data, nil
}
func invalid(section string) error { return fmt.Errorf("%w: %s", ErrFormat, section) }
