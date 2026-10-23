package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

type catalogTestLibrary struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Root string `json:"root"`
}

func TestCatalogMigratesPreviousSchemaWithoutSilentlyTrustingAnotherRoot(t *testing.T) {
	service := testService(t)
	root := t.TempDir()
	catalogWrite(t, root, "DEMO-123.mp4", "video")
	library := catalogLibrary(t, service, root)
	catalogScan(t, service, library)
	work := catalogDetail(t, service, catalogWorks(t, service, "")[0].ID)
	if _, err := service.db.db.Exec("ALTER TABLE catalog_libraries DROP COLUMN root_identity"); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.New(service.db.db, unambiguousMediaID)
	if err != nil {
		t.Fatal(err)
	}
	service.db.catalog = store
	if _, err := store.Scan(context.Background(), library.ID); !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("unpinned migrated root: %v", err)
	}
	response := catalogRequest(t, service, "PATCH", fmt.Sprintf("/api/catalog/libraries/%d", library.ID), map[string]any{"root": root})
	if response.Code != 200 {
		t.Fatalf("explicit root confirmation: %d %s", response.Code, response.Body.String())
	}
	catalogScan(t, service, library)
	again := catalogDetail(t, service, work.ID)
	if len(again.Assets) != 1 || again.Assets[0].ID != work.Assets[0].ID {
		t.Fatalf("migration changed identities: %+v", again)
	}
}

func TestCatalogRefusesMultipleIdentifiersInsideOneNFOField(t *testing.T) {
	for _, identity := range []string{`<num>DEMO-123 OTHER-999</num>`, `<uniqueid type="num">DEMO-123 OTHER-999</uniqueid>`} {
		t.Run(identity, func(t *testing.T) {
			service := testService(t)
			root := t.TempDir()
			catalogWrite(t, root, "DEMO-123.mp4", "video")
			catalogWrite(t, root, "DEMO-123.nfo", `<movie>`+identity+`<title>Ambiguous metadata</title></movie>`)
			library := catalogLibrary(t, service, root)
			catalogScan(t, service, library)
			work := catalogWorks(t, service, "")[0]
			if work.Title == "Ambiguous metadata" {
				t.Fatalf("ambiguous NFO accepted: %+v", work)
			}
		})
	}
}

func TestCatalogNFOProblemsDoNotForgetKnownIdentityOrResurrectPersonalFlags(t *testing.T) {
	service := testService(t)
	root := t.TempDir()
	catalogWrite(t, root, "_646-C.mp4", "video")
	catalogWrite(t, root, "_646-C.nfo", `<movie><num>DEMO-646</num><title>Known title</title></movie>`)
	library := catalogLibrary(t, service, root)
	catalogScan(t, service, library)
	work := catalogWorks(t, service, "")[0]
	if response := catalogRequest(t, service, "PATCH", "/api/catalog/works/"+work.ID+"/personal", map[string]bool{"favorite": true}); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if err := os.Remove(filepath.Join(root, "_646-C.nfo")); err != nil {
		t.Fatal(err)
	}
	catalogScan(t, service, library)
	if current := catalogWorks(t, service, "")[0]; current.ID != work.ID || current.Title != "Known title" {
		t.Fatalf("lost known identity after missing NFO: %+v", current)
	}
	catalogWrite(t, root, "_646-C.nfo", `<movie><num>OTHER-646</num><title>Corrected identity</title></movie>`)
	catalogScan(t, service, library)
	corrected := catalogWorks(t, service, "")[0]
	if response := catalogRequest(t, service, "PATCH", "/api/catalog/works/"+corrected.ID+"/personal", map[string]bool{"favorite": false}); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	catalogWrite(t, root, "_646-C.nfo", `<movie><num>DEMO-646</num><title>Known title</title></movie>`)
	catalogScan(t, service, library)
	if favorites := catalogWorks(t, service, "favorite=true"); len(favorites) != 0 {
		t.Fatalf("resurrected canceled favorite: %+v", favorites)
	}
}

