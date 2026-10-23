package browserdata

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBrowserFormatsAndPrivacy(t *testing.T) {
	for _, tc := range []struct{ name, kind, data, title string }{
		{"history.json", "history", `[{"url":"https://example.test/DEMO-100?token=secret#private","dt":1700000000.5,"metadata":{"title":"Neutral title"}}]`, "Neutral title"},
		{"history.jsonl", "history", "{\"url\":\"https://example.test/DEMO-100\",\"dt\":1700000000,\"metadata\":null}\n", ""},
		{"History.json", "history", `{"Browser History":[{"url":"https://example.test/DEMO-100","title":"Takeout title","time_usec":1700000000000000}]}`, "Takeout title"},
		{"Bookmarks", "bookmarks", `{"roots":{"bookmark_bar":{"type":"folder","children":[{"type":"url","url":"https://example.test/DEMO-100","name":"Bookmark title","date_added":"13344473600000000"}]}}}`, "Bookmark title"},
		{"bookmarks.html", "bookmarks", `<!DOCTYPE NETSCAPE-Bookmark-file-1><DL><DT><A HREF="https://example.test/DEMO-100" ADD_DATE="1700000000">HTML &amp; title</A></DL>`, "HTML & title"},
		{"firefox.json", "bookmarks", `{"type":"text/x-moz-place-container","children":[{"type":"text/x-moz-place","uri":"https://example.test/DEMO-100","title":"Firefox title","dateAdded":1700000000000000}]}`, "Firefox title"},
	} {
		t.Run(tc.name+tc.kind, func(t *testing.T) {
			got, err := Parse(context.Background(), tc.name, tc.kind, []byte(tc.data))
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Entries) != 1 || got.Entries[0].URL != "https://example.test/DEMO-100" || got.Entries[0].Title != tc.title || got.Entries[0].VisitedAt == "" || got.Digest == "" {
				t.Fatalf("bad import: %+v", got)
			}
			if strings.Contains(got.Entries[0].URL, "secret") {
				t.Fatal("query secret retained")
			}
		})
	}
}

func TestParseBrowserNativeSQLite(t *testing.T) {
	for _, tc := range []struct{ name, kind, ddl string }{
		{"Chrome", "history", `CREATE TABLE urls(id INTEGER,url TEXT,title TEXT);CREATE TABLE visits(url INTEGER,visit_time INTEGER);INSERT INTO urls VALUES(1,'https://example.test/DEMO-100','SQLite title');INSERT INTO visits VALUES(1,13344473600000000);`},
		{"Firefox", "history", `CREATE TABLE moz_places(id INTEGER,url TEXT,title TEXT);CREATE TABLE moz_historyvisits(place_id INTEGER,visit_date INTEGER);INSERT INTO moz_places VALUES(1,'https://example.test/DEMO-100','SQLite title');INSERT INTO moz_historyvisits VALUES(1,1700000000000000);`},
		{"Safari", "history", `CREATE TABLE history_items(id INTEGER,url TEXT);CREATE TABLE history_visits(history_item INTEGER,visit_time REAL,title TEXT);INSERT INTO history_items VALUES(1,'https://example.test/DEMO-100');INSERT INTO history_visits VALUES(1,721692800,'SQLite title');`},
		{"Firefox bookmarks", "bookmarks", `CREATE TABLE moz_places(id INTEGER,url TEXT);CREATE TABLE moz_bookmarks(fk INTEGER,type INTEGER,title TEXT,dateAdded INTEGER);INSERT INTO moz_places VALUES(1,'https://example.test/DEMO-100');INSERT INTO moz_bookmarks VALUES(1,1,'SQLite title',1700000000000000);`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "neutral.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(tc.ddl); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Parse(context.Background(), "upload.sqlite", tc.kind, data)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Entries) != 1 || got.Entries[0].Title != "SQLite title" || !strings.HasPrefix(got.Entries[0].VisitedAt, "2023-11-14T") {
				t.Fatalf("time conversion/schema: %+v", got)
			}
		})
	}
}

