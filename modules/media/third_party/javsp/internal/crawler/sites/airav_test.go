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

func TestAirAVParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/video/barcode/ABC-123":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
			  "count": 1,
			  "result": {
			    "barcode": "ABC-123",
			    "vid": "vid001",
			    "description": "Plot &amp; text",
			    "img_url": "https://img.example/cover.jpg",
			    "tags": [{"name":"Tag A"},{"name":"Tag B"}],
			    "name": "Title &amp; Name",
			    "actors": [{"name":"Actor A"}],
			    "publish_date": "2024-01-02",
			    "images": ["https://img.example/1.jpg"],
			    "factories": [{"name":"Producer"}]
			  }
			}`))
		case "/api/video/getVideoMedia":
			if r.URL.Query().Get("barcode") != "ABC-123" || r.URL.Query().Get("vid") != "vid001" {
				t.Fatalf("query = %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"url":"https://cdn.example/sample.mp4"}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewAirAV(client, server.URL, true)
	info, err := parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.Title != "Title & Name" || info.Plot != "Plot & text" {
		t.Fatalf("title/plot = %q / %q", info.Title, info.Plot)
	}
	if info.Cover != "https://img.example/cover.jpg" || len(info.Genre) != 2 || len(info.Actress) != 1 {
		t.Fatalf("cover/genre/actress = %#v", info)
	}
	if info.Producer != "Producer" || info.PreviewVideo != "https://cdn.example/sample.mp4" {
		t.Fatalf("producer/video = %q / %q", info.Producer, info.PreviewVideo)
	}
}

func TestAirAVNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":0}`))
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parser := NewAirAV(client, server.URL, false)
	_, err = parser.Parse(context.Background(), crawler.MovieRef{DVDID: "ABC-123"})
	if !errors.Is(err, crawler.ErrMovieNotFound) {
		t.Fatalf("err = %v, want ErrMovieNotFound", err)
	}
}
