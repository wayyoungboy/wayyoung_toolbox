package sites

import (
	"context"
	"strings"

	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
)

// FC2DB reads public structured metadata without account cookies or API keys.
type FC2DB struct {
	client  *httpclient.Client
	baseURL string
}

func NewFC2DB(client *httpclient.Client, baseURL string) *FC2DB {
	return &FC2DB{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

func (f *FC2DB) ID() model.CrawlerID { return "fc2db" }

func (f *FC2DB) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	match := fc2MetadataID.FindStringSubmatch(strings.TrimSpace(input.DVDID))
	if match == nil {
		return nil, crawler.ErrMovieNotFound
	}
	pageURL := f.baseURL + "/work/" + match[1] + "/"
	doc, err := f.client.GetHTML(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	return structuredVideo(doc, pageURL, "FC2-"+match[1], true)
}
