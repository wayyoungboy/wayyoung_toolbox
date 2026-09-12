package sites

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
)

func TestJavDBParseSearchAndDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			_, _ = w.Write([]byte(`<html><body>
<a class="box" href="/v/abc" title="Search Title">
  <div class="video-title"><strong>ABC-123</strong></div>
</a>
</body></html>`))
		case "/v/abc":
			_, _ = w.Write([]byte(`
<html><body><section><div><div class="video-detail">
  <h2>
    <strong class="current-title">ABC-123 JavDB Title</strong>
    <span class="origin-title">Original Title</span>
    <a class="meta-link" href="#">show</a>
  </h2>
  <img class="video-cover" src="https://img.example/cover.jpg">
  <a class="tile-item" data-fancybox="gallery" href="https://img.example/sample1.jpg"></a>
  <video id="preview-video"><source src="//video.example/preview.mp4"></video>
  <nav class="panel movie-panel-info">
    <div><span>ABC-123</span></div>
    <div><strong>日期:</strong><span>2024-03-04</span></div>
    <div><strong>時長:</strong><span>125 分鍾</span></div>
    <div><strong>導演:</strong><span><a>Director</a></span></div>
    <div><strong>片商:</strong><span><a>Producer</a></span></div>
    <div><strong>發行:</strong><span><a>Publisher</a></span></div>
    <div><strong>系列:</strong><span><a>Series</a></span></div>
    <div><span class="score-stars"></span>4.2分</div>
    <div><strong>類別:</strong><span><a href="/tags/uncensored?locale=zh">Genre A</a><a href="/tags/tag2">Genre B</a></span></div>
    <div><strong>演員:</strong><span><a>Actress A</a><strong>♀</strong><a>Actor B</a><strong>♂</strong></span></div>
  </nav>
  <div class="magnet-name column is-four-fifths"><a href="magnet:?xt=1[javdb.com]"></a></div>
</div></div></section></body></html>`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewJavDB(client, server.URL)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.URL != server.URL+"/v/abc" || info.Title != "JavDB Title" || info.OriginalTitle != "Original Title" {
		t.Fatalf("url/title/original = %#v", info)
	}
	if info.DVDID != "ABC-123" || info.Cover != "https://img.example/cover.jpg" || info.PublishDate != "2024-03-04" || info.Duration != "125" {
		t.Fatalf("basic fields = %#v", info)
	}
	if info.Director != "Director" || info.Producer != "Producer" || info.Publisher != "Publisher" || info.Serial != "Series" {
		t.Fatalf("staff fields = %#v", info)
	}
	if info.Score != "8.40" {
		t.Fatalf("Score = %q, want 8.40", info.Score)
	}
	if len(info.Genre) != 2 || len(info.GenreID) != 2 || info.GenreID[0] != "uncensored?locale=zh" {
		t.Fatalf("genre = %#v / %#v", info.Genre, info.GenreID)
	}
	if info.Uncensored == nil || !*info.Uncensored {
		t.Fatalf("Uncensored = %#v, want true", info.Uncensored)
	}
	if len(info.Actress) != 1 || info.Actress[0] != "Actress A" {
		t.Fatalf("Actress = %#v", info.Actress)
	}
	if len(info.Magnet) != 1 || info.Magnet[0] != "magnet:?xt=1" {
		t.Fatalf("Magnet = %#v", info.Magnet)
	}
	if len(info.PreviewPics) != 1 || info.PreviewVideo != "https://video.example/preview.mp4" {
		t.Fatalf("previews = %#v / %q", info.PreviewPics, info.PreviewVideo)
	}
}

func TestJavDBFallsBackToSearchCardWhenDetailNeedsLogin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			_, _ = w.Write([]byte(`<html><body>
<a class="box" href="/v/restricted" title="Search Title">
  <div><img src="https://img.example/search-cover.jpg"></div>
  <div class="score"><span><span></span>4.0分</span></div>
  <div class="meta">2024-01-02</div>
  <div class="video-title"><strong>ABC-123</strong></div>
</a>
</body></html>`))
		case "/v/restricted":
			http.Redirect(w, r, "/login", http.StatusFound)
		case "/login":
			_, _ = w.Write([]byte(`<html><body>login</body></html>`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewJavDB(client, server.URL)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.URL != server.URL+"/v/restricted" || info.Title != "Search Title" || info.Cover != "https://img.example/search-cover.jpg" {
		t.Fatalf("fallback fields = %#v", info)
	}
	if info.Score != "8.00" || info.PublishDate != "2024-01-02" {
		t.Fatalf("fallback score/date = %#v", info)
	}
}

func TestJavDBRetriesWithProviderCookiesOnLoginRedirect(t *testing.T) {
	var unauthenticatedSearches int
	var authenticatedSearches int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			if cookie, err := r.Cookie("_jdb_session"); err != nil || cookie.Value != "valid" {
				unauthenticatedSearches++
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			authenticatedSearches++
			_, _ = w.Write([]byte(`<html><body>
<a class="box" href="/v/abc" title="Search Title">
  <div class="video-title"><strong>ABC-123</strong></div>
</a>
</body></html>`))
		case "/v/abc":
			if cookie, err := r.Cookie("_jdb_session"); err != nil || cookie.Value != "valid" {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			_, _ = w.Write([]byte(`
<html><body><section><div><div class="video-detail">
  <h2><strong class="current-title">ABC-123 Cookie Title</strong></h2>
  <img class="video-cover" src="https://img.example/cover.jpg">
  <nav class="panel movie-panel-info"><div><span>ABC-123</span></div></nav>
</div></div></section></body></html>`))
		case "/login":
			_, _ = w.Write([]byte(`<html><body>login</body></html>`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	provider := &fakeJavDBCookieProvider{
		cookies: [][]*http.Cookie{{{Name: "_jdb_session", Value: "valid"}}},
	}
	parser := NewJavDBWithCookieProvider(client, server.URL, provider)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.Title != "Cookie Title" || info.DVDID != "ABC-123" {
		t.Fatalf("info = %#v", info)
	}
	if unauthenticatedSearches != 1 || authenticatedSearches != 1 || provider.calls != 1 {
		t.Fatalf("unauth=%d auth=%d provider=%d", unauthenticatedSearches, authenticatedSearches, provider.calls)
	}
}

func TestJavDBNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body><a class="box"><div class="video-title"><strong>XYZ-999</strong></div></a></body></html>`))
	}))
	defer server.Close()

	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewJavDB(client, server.URL)
	_, err = parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if !errors.Is(err, crawler.ErrMovieNotFound) {
		t.Fatalf("err = %v, want ErrMovieNotFound", err)
	}
}

type fakeJavDBCookieProvider struct {
	cookies [][]*http.Cookie
	calls   int
}

func (f *fakeJavDBCookieProvider) NextJavDBCookies(context.Context) ([]*http.Cookie, bool) {
	f.calls++
	if len(f.cookies) == 0 {
		return nil, false
	}
	cookies := f.cookies[0]
	f.cookies = f.cookies[1:]
	return cookies, true
}