func TestBrowserImportArchivesAndDedup(t *testing.T) {
	data := []byte(`[{"url":"https://example.test/DEMO-100?a=1","dt":1700000000,"metadata":{"title":"A"}},{"url":"https://example.test/DEMO-100?a=2","dt":1700000001,"metadata":{"title":"B"}},{"url":"javascript:alert(1)","dt":1}]`)
	original, err := Parse(context.Background(), "a.json", "history", data)
	if err != nil {
		t.Fatal(err)
	}
	if len(original.Entries) != 2 || original.Skipped != 1 || original.Duplicates != 0 {
		t.Fatalf("normalization: %+v", original)
	}
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	w.Write(data)
	w.Close()
	var tarGZ bytes.Buffer
	gw := gzip.NewWriter(&tarGZ)
	tw := tar.NewWriter(gw)
	tw.WriteHeader(&tar.Header{Name: "export/history.json", Mode: 0600, Size: int64(len(data))})
	tw.Write(data)
	tw.Close()
	gw.Close()
	var zipData bytes.Buffer
	zw := zip.NewWriter(&zipData)
	member, _ := zw.Create("Takeout/Chrome/BrowserHistory.json")
	member.Write(data)
	zw.Close()
	for _, tc := range []struct {
		name string
		data []byte
	}{{"history.json.gz", gz.Bytes()}, {"history.json.tar.gz", tarGZ.Bytes()}, {"takeout.zip", zipData.Bytes()}} {
		got, err := Parse(context.Background(), tc.name, "history", tc.data)
		if err != nil {
			t.Fatal(tc.name, err)
		}
		if got.Digest != original.Digest || len(got.Entries) != 2 {
			t.Fatalf("container changed snapshot: %+v", got)
		}
	}
}

func TestBrowserImportRejectsUnsafeAndMalformedInput(t *testing.T) {
	for _, tc := range []struct{ name, kind, data string }{
		{"a.json", "history", `{"unknown":[]}`}, {"a.json", "invalid", `[]`}, {"a.json", "history", `[] {}`},
		{"a.jsonl", "history", "{\"url\":\"https://example.test/ok\"}\nnot-json"},
		{"a.html", "history", `<script>alert(1)</script>`}, {"a.sqlite", "history", "SQLite format 3\x00broken"},
	} {
		if _, err := Parse(context.Background(), tc.name, tc.kind, []byte(tc.data)); err == nil {
			t.Fatal("accepted invalid", tc.name, tc.kind)
		}
	}
	got, err := Parse(context.Background(), "a.json", "history", []byte(`[{"url":"file:///etc/passwd","dt":1700000000},{"url":"https://user:pass@example.test/private","dt":1700000000},{"url":"https://example.test/ok","dt":1700000000,"metadata":{"title":"<script>text only</script>"}}]`))
	if err != nil || len(got.Entries) != 1 || got.Skipped != 2 {
		t.Fatalf("URL policy: %+v %v", got, err)
	}
	var bad bytes.Buffer
	zw := zip.NewWriter(&bad)
	member, _ := zw.Create("../../history.json")
	member.Write([]byte(`[]`))
	zw.Close()
	if _, err := Parse(context.Background(), "a.zip", "history", bad.Bytes()); err == nil {
		t.Fatal("accepted archive traversal")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Parse(ctx, "a.json", "history", []byte(`[]`)); err == nil {
		t.Fatal("ignored cancellation")
	}
	if _, err := Parse(context.Background(), "a.json", "history", bytes.Repeat([]byte(" "), MaxUpload+1)); err == nil {
		t.Fatal("accepted oversized upload")
	}
}

func FuzzBrowserImport(f *testing.F) {
	f.Add([]byte(`[{"url":"https://example.test/DEMO-100","dt":1700000000}]`))
	f.Add([]byte(`[{"url":"https://example.test/search?q=neutral","dt":1700000000}]`))
	f.Add([]byte("garbage"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		got, err := Parse(context.Background(), "a.json", "history", data)
		if err == nil {
			if len(got.Entries) > MaxEntries {
				t.Fatal("limit")
			}
			for _, e := range got.Entries {
				if strings.Contains(e.URL, "#") {
					t.Fatal("secret-bearing URL")
				}
			}
		}
	})
}
