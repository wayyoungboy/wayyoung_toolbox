package browsercookies

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha1"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const chromeUnixEpochOffsetSeconds = 11644473600

type Entry struct {
	Profile string
	Site    string
	Cookies []*http.Cookie
}

type Provider struct {
	load func(context.Context) ([]Entry, error)

	once    sync.Once
	loadErr error

	mu      sync.Mutex
	entries []Entry
	index   int
}

func NewJavDBProvider() *Provider {
	return &Provider{load: LoadJavDB}
}

func NewProviderFromEntries(entries []Entry) *Provider {
	return &Provider{entries: cloneEntries(entries)}
}

func (p *Provider) NextJavDBCookies(ctx context.Context) ([]*http.Cookie, bool) {
	p.once.Do(func() {
		if p.load != nil {
			p.entries, p.loadErr = p.load(ctx)
		}
	})
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.index >= len(p.entries) {
		return nil, false
	}
	cookies := cloneCookies(p.entries[p.index].Cookies)
	p.index++
	return cookies, len(cookies) > 0
}

func LoadJavDB(ctx context.Context) ([]Entry, error) {
	var entries []Entry
	var firstErr error
	for _, browser := range browserProfiles() {
		if err := ctx.Err(); err != nil {
			return entries, err
		}
		decrypt := decryptPlain
		if runtime.GOOS == "darwin" && browser.safeStorageService != "" {
			password, err := macSafeStoragePassword(ctx, browser.safeStorageService)
			if err == nil && password != "" {
				decrypt = newChromeCBCDecryptor(password, 1003)
			} else if firstErr == nil && err != nil {
				firstErr = err
			}
		} else if runtime.GOOS == "linux" {
			decrypt = newChromeCBCDecryptor("peanuts", 1)
		} else if runtime.GOOS == "windows" {
			if found, err := windowsChromeDecryptor(filepath.Join(browser.userDataDir, "Local State")); err == nil {
				decrypt = found
			} else if firstErr == nil {
				firstErr = err
			}
		}

		for _, dbPath := range cookieDBPaths(browser.userDataDir) {
			profile := browser.name + ": " + profileName(browser.userDataDir, dbPath)
			found, err := readJavDBCookieDB(dbPath, time.Now(), decrypt)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			for _, entry := range found {
				entry.Profile = profile
				entries = append(entries, entry)
			}
		}
	}
	if len(entries) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return entries, nil
}

