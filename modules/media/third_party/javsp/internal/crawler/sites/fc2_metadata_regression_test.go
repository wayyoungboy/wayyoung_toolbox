package sites

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
)

func TestFC2MetadataCurrentPages(t *testing.T) {
	tests := []struct {
		name, site, path, body, title string
		wantErr                       error
	}{
		{"javmenu current layout", "javmenu", "/FC2-4419625", `<h1 class="new-heading"><span>FC2-4419625</span> Sample title</h1><script type="application/ld+json">{"@type":"VideoObject","name":"Sample title","thumbnailUrl":"/cover.jpg","uploadDate":"2024-05-07"}</script>`, "Sample title", nil},
		{"javmenu graph", "javmenu", "/FC2-4419625", `<h1>FC2-PPV-4419625 Sample title</h1><script type="application/ld+json">{"@graph":[{"@type":"WebPage"},{"@type":"VideoObject","name":"Sample title","thumbnailUrl":["/cover.jpg"],"uploadDate":"2024-05-07T00:00:00+09:00"}]}</script>`, "Sample title", nil},
		{"javmenu similar ID", "javmenu", "/FC2-4419625", `<h1>FC2-4149625 Wrong title</h1><script type="application/ld+json">{"@type":"VideoObject","name":"Wrong title","thumbnailUrl":"/cover.jpg"}</script>`, "", crawler.ErrMovieNotFound},
		{"javmenu longer ID", "javmenu", "/FC2-4419625", `<h1>FC2-44196250 Wrong title</h1><script type="application/ld+json">{"@type":"VideoObject","name":"Wrong title","thumbnailUrl":"/cover.jpg"}</script>`, "", crawler.ErrMovieNotFound},
		{"javmenu multiple IDs", "javmenu", "/FC2-4419625", `<h1>FC2-4419625 FC2-4419627 Ambiguous title</h1><script type="application/ld+json">{"@type":"VideoObject","name":"Ambiguous title","thumbnailUrl":"/cover.jpg"}</script>`, "", crawler.ErrMovieNotFound},
		{"javmenu recommendations only", "javmenu", "/FC2-4419625", `<div>FC2-4419625</div><script type="application/ld+json">{"@type":"VideoObject","name":"Unrelated title","thumbnailUrl":"/cover.jpg"}</script>`, "", crawler.ErrMovieNotFound},
		{"javmenu broken schema", "javmenu", "/FC2-4419625", `<h1>FC2-4419625 Sample title</h1><script type="application/ld+json">{broken</script>`, "", crawler.ErrWebsite},
		{"fc2db current layout", "fc2db", "/work/4419625/", `<h1>FC2PPV-4419625 Sample title</h1><script type="application/ld+json">{"@type":"VideoObject","name":"FC2PPV-4419625","thumbnailUrl":"/cover.jpg","uploadDate":"2024-05-07","duration":"01:20:00","actor":[{"name":"Sample Actor"}],"publisher":{"name":"Sample Seller"}}</script>`, "Sample title", nil},
		{"fc2db graph", "fc2db", "/work/4419625/", `<h1>FC2-PPV-4419625 Sample title</h1><script type="application/ld+json">[{"@type":"BreadcrumbList"},{"@type":"VideoObject","name":"FC2PPV-4419625","thumbnailUrl":["/cover.jpg"],"uploadDate":"2024-05-07"}]</script>`, "Sample title", nil},
		{"fc2db bracketed code only", "fc2db", "/work/4419625/", `<h1>【FC2PPV-4419625】</h1><script type="application/ld+json">{"@type":"VideoObject","name":"FC2PPV-4419625","thumbnailUrl":"/cover.jpg","uploadDate":"2024-05-07"}</script>`, "", nil},
		{"fc2db wrong ID", "fc2db", "/work/4419625/", `<h1>FC2PPV-4419625 Sample title</h1><script type="application/ld+json">{"@type":"VideoObject","name":"FC2PPV-4149625","thumbnailUrl":"/cover.jpg"}</script>`, "", crawler.ErrMovieNotFound},
		{"fc2db longer schema ID", "fc2db", "/work/4419625/", `<h1>FC2PPV-4419625 Sample title</h1><script type="application/ld+json">{"@type":"VideoObject","name":"FC2PPV-44196250","thumbnailUrl":"/cover.jpg"}</script>`, "", crawler.ErrMovieNotFound},
		{"fc2db missing", "fc2db", "/work/4419625/", `<h1>Not found</h1>`, "", crawler.ErrMovieNotFound},
		{"fc2db broken schema", "fc2db", "/work/4419625/", `<h1>FC2PPV-4419625 Sample title</h1><script type="application/ld+json">{broken</script>`, "", crawler.ErrWebsite},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.path {
					t.Errorf("path = %s; want %s", r.URL.Path, tt.path)
				}
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			client, err := httpclient.New(httpclient.Options{})
			if err != nil {
				t.Fatal(err)
			}
			registry := crawler.NewRegistry()
			if err := RegisterAll(registry, client, RegistryOptions{BaseURLs: map[string]string{tt.site: server.URL}}); err != nil {
				t.Fatal(err)
			}
			parser, ok := registry.Get(model.CrawlerID(tt.site))
			if !ok {
				t.Fatalf("%s is not registered", tt.site)
			}
			info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "FC2-4419625"})
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v; want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if info.DVDID != "FC2-4419625" || info.Title != tt.title || info.Cover != server.URL+"/cover.jpg" || info.PublishDate != "2024-05-07" {
				t.Fatalf("incorrect metadata: %#v", info)
			}
			if strings.Contains(tt.body, "Sample Actor") && (info.Duration != "80" || len(info.Actress) != 1 || info.Actress[0] != "Sample Actor" || info.Producer != "Sample Seller") {
				t.Fatalf("missing additional metadata: %#v", info)
			}
		})
	}
}

func TestFC2DBRejectsInvalidInputAndUnsafeImage(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`<h1>FC2PPV-4419625 Sample title</h1><script type="application/ld+json">{"@type":"VideoObject","name":"FC2PPV-4419625","thumbnailUrl":"file:///etc/passwd"}</script>`))
	}))
	defer server.Close()
	client, _ := httpclient.New(httpclient.Options{})
	registry := crawler.NewRegistry()
	if err := RegisterAll(registry, client, RegistryOptions{BaseURLs: map[string]string{"fc2db": server.URL}}); err != nil {
		t.Fatal(err)
	}
	parser, ok := registry.Get("fc2db")
	if !ok {
		t.Fatal("fc2db is not registered")
	}
	for _, id := range []string{"FC2-../../secret", "4419625", "FC2-44196250extra", "ABC-123"} {
		if _, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: id}); !errors.Is(err, crawler.ErrMovieNotFound) {
			t.Fatalf("input %q accepted: %v", id, err)
		}
	}
	if requests != 0 {
		t.Fatalf("invalid IDs triggered %d requests", requests)
	}
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "FC2-4419625"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Cover != "" {
		t.Fatalf("unsafe image URL accepted: %q", info.Cover)
	}
}
