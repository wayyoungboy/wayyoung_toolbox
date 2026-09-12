package browsercookies

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestReadJavDBCookieDBFiltersAndDecryptsCookies(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "Cookies")
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	createCookieDB(t, dbPath)
	insertCookie(t, dbPath, "javdb123.com", "_jdb_session", "plain-session", nil, now.Add(time.Hour))
	insertCookie(t, dbPath, "javdb123.com", "theme", "dark", nil, now.Add(time.Hour))
	insertCookie(t, dbPath, "javdb999.com", "theme", "dark", nil, now.Add(time.Hour))
	insertCookie(t, dbPath, "javdbexpired.com", "_jdb_session", "expired", nil, now.Add(-time.Hour))
	insertCookie(t, dbPath, ".javdb456.com", "_jdb_session", "", []byte("encrypted-session"), now.Add(time.Hour))

	entries, err := readJavDBCookieDB(dbPath, now, func(data []byte) (string, error) {
		if string(data) != "encrypted-session" {
			return "", errors.New("unexpected encrypted value")
		}
		return "decrypted-session", nil
	})
	if err != nil {
		t.Fatalf("readJavDBCookieDB returned error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %#v, want 2 valid sites", entries)
	}
	bySite := map[string]Entry{}
	for _, entry := range entries {
		bySite[entry.Site] = entry
	}
	if got := cookieValue(bySite["javdb123.com"].Cookies, "_jdb_session"); got != "plain-session" {
		t.Fatalf("plain session = %q", got)
	}
	if got := cookieValue(bySite["javdb123.com"].Cookies, "theme"); got != "dark" {
		t.Fatalf("theme = %q", got)
	}
	if got := cookieValue(bySite[".javdb456.com"].Cookies, "_jdb_session"); got != "decrypted-session" {
		t.Fatalf("decrypted session = %q", got)
	}
	if _, ok := bySite["javdb999.com"]; ok {
		t.Fatalf("site without _jdb_session should be filtered")
	}
}

func TestProviderReturnsCookiesOnce(t *testing.T) {
	provider := NewProviderFromEntries([]Entry{{
		Profile: "Chrome: Default",
		Site:    "javdb123.com",
		Cookies: []*http.Cookie{{Name: "_jdb_session", Value: "valid"}},
	}})
	cookies, ok := provider.NextJavDBCookies(context.Background())
	if !ok || cookieValue(cookies, "_jdb_session") != "valid" {
		t.Fatalf("first cookies = %#v, ok=%v", cookies, ok)
	}
	if cookies, ok := provider.NextJavDBCookies(context.Background()); ok || cookies != nil {
		t.Fatalf("second cookies = %#v, ok=%v", cookies, ok)
	}
}

func createCookieDB(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE cookies (
		host_key TEXT,
		name TEXT,
		value TEXT,
		encrypted_value BLOB,
		expires_utc INTEGER
	)`)
	if err != nil {
		t.Fatal(err)
	}
}

func insertCookie(t *testing.T, dbPath string, host string, name string, value string, encrypted []byte, expires time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(
		`INSERT INTO cookies(host_key, name, value, encrypted_value, expires_utc) VALUES (?, ?, ?, ?, ?)`,
		host,
		name,
		value,
		encrypted,
		chromeTime(expires),
	)
	if err != nil {
		t.Fatal(err)
	}
}

func cookieValue(cookies []*http.Cookie, name string) string {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}