func TestCatalogConcurrentProgressUsesOneRevisionAndCanceledScanKeepsInventory(t *testing.T) {
	service := testService(t)
	root := t.TempDir()
	catalogWrite(t, root, "DEMO-123.mp4", "0123456789")
	for _, hidden := range []string{".hidden", "#recycle", "@eaDir"} {
		if err := os.Mkdir(filepath.Join(root, hidden), 0o700); err != nil {
			t.Fatal(err)
		}
		catalogWrite(t, filepath.Join(root, hidden), "DEMO-999.mp4", "hidden")
	}
	library := catalogLibrary(t, service, root)
	catalogScan(t, service, library)
	works := catalogWorks(t, service, "")
	if len(works) != 1 {
		t.Fatalf("hidden directories imported: %+v", works)
	}
	asset := catalogDetail(t, service, works[0].ID).Assets[0]
	start := make(chan struct{})
	statuses := make(chan int, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			request := httptest.NewRequest("PUT", "/api/catalog/assets/"+asset.ID+"/progress", strings.NewReader(`{"position":3,"duration":10,"revision":0}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Trim-Isadmin", "true")
			request.Header.Set("X-Catalog-CSRF", service.catalogCSRF)
			response := httptest.NewRecorder()
			service.Handler().ServeHTTP(response, request)
			statuses <- response.Code
		}()
	}
	close(start)
	wait.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatalf("concurrent CAS: %v", counts)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.db.catalog.Scan(ctx, library.ID); err == nil {
		t.Fatal("canceled scan succeeded")
	}
	if work := catalogDetail(t, service, works[0].ID); !work.Assets[0].Available {
		t.Fatal("canceled scan erased inventory")
	}
	for _, test := range []struct {
		method, byteRange string
		status            int
	}{{"HEAD", "", 200}, {"GET", "bytes=100-101", 416}} {
		request := httptest.NewRequest(test.method, "/api/catalog/assets/"+asset.ID+"/stream", nil)
		request.Header.Set("X-Trim-Isadmin", "true")
		request.Header.Set("Range", test.byteRange)
		response := httptest.NewRecorder()
		service.Handler().ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s %s: %d", test.method, test.byteRange, response.Code)
		}
		if test.method == "HEAD" && response.Body.Len() != 0 {
			t.Fatal("HEAD returned media body")
		}
	}
}

func TestCatalogListsLibrariesAndRejectsInvalidSidecarResponses(t *testing.T) {
	service := testService(t)
	root := t.TempDir()
	catalogWrite(t, root, "DEMO-123.mp4", "video")
	catalogWrite(t, root, "DEMO-123.nfo", `<movie><title>Kept title</title><art><poster>../secret.png</poster></art></movie>`)
	catalogWrite(t, root, "DEMO-123.zh.vtt", "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nSubtitle\n")
	library := catalogLibrary(t, service, root)
	catalogScan(t, service, library)
	response := catalogRequest(t, service, "GET", "/api/catalog/libraries", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"asset_count":1`) {
		t.Fatalf("libraries: %d %s", response.Code, response.Body.String())
	}
	work := catalogDetail(t, service, catalogWorks(t, service, "")[0].ID)
	asset := work.Assets[0]
	response = catalogRequest(t, service, "GET", "/api/catalog/assets/"+asset.ID+"/poster", nil)
	if response.Code != 404 {
		t.Fatalf("unsafe artwork served: %d", response.Code)
	}
	for _, test := range []struct {
		suffix string
		status int
	}{{"subtitles/0", 200}, {"subtitles/1", 404}, {"subtitles/-1", 400}} {
		response = catalogRequest(t, service, "GET", "/api/catalog/assets/"+asset.ID+"/"+test.suffix, nil)
		if response.Code != test.status {
			t.Fatalf("%s: %d %s", test.suffix, response.Code, response.Body.String())
		}
	}
	catalogWrite(t, root, "DEMO-123.zh.vtt", "not WebVTT")
	if response := catalogRequest(t, service, "GET", "/api/catalog/assets/"+asset.ID+"/subtitles/0", nil); response.Code != 400 {
		t.Fatalf("invalid VTT: %d", response.Code)
	}
	for _, target := range []string{"limit=0", "limit=bad", "offset=-1", "library_id=-1", "favorite=bad", "continue=bad", "query=" + strings.Repeat("x", 501)} {
		if response := catalogRequest(t, service, "GET", "/api/catalog/works?"+target, nil); response.Code != 400 {
			t.Fatalf("invalid filter %q: %d", target, response.Code)
		}
	}
	if len(catalogWorks(t, service, "query=%25")) != 0 {
		t.Fatal("literal percent became wildcard")
	}
	if len(catalogWorks(t, service, fmt.Sprintf("library_id=%d", library.ID))) != 1 {
		t.Fatal("library filter missed known asset")
	}
	if response := catalogRequest(t, service, "PATCH", "/api/catalog/works/"+work.ID+"/personal", map[string]any{}); response.Code != 400 {
		t.Fatalf("empty personal patch=%d", response.Code)
	}
	if response := catalogRequest(t, service, "PATCH", "/api/catalog/works/"+work.ID, map[string]any{"title": " "}); response.Code != 400 {
		t.Fatalf("empty title patch=%d", response.Code)
	}
}

type catalogTestAsset struct {
	ID        string   `json:"id"`
	Path      string   `json:"path"`
	Available bool     `json:"available"`
	Issues    []string `json:"issues"`
	Subtitles []struct {
		Index int `json:"index"`
	} `json:"subtitles"`
	Progress struct {
		Position float64 `json:"position"`
		Duration float64 `json:"duration"`
		Revision int64   `json:"revision"`
	} `json:"progress"`
}

type catalogTestWork struct {
	ID          string             `json:"id"`
	Code        string             `json:"code"`
	Title       string             `json:"title"`
	Actors      []string           `json:"actors"`
	AssetCount  int                `json:"asset_count"`
	Favorite    bool               `json:"favorite"`
	WatchLater  bool               `json:"watch_later"`
	TitleLocked bool               `json:"title_locked"`
	Plot        string             `json:"plot"`
	Genres      []string           `json:"genres"`
	Assets      []catalogTestAsset `json:"assets"`
}

func TestCatalogKeepsPersonalStateWhenUnknownIdentityIsResolved(t *testing.T) {
	service := testService(t)
	root := t.TempDir()
	catalogWrite(t, root, "_646-C.mp4", "video")
	library := catalogLibrary(t, service, root)
	catalogScan(t, service, library)
	old := catalogWorks(t, service, "")[0]
	for endpoint, payload := range map[string]any{
		old.ID + "/personal": map[string]bool{"favorite": true, "watch_later": true},
		old.ID:               map[string]string{"title": "My corrected title"},
	} {
		if response := catalogRequest(t, service, "PATCH", "/api/catalog/works/"+endpoint, payload); response.Code != 200 {
			t.Fatalf("patch: %d %s", response.Code, response.Body.String())
		}
	}
	catalogWrite(t, root, "_646-C.nfo", `<movie><num>DEMO-646</num><title>Imported title</title></movie>`)
	catalogScan(t, service, library)
	works := catalogWorks(t, service, "favorite=true")
	if len(works) != 1 || works[0].Code != "DEMO-646" || !works[0].WatchLater || !works[0].TitleLocked || works[0].Title != "My corrected title" {
		t.Fatalf("identity migration lost state: %+v", works)
	}
}

func TestCatalogPartialNFOCannotEraseRichMetadataAcrossLibraries(t *testing.T) {
	service := testService(t)
	root := t.TempDir()
	catalogWrite(t, root, "DEMO-123.mp4", "video")
	catalogWrite(t, root, "DEMO-123.nfo", `<movie><title>Rich title</title><plot>Original synopsis</plot><actor><name>Test Actor</name></actor><genre>Test genre</genre></movie>`)
	first := catalogLibrary(t, service, root)
	catalogScan(t, service, first)
	partial := t.TempDir()
	catalogWrite(t, partial, "DEMO-123-C.mp4", "edition")
	catalogWrite(t, partial, "DEMO-123-C.nfo", `<movie><num>DEMO-123</num></movie>`)
	second := catalogLibrary(t, service, partial)
	catalogScan(t, service, second)
	work := catalogWorks(t, service, "")[0]
	if work.Title != "Rich title" || work.Plot != "Original synopsis" || len(work.Actors) != 1 || len(work.Genres) != 1 {
		t.Fatalf("partial NFO erased metadata: %+v", work)
	}
	// A partial sidecar encountered first in the same scan must not hide later fields.
	catalogWrite(t, partial, "DEMO-123-D.mp4", "another edition")
	catalogWrite(t, partial, "DEMO-123-D.nfo", `<movie><title>Updated rich title</title><plot>Updated synopsis</plot></movie>`)
	catalogScan(t, service, second)
	work = catalogWorks(t, service, "")[0]
	if work.Title != "Updated rich title" || work.Plot != "Updated synopsis" || len(work.Actors) != 1 {
		t.Fatalf("same-scan merge lost metadata: %+v", work)
	}
}

func TestCatalogReplacedRootCannotEraseInventoryOrServeOtherFiles(t *testing.T) {
	service := testService(t)
	parent := t.TempDir()
	root := filepath.Join(parent, "library")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	catalogWrite(t, root, "DEMO-123.mp4", "original")
	library := catalogLibrary(t, service, root)
	catalogScan(t, service, library)
	work := catalogDetail(t, service, catalogWorks(t, service, "")[0].ID)
	if err := os.Rename(root, filepath.Join(parent, "unmounted")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	response := catalogRequest(t, service, "POST", fmt.Sprintf("/api/catalog/libraries/%d/scan", library.ID), map[string]any{})
	if response.Code != 409 {
		t.Fatalf("replaced root scan: %d %s", response.Code, response.Body.String())
	}
	again := catalogDetail(t, service, work.ID)
	if len(again.Assets) != 1 || !again.Assets[0].Available {
		t.Fatalf("inventory erased: %+v", again)
	}
	catalogWrite(t, root, "DEMO-123.mp4", "replacement")
	response = catalogRequest(t, service, "GET", "/api/catalog/assets/"+work.Assets[0].ID+"/stream", nil)
	if response.Code != 409 {
		t.Fatalf("replaced root stream: %d", response.Code)
	}
}

func catalogRequest(t *testing.T, service *Service, method, target string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, target, &body)
	request.Header.Set("X-Trim-Isadmin", "true")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Catalog-CSRF", service.catalogCSRF)
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	return response
}

func TestCatalogGatewayOriginRequiresSecretAndNeverTrustsForwardedHost(t *testing.T) {
	service := testService(t)
	for _, test := range []struct {
		name, origin, referrer, site, token string
		want                                int
	}{
		{"native host rewrite", "http://nas.test:8001", "http://nas.test:8001" + service.prefix + "/", "", service.catalogCSRF, 400},
		{"missing secret", "http://nas.test:8001", "http://nas.test:8001" + service.prefix + "/", "", "", 403},
		{"other app", "http://nas.test:8001", "http://nas.test:8001/app/unrelated/", "", service.catalogCSRF, 403},
		{"scheme mismatch", "https://example.com", "https://example.com/", "same-origin", service.catalogCSRF, 403},
		{"origin query", "http://example.com?bad=1", "", "", service.catalogCSRF, 403},
		{"same site is not same origin", "http://example.com", "", "same-site", service.catalogCSRF, 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/api/catalog/libraries", strings.NewReader(`{"name":"invalid","root":"/"}`))
			request.Header.Set("X-Trim-Isadmin", "true")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Referer", test.referrer)
			request.Header.Set("Sec-Fetch-Site", test.site)
			request.Header.Set("X-Catalog-CSRF", test.token)
			request.Header.Set("X-Forwarded-Host", "nas.test:8001")
			response := httptest.NewRecorder()
			service.Handler().ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func catalogLibrary(t *testing.T, service *Service, root string) catalogTestLibrary {
	t.Helper()
	response := catalogRequest(t, service, "POST", "/api/catalog/libraries", map[string]any{"name": "Test library", "root": root})
	if response.Code != http.StatusCreated {
		t.Fatalf("create library: %d %s", response.Code, response.Body.String())
	}
	var library catalogTestLibrary
	decodeResponseJSON(t, response, &library)
	return library
}

func catalogScan(t *testing.T, service *Service, library catalogTestLibrary) {
	t.Helper()
	response := catalogRequest(t, service, "POST", fmt.Sprintf("/api/catalog/libraries/%d/scan", library.ID), map[string]any{})
	if response.Code != http.StatusOK {
		t.Fatalf("scan: %d %s", response.Code, response.Body.String())
	}
}

func catalogWorks(t *testing.T, service *Service, query string) []catalogTestWork {
	t.Helper()
	response := catalogRequest(t, service, "GET", "/api/catalog/works?"+query, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("list: %d %s", response.Code, response.Body.String())
	}
	var result struct {
		Items []catalogTestWork `json:"items"`
	}
	decodeResponseJSON(t, response, &result)
	return result.Items
}

func catalogDetail(t *testing.T, service *Service, id string) catalogTestWork {
	t.Helper()
	response := catalogRequest(t, service, "GET", "/api/catalog/works/"+id, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("detail: %d %s", response.Code, response.Body.String())
	}
	var work catalogTestWork
	decodeResponseJSON(t, response, &work)
	return work
}

func catalogWrite(t *testing.T, root, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogImportsExistingMediaAndPreservesEditionsWithoutChangingFiles(t *testing.T) {
	service := testService(t)
	root := t.TempDir()
	catalogWrite(t, root, "DEMO-123.mp4", "original version")
	catalogWrite(t, root, "DEMO-123-C.mp4", "subtitled version")
	catalogWrite(t, root, "DEMO-123.nfo", `<movie><title>Imported title</title><uniqueid type="num">DEMO-123</uniqueid><actor><name>Test Actor</name></actor><genre>Test genre</genre></movie>`)
	catalogWrite(t, root, "_646-C.mp4", "unknown identity")
	library := catalogLibrary(t, service, root)
	catalogScan(t, service, library)
	works := catalogWorks(t, service, "")
	if len(works) != 2 {
		t.Fatalf("works=%+v", works)
	}
	var known catalogTestWork
	for _, work := range works {
		if work.Code == "DEMO-123" {
			known = work
			if work.Title != "Imported title" || work.AssetCount != 2 || len(work.Actors) != 1 || work.Actors[0] != "Test Actor" {
				t.Fatalf("known=%+v", work)
			}
		} else if work.Code != "" || work.Title != "_646-C" {
			t.Fatalf("unknown identity was invented: %+v", work)
		}
	}
	if known.ID == "" {
		t.Fatal("known work missing")
	}
	catalogScan(t, service, library)
	again := catalogWorks(t, service, "query=DEMO-123")
	if len(again) != 1 || again[0].ID != known.ID || again[0].AssetCount != 2 {
		t.Fatalf("non-idempotent scan: %+v", again)
	}
	for name, expected := range map[string]string{"DEMO-123.mp4": "original version", "DEMO-123-C.mp4": "subtitled version", "_646-C.mp4": "unknown identity"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(data) != expected {
			t.Fatalf("scan modified media %s: %q %v", name, data, err)
		}
	}
}

func TestCatalogIgnoresConflictingAndUnsafeNFOAndDoesNotShareOtherWorkMetadata(t *testing.T) {
	service := testService(t)
	root := t.TempDir()
	catalogWrite(t, root, "DEMO-111.mp4", "a")
	catalogWrite(t, root, "DEMO-111.nfo", `<movie><title>Wrong title</title><uniqueid type="num">DEMO-999</uniqueid></movie>`)
	catalogWrite(t, root, "DEMO-222.mp4", "b")
	catalogWrite(t, root, "DEMO-222.nfo", `<!DOCTYPE movie [<!ENTITY secret SYSTEM "file:///etc/passwd">]><movie><title>&secret;</title></movie>`)
	catalogWrite(t, root, "DEMO-333.mp4", "c")
	catalogWrite(t, root, "movie.nfo", `<movie><title>Not shared across releases</title><uniqueid type="num">DEMO-333</uniqueid></movie>`)
	library := catalogLibrary(t, service, root)
	catalogScan(t, service, library)
	for _, work := range catalogWorks(t, service, "") {
		if work.Title != work.Code {
			t.Fatalf("untrusted metadata applied: %+v", work)
		}
		detail := catalogDetail(t, service, work.ID)
		if work.Code != "DEMO-333" && (len(detail.Assets) != 1 || len(detail.Assets[0].Issues) == 0) {
			t.Fatalf("missing actionable NFO issue: %+v", detail)
		}
	}
}

func TestCatalogStreamsRangesAndSubtitlesAndRejectsChangedAsset(t *testing.T) {
	service := testService(t)
	root := t.TempDir()
	catalogWrite(t, root, "DEMO-123.mp4", "0123456789")
	catalogWrite(t, root, "DEMO-123.zh.srt", "1\n00:00:01,000 --> 00:00:02,000\nTest subtitle\n")
	poster, err := os.Create(filepath.Join(root, "poster.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(poster, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err := poster.Close(); err != nil {
		t.Fatal(err)
	}
	library := catalogLibrary(t, service, root)
	catalogScan(t, service, library)
	work := catalogDetail(t, service, catalogWorks(t, service, "")[0].ID)
	asset := work.Assets[0]
	path := "/api/catalog/assets/" + asset.ID
	request := httptest.NewRequest("GET", path+"/stream", nil)
	request.Header.Set("X-Trim-Isadmin", "true")
	request.Header.Set("Range", "bytes=2-5")
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusPartialContent || response.Body.String() != "2345" || response.Header().Get("Content-Range") != "bytes 2-5/10" {
		t.Fatalf("range: %d %q %v", response.Code, response.Body.String(), response.Header())
	}
	denied := httptest.NewRecorder()
	service.Handler().ServeHTTP(denied, httptest.NewRequest("GET", path+"/stream", nil))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated stream=%d", denied.Code)
	}
	if len(asset.Subtitles) != 1 {
		t.Fatalf("subtitles=%+v", asset.Subtitles)
	}
	subtitle := catalogRequest(t, service, "GET", path+"/subtitles/0", nil)
	if subtitle.Code != http.StatusOK || !strings.HasPrefix(subtitle.Body.String(), "WEBVTT\n") || !strings.Contains(subtitle.Body.String(), "00:00:01.000 --> 00:00:02.000") {
		t.Fatalf("subtitle: %d %s", subtitle.Code, subtitle.Body.String())
	}
	art := catalogRequest(t, service, "GET", path+"/poster", nil)
	if art.Code != 200 || art.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("poster: %d %v", art.Code, art.Header())
	}
	catalogWrite(t, root, "DEMO-123.mp4", "changed content")
	changed := catalogRequest(t, service, "GET", path+"/stream", nil)
	if changed.Code != http.StatusConflict || strings.Contains(changed.Body.String(), "changed content") {
		t.Fatalf("changed asset served: %d %s", changed.Code, changed.Body.String())
	}
}

func TestCatalogNeverFollowsMediaSymlinksOrAReplacedLibraryRoot(t *testing.T) {
	service := testService(t)
	root := t.TempDir()
	outside := t.TempDir()
	catalogWrite(t, root, "DEMO-123.mp4", "authorized")
	catalogWrite(t, outside, "secret.mp4", "private outside content")
	if err := os.Symlink(filepath.Join(outside, "secret.mp4"), filepath.Join(root, "DEMO-999.mp4")); err != nil {
		t.Fatal(err)
	}
	library := catalogLibrary(t, service, root)
	catalogScan(t, service, library)
	works := catalogWorks(t, service, "")
	if len(works) != 1 {
		t.Fatalf("symlink indexed: %+v", works)
	}
	asset := catalogDetail(t, service, works[0].ID).Assets[0]
	if err := os.Remove(filepath.Join(root, "DEMO-123.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.mp4"), filepath.Join(root, "DEMO-123.mp4")); err != nil {
		t.Fatal(err)
	}
	response := catalogRequest(t, service, "GET", "/api/catalog/assets/"+asset.ID+"/stream", nil)
	if response.Code != http.StatusConflict || strings.Contains(response.Body.String(), "private outside content") {
		t.Fatalf("symlink escaped: %d %q", response.Code, response.Body.String())
	}
	for _, invalid := range []string{"/", filepath.Join(root, "DEMO-123.mp4"), root + "/../" + filepath.Base(outside)} {
		response := catalogRequest(t, service, "POST", "/api/catalog/libraries", map[string]any{"name": "bad", "root": invalid})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("unsafe root accepted %q: %d", invalid, response.Code)
		}
	}
}

func TestCatalogProgressAndFavoritePersistAndStaleProgressIsRejected(t *testing.T) {
	service := testService(t)
	root := t.TempDir()
	catalogWrite(t, root, "DEMO-123.mp4", "video")
	library := catalogLibrary(t, service, root)
	catalogScan(t, service, library)
	work := catalogDetail(t, service, catalogWorks(t, service, "")[0].ID)
	asset := work.Assets[0]
	progressPath := "/api/catalog/assets/" + asset.ID + "/progress"
	progress := map[string]any{"position": 42.5, "duration": 600, "revision": 0}
	response := catalogRequest(t, service, "PUT", progressPath, progress)
	if response.Code != 200 {
		t.Fatalf("save progress: %d %s", response.Code, response.Body.String())
	}
	stale := catalogRequest(t, service, "PUT", progressPath, progress)
	if stale.Code != 409 {
		t.Fatalf("stale progress: %d %s", stale.Code, stale.Body.String())
	}
	favorite := catalogRequest(t, service, "PATCH", "/api/catalog/works/"+work.ID+"/personal", map[string]any{"favorite": true})
	if favorite.Code != 200 {
		t.Fatalf("favorite: %d %s", favorite.Code, favorite.Body.String())
	}
	var seq int
	var name, databasePath string
	if err := service.db.db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &databasePath); err != nil {
		t.Fatal(err)
	}
	if err := service.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenDatabase(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	service.db = reopened
	after := catalogDetail(t, service, work.ID)
	if !after.Favorite || after.Assets[0].Progress.Position != 42.5 || after.Assets[0].Progress.Revision != 1 {
		t.Fatalf("lost personal state: %+v", after)
	}
	if len(catalogWorks(t, service, "favorite=true")) != 1 || len(catalogWorks(t, service, "continue=true")) != 1 {
		t.Fatal("personal filters lost")
	}
	if err := os.Remove(filepath.Join(root, "DEMO-123.mp4")); err != nil {
		t.Fatal(err)
	}
	catalogScan(t, service, library)
	after = catalogDetail(t, service, work.ID)
	if after.Assets[0].Available || after.Assets[0].Progress.Position != 42.5 {
		t.Fatalf("missing file lost identity or progress: %+v", after)
	}
}

func TestCatalogRejectsCrossOriginAndInvalidProgress(t *testing.T) {
	service := testService(t)
	request := httptest.NewRequest("POST", "/api/catalog/libraries", strings.NewReader(`{"name":"bad","root":"/"}`))
	request.Header.Set("X-Trim-Isadmin", "true")
	request.Header.Set("Origin", "https://unrelated.invalid")
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin accepted: %d %s", response.Code, response.Body.String())
	}
	for _, body := range []map[string]any{
		{"position": -1, "duration": 60, "revision": 0},
		{"position": 61, "duration": 60, "revision": 0},
		{"position": 1, "duration": 0, "revision": 0},
	} {
		response := catalogRequest(t, service, "PUT", "/api/catalog/assets/unknown/progress", body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid progress: %d %s", response.Code, response.Body.String())
		}
	}
}
