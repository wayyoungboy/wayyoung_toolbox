package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func quoteJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func decodeResponseJSON(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatal(err)
	}
}

func TestParseAnimeFilename(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		wantTitle   string
		wantStudio  string
		wantDate    string
		wantEpisode int
	}{
		{
			name:      "episode suffix and language",
			filename:  "[230331][魔人]おとぎばなしの鬼ごっこ 第二話 アリスの壊れた世界.chs.mp4",
			wantTitle: "おとぎばなしの鬼ごっこ", wantStudio: "魔人", wantDate: "2023-03-31", wantEpisode: 2,
		},
		{
			name:      "volume suffix",
			filename:  "[230331][ショーテン]いくものがかり The Animation 上巻.chs.mp4",
			wantTitle: "いくものがかり The Animation", wantStudio: "ショーテン", wantDate: "2023-03-31", wantEpisode: 1,
		},
		{
			name:      "double date prefix",
			filename:  "[231107][231027][Queen Bee]聖痕のアリア 第一話.chs.mp4",
			wantTitle: "聖痕のアリア", wantStudio: "Queen Bee", wantDate: "2023-11-07", wantEpisode: 1,
		},
		{
			name:      "hash episode",
			filename:  "[250101][Studio]作品名 ＃3.mp4",
			wantTitle: "作品名", wantStudio: "Studio", wantDate: "2025-01-01", wantEpisode: 3,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := parseAnimeFilename(test.filename)
			if got.Title != test.wantTitle || got.Studio != test.wantStudio || got.ReleaseDate != test.wantDate || got.Episode != test.wantEpisode {
				t.Fatalf("parseAnimeFilename(%q) = %+v", test.filename, got)
			}
		})
	}
}

func TestMatchAniDBTitleUsesExactThenLongestPrefix(t *testing.T) {
	titles := []AniDBTitle{
		{AID: 17697, Language: "ja", Kind: 1, Title: "おとぎばなしの鬼ごっこ"},
		{AID: 16020, Language: "x-jat", Kind: 4, Title: "Sakusei Byoutou The Animation"},
		{AID: 17798, Language: "ja", Kind: 1, Title: "ゴブリンの巣穴"},
	}
	matcher := newAniDBMatcher(titles)

	exact := matcher.Match("おとぎばなしの鬼ごっこ")
	if exact.AID != 17697 || exact.Method != "exact" || exact.Confidence != 100 {
		t.Fatalf("exact = %+v", exact)
	}
	prefix := matcher.Match("ゴブリンの巣穴 巡礼者 アンヴィル")
	if prefix.AID != 17798 || prefix.Method != "prefix" || prefix.Confidence < 90 {
		t.Fatalf("prefix = %+v", prefix)
	}
	if got := matcher.Match("作品"); got.AID != 0 {
		t.Fatalf("short generic title should not match: %+v", got)
	}
}

type staticAniDBTitles struct {
	titles []AniDBTitle
	info   AniDBCacheInfo
}

func (s staticAniDBTitles) Load(_ context.Context, _ bool) ([]AniDBTitle, AniDBCacheInfo, error) {
	return s.titles, s.info, nil
}

func TestAnimeScanEndpointIsReadOnlyAndSkipsSymlinks(t *testing.T) {
	service := testService(t)
	root := t.TempDir()
	video := "[230331][魔人]おとぎばなしの鬼ごっこ 第二話.chs.mp4"
	if err := os.WriteFile(filepath.Join(root, video), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "说明.txt"), []byte("text"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "[230331][魔人]ゴブリンの巣穴 第一話.mp4")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.mp4")); err != nil {
		t.Fatal(err)
	}
	service.anidb = staticAniDBTitles{
		titles: []AniDBTitle{{AID: 17697, Language: "ja", Kind: 1, Title: "おとぎばなしの鬼ごっこ"}},
		info:   AniDBCacheInfo{Source: "test", TitleCount: 1},
	}

	request := httptest.NewRequest(http.MethodPost, "/api/anime/scan", strings.NewReader(`{"directory":`+quoteJSON(root)+`}`))
	request.Header.Set("X-Trim-Isadmin", "true")
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", response.Code, response.Body.String())
	}
	var result AnimeScanResult
	decodeResponseJSON(t, response, &result)
	if result.Total != 1 || result.Matched != 1 || len(result.Items) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if result.Items[0].AID != 17697 || result.Items[0].Episode != 2 || result.Items[0].FileName != video {
		t.Fatalf("item = %+v", result.Items[0])
	}
	if _, err := os.Stat(filepath.Join(root, strings.TrimSuffix(video, ".mp4")+".nfo")); !os.IsNotExist(err) {
		t.Fatalf("scan unexpectedly wrote an NFO: %v", err)
	}
}

func TestAnimeScanRejectsRelativeDirectory(t *testing.T) {
	service := testService(t)
	request := httptest.NewRequest(http.MethodPost, "/api/anime/scan", strings.NewReader(`{"directory":"relative"}`))
	request.Header.Set("X-Trim-Isadmin", "true")
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d: %s", response.Code, response.Body.String())
	}
}