func readJavDBCookieDB(dbPath string, now time.Time, decrypt func([]byte) (string, error)) ([]Entry, error) {
	tempDB, cleanup, err := copyCookieDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	db, err := sql.Open("sqlite", tempDB)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT host_key, name, value, encrypted_value, expires_utc
FROM cookies
WHERE host_key LIKE ? OR host_key LIKE ?
ORDER BY host_key, name`, "javdb%.com", ".javdb%.com")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byHost := map[string][]*http.Cookie{}
	for rows.Next() {
		var host string
		var name string
		var value string
		var encrypted []byte
		var expiresUTC int64
		if err := rows.Scan(&host, &name, &value, &encrypted, &expiresUTC); err != nil {
			return nil, err
		}
		expires := chromeUTCToTime(expiresUTC)
		if !expires.After(now) {
			continue
		}
		if value == "" && len(encrypted) > 0 {
			decrypted, err := decrypt(encrypted)
			if err != nil {
				continue
			}
			value = decrypted
		}
		if value == "" {
			continue
		}
		byHost[host] = append(byHost[host], &http.Cookie{
			Name:    name,
			Value:   value,
			Domain:  host,
			Expires: expires,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	hosts := make([]string, 0, len(byHost))
	for host, cookies := range byHost {
		if cookieByName(cookies, "_jdb_session") != nil {
			hosts = append(hosts, host)
		}
	}
	sort.Strings(hosts)
	entries := make([]Entry, 0, len(hosts))
	for _, host := range hosts {
		entries = append(entries, Entry{Site: host, Cookies: byHost[host]})
	}
	return entries, nil
}

func copyCookieDB(dbPath string) (string, func(), error) {
	source, err := os.Open(dbPath)
	if err != nil {
		return "", func() {}, err
	}
	defer source.Close()

	tempFile, err := os.CreateTemp("", "javsp-cookies-*.sqlite")
	if err != nil {
		return "", func() {}, err
	}
	tempPath := tempFile.Name()
	cleanup := func() { _ = os.Remove(tempPath) }
	if _, err := io.Copy(tempFile, source); err != nil {
		_ = tempFile.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := tempFile.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return tempPath, cleanup, nil
}

func chromeUTCToTime(micros int64) time.Time {
	if micros <= 0 {
		return time.Unix(0, 0)
	}
	return time.Unix(micros/1_000_000-chromeUnixEpochOffsetSeconds, 0)
}

func chromeTime(t time.Time) int64 {
	return (t.Unix() + chromeUnixEpochOffsetSeconds) * 1_000_000
}

func cookieByName(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func cookieDBPaths(userDataDir string) []string {
	var out []string
	for _, pattern := range []string{
		filepath.Join(userDataDir, "*", "Cookies"),
		filepath.Join(userDataDir, "*", "Network", "Cookies"),
	} {
		matches, _ := filepath.Glob(pattern)
		out = append(out, matches...)
	}
	sort.Strings(out)
	return out
}

func profileName(userDataDir string, dbPath string) string {
	rel, err := filepath.Rel(userDataDir, dbPath)
	if err != nil {
		return filepath.Base(filepath.Dir(dbPath))
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) == 0 || parts[0] == "." || parts[0] == "" {
		return filepath.Base(filepath.Dir(dbPath))
	}
	return parts[0]
}

type browserProfile struct {
	name               string
	userDataDir        string
	safeStorageService string
}

func browserProfiles() []browserProfile {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		root := filepath.Join(home, "Library", "Application Support")
		return []browserProfile{
			{"Chrome", filepath.Join(root, "Google", "Chrome"), "Chrome Safe Storage"},
			{"Chrome Beta", filepath.Join(root, "Google", "Chrome Beta"), "Chrome Safe Storage"},
			{"Chrome Canary", filepath.Join(root, "Google", "Chrome Canary"), "Chrome Safe Storage"},
			{"Chromium", filepath.Join(root, "Chromium"), "Chromium Safe Storage"},
			{"Edge", filepath.Join(root, "Microsoft Edge"), "Microsoft Edge Safe Storage"},
			{"Vivaldi", filepath.Join(root, "Vivaldi"), "Vivaldi Safe Storage"},
		}
	case "windows":
		localAppData := os.Getenv("LOCALAPPDATA")
		return []browserProfile{
			{"Chrome", filepath.Join(localAppData, "Google", "Chrome", "User Data"), ""},
			{"Chrome Beta", filepath.Join(localAppData, "Google", "Chrome Beta", "User Data"), ""},
			{"Chrome Canary", filepath.Join(localAppData, "Google", "Chrome SxS", "User Data"), ""},
			{"Chromium", filepath.Join(localAppData, "Google", "Chromium", "User Data"), ""},
			{"Edge", filepath.Join(localAppData, "Microsoft", "Edge", "User Data"), ""},
			{"Vivaldi", filepath.Join(localAppData, "Vivaldi", "User Data"), ""},
		}
	default:
		config := filepath.Join(home, ".config")
		return []browserProfile{
			{"Chrome", filepath.Join(config, "google-chrome"), ""},
			{"Chrome Beta", filepath.Join(config, "google-chrome-beta"), ""},
			{"Chrome Canary", filepath.Join(config, "google-chrome-unstable"), ""},
			{"Chromium", filepath.Join(config, "chromium"), ""},
			{"Edge", filepath.Join(config, "microsoft-edge"), ""},
			{"Vivaldi", filepath.Join(config, "vivaldi"), ""},
		}
	}
}

func macSafeStoragePassword(ctx context.Context, service string) (string, error) {
	out, err := exec.CommandContext(ctx, "security", "find-generic-password", "-w", "-s", service).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func decryptPlain(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("empty encrypted value")
	}
	return "", errors.New("encrypted browser cookie has no decryptor")
}

func newChromeCBCDecryptor(password string, iterations int) func([]byte) (string, error) {
	key := pbkdf2SHA1([]byte(password), []byte("saltysalt"), iterations, 16)
	iv := bytes.Repeat([]byte(" "), aes.BlockSize)
	return func(encrypted []byte) (string, error) {
		data := encrypted
		if bytes.HasPrefix(data, []byte("v10")) || bytes.HasPrefix(data, []byte("v11")) {
			data = data[3:]
		}
		if len(data) == 0 || len(data)%aes.BlockSize != 0 {
			return "", fmt.Errorf("invalid encrypted cookie length: %d", len(data))
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return "", err
		}
		plain := make([]byte, len(data))
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, data)
		plain, err = pkcs7Unpad(plain, aes.BlockSize)
		if err != nil {
			return "", err
		}
		return string(plain), nil
	}
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, errors.New("invalid pkcs7 data length")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, errors.New("invalid pkcs7 padding")
	}
	for _, b := range data[len(data)-padding:] {
		if int(b) != padding {
			return nil, errors.New("invalid pkcs7 padding bytes")
		}
	}
	return data[:len(data)-padding], nil
}

func pbkdf2SHA1(password []byte, salt []byte, iterations int, keyLen int) []byte {
	hashLen := sha1.Size
	numBlocks := (keyLen + hashLen - 1) / hashLen
	var key []byte
	for block := 1; block <= numBlocks; block++ {
		mac := hmac.New(sha1.New, password)
		_, _ = mac.Write(salt)
		var blockIndex [4]byte
		binary.BigEndian.PutUint32(blockIndex[:], uint32(block))
		_, _ = mac.Write(blockIndex[:])
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iterations; i++ {
			mac = hmac.New(sha1.New, password)
			_, _ = mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		key = append(key, t...)
	}
	return key[:keyLen]
}

func cloneEntries(entries []Entry) []Entry {
	out := make([]Entry, len(entries))
	for i, entry := range entries {
		out[i] = Entry{
			Profile: entry.Profile,
			Site:    entry.Site,
			Cookies: cloneCookies(entry.Cookies),
		}
	}
	return out
}

func cloneCookies(cookies []*http.Cookie) []*http.Cookie {
	out := make([]*http.Cookie, len(cookies))
	for i, cookie := range cookies {
		copied := *cookie
		out[i] = &copied
	}
	return out
}
