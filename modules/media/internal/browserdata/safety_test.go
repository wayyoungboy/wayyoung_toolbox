package browserdata

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz"
)

func TestBrowserCompressionAllFormatsAndTruncation(t *testing.T) {
	data := []byte(`[{"url":"https://example.test/item","dt":1700000000}]`)
	var x, l, z bytes.Buffer
	xw, err := xz.NewWriter(&x)
	if err != nil {
		t.Fatal(err)
	}
	xw.Write(data)
	xw.Close()
	lw := lz4.NewWriter(&l)
	lw.Write(data)
	lw.Close()
	zw, err := zstd.NewWriter(&z, zstd.WithEncoderConcurrency(1), zstd.WithWindowSize(1<<20))
	if err != nil {
		t.Fatal(err)
	}
	zw.Write(data)
	zw.Close()
	for _, tc := range []struct {
		name string
		data []byte
	}{{"a.json.xz", x.Bytes()}, {"a.json.lz4", l.Bytes()}, {"a.json.zstd", z.Bytes()}, {"a.json.zst", z.Bytes()}} {
		got, err := Parse(context.Background(), tc.name, "history", tc.data)
		if err != nil || len(got.Entries) != 1 {
			t.Fatalf("%s: %+v %v", tc.name, got, err)
		}
		if _, err := Parse(context.Background(), tc.name, "history", tc.data[:len(tc.data)-2]); err == nil {
			t.Fatal("accepted truncated", tc.name)
		}
	}
}
func TestBrowserTakeoutArchiveAndActivityDedup(t *testing.T) {
	activity := `<html><div class="outer-cell"><div>Visited&nbsp;<a href="https://www.google.com/url?q=https%3A%2F%2Fexample.test%2Fitem">Neutral title</a><br>Nov 15, 2023, 9:18:20 PM HKT<br>Products:<br>Chrome</div></div></html>`
	parsed, err := Parse(context.Background(), "MyActivity.html", "history", []byte(activity))
	if err != nil || len(parsed.Entries) != 1 || parsed.Entries[0].VisitedAt != "2023-11-15T13:18:20Z" {
		t.Fatalf("activity: %+v %v", parsed, err)
	}
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, _ := zw.Create("Takeout/My Activity/Chrome/MyActivity.html")
	w.Write([]byte(activity))
	w, _ = zw.Create("Takeout/Chrome/History.json")
	w.Write([]byte(`{"Browser History":[{"url":"https://example.test/item","title":"Neutral title","time_usec":1700054300000000}]}`))
	zw.Close()
	got, err := Parse(context.Background(), "takeout.zip", "history", b.Bytes())
	if err != nil || len(got.Entries) != 1 || got.Duplicates != 1 {
		t.Fatalf("takeout dedup: %+v %v", got, err)
	}
}
func TestBrowserAdditionalNativeSchemas(t *testing.T) {
	for _, ddl := range []string{
		`CREATE TABLE history(guid TEXT,url TEXT,title TEXT);CREATE TABLE visits(history_guid TEXT,date INTEGER);INSERT INTO history VALUES('a','https://example.test/item','Neutral');INSERT INTO visits VALUES('a',1700000000000000);`,
		`CREATE TABLE History(url TEXT,title TEXT,atime INTEGER);INSERT INTO History VALUES('https://example.test/item','Neutral',1700000000);`,
		`CREATE TABLE moz_places(id INTEGER,url TEXT,title TEXT);CREATE TABLE moz_historyvisits(place_id INTEGER,visit_date INTEGER);INSERT INTO moz_places VALUES(1,'https://example.test/item','Neutral');INSERT INTO moz_historyvisits VALUES(1,1700000000000);`,
	} {
		path := filepath.Join(t.TempDir(), "neutral.sqlite")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
		db.Close()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Parse(context.Background(), "history", "history", data)
		if err != nil || len(got.Entries) != 1 || !strings.HasPrefix(got.Entries[0].VisitedAt, "2023-11-14T") {
			t.Fatalf("native schema: %+v %v", got, err)
		}
	}
}
func TestBrowserRejectsFalseTimestampsAndDangerousStructures(t *testing.T) {
	for _, dt := range []string{`null`, `"not-a-time"`, `"1700000000"`, `-1`, `1e999`, `999999999999999`} {
		if _, err := Parse(context.Background(), "a.json", "history", []byte(`[{"url":"https://example.test/item","dt":`+dt+`}]`)); err == nil {
			t.Fatal("false timestamp accepted", dt)
		}
	}
	deep := strings.Repeat(`{"type":"folder","children":[`, 60) + `{"url":"https://example.test/item"}` + strings.Repeat(`]}`, 60)
	if _, err := Parse(context.Background(), "bookmarks.json", "bookmarks", []byte(`{"roots":{"folder":`+deep+`}}`)); err == nil {
		t.Fatal("accepted deep JSON")
	}
	for _, ddl := range []string{
		`CREATE VIEW History AS SELECT 'https://example.test/item' AS url,'Fake' AS title,1700000000 AS atime;`,
		`CREATE VIRTUAL TABLE History USING fts5(url,title,atime);`,
		`CREATE TABLE History(url TEXT,title TEXT,atime INTEGER);INSERT INTO History VALUES('https://example.test/item','Neutral',-999);`,
	} {
		path := filepath.Join(t.TempDir(), "untrusted.sqlite")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
		db.Close()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(context.Background(), "upload", "history", data); err == nil {
			t.Fatal("unsafe SQLite accepted")
		}
	}
}
func TestBrowserArchiveSafetyAndLimits(t *testing.T) {
	for _, name := range []string{"../outside.json", "/absolute.json", "x\\y.json", "history.json"} {
		var b bytes.Buffer
		w := zip.NewWriter(&b)
		m, _ := w.Create(name)
		m.Write([]byte(`[]`))
		if name == "history.json" {
			m, _ = w.Create(name)
			m.Write([]byte(`[]`))
		}
		w.Close()
		if _, err := Parse(context.Background(), "a.zip", "history", b.Bytes()); err == nil {
			t.Fatal("unsafe ZIP", name)
		}
	}
	var tarData bytes.Buffer
	tw := tar.NewWriter(&tarData)
	tw.WriteHeader(&tar.Header{Name: "history.json", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink})
	tw.Close()
	if _, err := Parse(context.Background(), "a.tar", "history", tarData.Bytes()); err == nil {
		t.Fatal("accepted tar symlink")
	}
	var bomb bytes.Buffer
	gw := gzip.NewWriter(&bomb)
	block := bytes.Repeat([]byte(" "), 1<<20)
	for i := 0; i < 65; i++ {
		gw.Write(block)
	}
	gw.Close()
	if _, err := Parse(context.Background(), "a.json.gz", "history", bomb.Bytes()); err == nil {
		t.Fatal("unbounded decompression")
	}
	tooMany := `[` + strings.Repeat(`{"url":"https://example.test/item","dt":1700000000},`, MaxEntries) + `{"url":"https://example.test/last","dt":1700000000}]`
	if _, err := Parse(context.Background(), "a.json", "history", []byte(tooMany)); err == nil {
		t.Fatal("record limit ignored")
	}
	if _, err := Parse(context.Background(), "a.jsonl", "history", []byte(strings.Repeat("x", (1<<20)+1))); err == nil {
		t.Fatal("line limit ignored")
	}
}

