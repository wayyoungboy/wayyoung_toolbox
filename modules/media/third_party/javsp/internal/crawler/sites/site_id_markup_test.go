package sites

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/antchfx/htmlquery"
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
)

func TestJavBusParsesDVDIDFromCurrentSpanMarkup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`
<html><head><title>MIKR-069</title></head><body>
<div class="container">
  <h3>MIKR-069 Current Markup Title</h3>
  <a class="bigImage"><img src="/cover.jpg"></a>
  <div class="col-md-3 info">
    <p><span class="header">識別碼:</span> <span style="color:#CC0000;">MIKR-069</span></p>
    <p><span class="header">發行日期:</span> 2026-01-15</p>
    <p><span class="header">長度:</span> 122分鐘</p>
  </div>
</div>
</body></html>`))
	}))
	defer server.Close()

	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	info, err := NewJavBus(client, server.URL).Parse(context.Background(), crawler.MovieRef{DVDID: "MIKR-069"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if info.DVDID != "MIKR-069" {
		t.Fatalf("DVDID = %q, want MIKR-069", info.DVDID)
	}
}

func TestJavDBParsesDVDIDAcrossNestedPrefixMarkup(t *testing.T) {
	doc, err := htmlquery.Parse(strings.NewReader(`
<html><body><section><div><div class="video-detail">
  <h2><strong class="current-title">MIKR-069 Current Markup Title</strong></h2>
  <nav class="panel movie-panel-info">
    <div class="panel-block">
      <strong>番號:</strong>
      &nbsp;<span class="value"><a href="/video_codes/MIKR">MIKR</a>-069</span>
    </div>
  </nav>
</div></div></section></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	info, err := NewJavDB(client, "https://javdb.example").parseDetail("https://javdb.example/v/test", doc, "MIKR-069")
	if err != nil {
		t.Fatalf("parseDetail returned error: %v", err)
	}
	if info.DVDID != "MIKR-069" {
		t.Fatalf("DVDID = %q, want MIKR-069", info.DVDID)
	}
}