func TestAniDBTitleCacheDownloadsOncePerDay(t *testing.T) {
	var requests atomic.Int32
	var unavailable atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		if unavailable.Load() {
			http.Error(w, "temporary outage", http.StatusServiceUnavailable)
			return
		}
		var content bytes.Buffer
		compressed := gzip.NewWriter(&content)
		_, _ = compressed.Write([]byte("# AniDB titles\n17697|1|ja|おとぎばなしの鬼ごっこ\n"))
		_ = compressed.Close()
		_, _ = w.Write(content.Bytes())
	}))
	defer server.Close()

	cache := newAniDBTitleCache(t.TempDir())
	cache.url = server.URL
	cache.client = server.Client()
	for range 2 {
		titles, info, err := cache.Load(context.Background(), false)
		if err != nil {
			t.Fatal(err)
		}
		if len(titles) != 1 || titles[0].AID != 17697 || info.TitleCount != 1 {
			t.Fatalf("titles=%+v info=%+v", titles, info)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("requests=%d, want 1", requests.Load())
	}
	unavailable.Store(true)
	cache.now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	titles, info, err := cache.Load(context.Background(), false)
	if err != nil || len(titles) != 1 || !info.Stale {
		t.Fatalf("stale fallback titles=%+v info=%+v err=%v", titles, info, err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests=%d, want 2", requests.Load())
	}
}

func TestWriteAnimeNFOsDoesNotOverwriteOrEscapeRoot(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "episode.mp4")
	if err := os.WriteFile(video, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	item := AnimeScanItem{
		FilePath: video, AID: 17697, MatchedTitle: "おとぎばなしの鬼ごっこ",
		Studio: "魔人", ReleaseDate: "2023-03-31", Episode: 2, Confidence: 100,
	}
	result, err := writeAnimeNFOs(animeNFORequest{Directory: root, Items: []AnimeScanItem{item}})
	if err != nil || result.Created != 1 || result.Failed != 0 {
		t.Fatalf("first write result=%+v err=%v", result, err)
	}
	nfo := strings.TrimSuffix(video, ".mp4") + ".nfo"
	content, err := os.ReadFile(nfo)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"<title>おとぎばなしの鬼ごっこ</title>", `<uniqueid type="anidb" default="true">17697</uniqueid>`, "<year>2023</year>"} {
		if !bytes.Contains(content, []byte(expected)) {
			t.Fatalf("NFO missing %q: %s", expected, content)
		}
	}
	result, err = writeAnimeNFOs(animeNFORequest{Directory: root, Items: []AnimeScanItem{item}})
	if err != nil || result.Created != 0 || result.Skipped != 1 {
		t.Fatalf("second write result=%+v err=%v", result, err)
	}

	outside := filepath.Join(t.TempDir(), "outside.mp4")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	item.FilePath = outside
	result, err = writeAnimeNFOs(animeNFORequest{Directory: root, Items: []AnimeScanItem{item}})
	if err != nil || result.Failed != 1 {
		t.Fatalf("escape result=%+v err=%v", result, err)
	}

	item.FilePath = video
	item.Confidence = 20
	result, err = writeAnimeNFOs(animeNFORequest{Directory: root, Items: []AnimeScanItem{item}})
	if err != nil || result.Skipped != 1 {
		t.Fatalf("low confidence result=%+v err=%v", result, err)
	}
	item.Confidence = 100
	item.MatchedTitle = "更新后的标题"
	result, err = writeAnimeNFOs(animeNFORequest{Directory: root, Overwrite: true, Items: []AnimeScanItem{item}})
	if err != nil || result.Created != 1 {
		t.Fatalf("overwrite result=%+v err=%v", result, err)
	}
	content, err = os.ReadFile(nfo)
	if err != nil || !bytes.Contains(content, []byte("更新后的标题")) {
		t.Fatalf("overwritten NFO=%s err=%v", content, err)
	}
}

func TestAniDBHelpersHandleInvalidDataAndChineseNumbers(t *testing.T) {
	invalid := filepath.Join(t.TempDir(), "invalid.gz")
	if err := os.WriteFile(invalid, []byte("not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := parseAniDBTitleFile(invalid); err == nil {
		t.Fatal("invalid gzip was accepted")
	}
	if got := parseEpisodeNumber("十一"); got != 11 {
		t.Fatalf("十一 = %d", got)
	}
	if got := parseEpisodeNumber("一百零二"); got != 102 {
		t.Fatalf("一百零二 = %d", got)
	}
	matcher := newAniDBMatcher([]AniDBTitle{
		{AID: 1, Kind: 4, Language: "en", Title: "Shared Title"},
		{AID: 2, Kind: 1, Language: "ja", Title: "Shared Title"},
	})
	if got := matcher.Match("shared title"); got.AID != 2 {
		t.Fatalf("preferred title = %+v", got)
	}
}