func TestBrowserEnvelopeAndHTMLResourceBounds(t *testing.T) {
	envelope := strings.Repeat(`{"Browser History":`, 40) + `[]` + strings.Repeat(`}`, 40)
	if _, err := Parse(context.Background(), "History.json", "history", []byte(envelope)); err == nil {
		t.Error("nested History envelope is not a history array")
	}
	html := `<!DOCTYPE NETSCAPE-Bookmark-file-1>` + strings.Repeat(`<span></span>`, 110000)
	if _, err := Parse(context.Background(), "bookmarks.html", "bookmarks", []byte(html)); err == nil {
		t.Fatal("unbounded HTML token count")
	}
}

func TestBrowserSQLiteExpandedRecordsBudget(t *testing.T) {
	file := filepath.Join(t.TempDir(), "small.sqlite")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE urls(id INTEGER,url TEXT,title TEXT);CREATE TABLE visits(url INTEGER,visit_time INTEGER);INSERT INTO urls VALUES(1,?,?)`, "https://example.test/"+strings.Repeat("a", 8000), strings.Repeat("b", 8192)); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1200; i++ {
		if _, err := tx.Exec("INSERT INTO visits VALUES(1,?)", int64(13344473600000000)+int64(i)*1000000); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(context.Background(), "history.sqlite", "history", data); err == nil {
		t.Fatal("SQLite JOIN bypassed normalized text budget")
	}
}

func TestBrowserJSONTraversalBudgetAndStableDigest(t *testing.T) {
	leaf := `{"url":"https://example.test/item","padding":"` + strings.Repeat("a", 2<<20) + `"}`
	nested := strings.Repeat(`{"type":"folder","children":[`, 42) + leaf + strings.Repeat(`]}`, 42)
	if _, err := Parse(context.Background(), "Bookmarks", "bookmarks", []byte(`{"roots":{"folder":`+nested+`}}`)); err == nil {
		t.Error("repeated RawMessage copies bypassed traversal budget")
	}
	data := []byte(`[{"url":"https://example.test/item","dt":1700000000}]`)
	got, err := Parse(context.Background(), "history.json", "history", data)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got.Entries)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(append([]byte("history\x00"), encoded...))
	if got.Digest != hex.EncodeToString(sum[:]) {
		t.Error("streaming digest changed existing source identity")
	}
}

func TestBrowserDoesNotPickFirstConflictingIdentifier(t *testing.T) {
	got, err := Parse(context.Background(), "history.json", "history", []byte(`[{"url":"https://www.javbus.com/?id=DEMO-100&id=DEMO-101","dt":1700000000}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 0 || got.Skipped != 1 {
		t.Fatal("conflicting repeated query identity was silently reduced to the first value")
	}
}
