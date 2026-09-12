package sites

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
)

func TestJavBusParseDoesNotFallIntoAgeVerification(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/MIKR-069" {
			t.Fatalf("path = %q, want /MIKR-069", r.URL.Path)
		}
		if r.Header.Get("Accept-Language") != "zh-CN,zh;q=0.9,en;q=0.8" {
			_, _ = w.Write([]byte(`<html><head><title>JavBus</title></head><body><form action="/age.php">Age Verification</form></body></html>`))
			return
		}
		_, _ = w.Write([]byte(`
<html><head><title>MIKR-069</title></head><body>
<div class="container">
  <h3>MIKR-069 Regression Title</h3>
  <a class="bigImage"><img src="/cover.jpg"></a>
  <div class="col-md-3 info">
    <p><span>識別碼:</span> MIKR-069</p>
    <p><span>發行日期:</span> 2024-01-02</p>
    <p><span>長度:</span> 120分鐘</p>
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
	if info.DVDID != "MIKR-069" || info.Title != "Regression Title" {
		t.Fatalf("parsed info = %#v", info)
	}
}
