package sites

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
)

// TestFC2LiveMetadata is opt-in and only fetches metadata pages. It never reads
// media files, downloads covers, translates text, or invokes the organizer.
func TestFC2LiveMetadata(t *testing.T) {
	if os.Getenv("JAVSP_FC2_LIVE") != "1" {
		t.Skip("set JAVSP_FC2_LIVE=1 to run read-only external metadata probes")
	}
	client, err := httpclient.New(httpclient.Options{
		ProxyURL: os.Getenv("JAVSP_FC2_LIVE_PROXY"), Timeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatal("invalid probe HTTP configuration")
	}
	for _, parser := range []crawler.Crawler{
		NewJavMenu(client, "https://mrzyx.xyz"), NewFC2DB(client, "https://fc2db.net"),
	} {
		t.Run(string(parser.ID()), func(t *testing.T) {
			t.Parallel()
			for index, id := range []string{"FC2-4419625", "FC2-4419627", "FC2-4420953", "FC2-4421745", "FC2-4432303", "FC2-4434719"} {
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				info, err := parser.Parse(ctx, crawler.MovieRef{DVDID: id})
				cancel()
				t.Logf("%s %s", id, crawler.Diagnostics([]crawler.Result{{CrawlerID: parser.ID(), Info: info, Err: err}}))
				minimumExpected := index < 2 || (index == 2 && parser.ID() == model.CrawlerID("javmenu"))
				if minimumExpected && (err != nil || info == nil || info.DVDID != id || info.Title == "" || info.Cover == "") {
					t.Errorf("%s did not provide exact title and cover metadata", id)
				}
			}
		})
	}
}
