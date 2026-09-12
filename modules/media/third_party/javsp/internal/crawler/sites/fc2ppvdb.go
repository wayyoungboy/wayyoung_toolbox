package sites

import (
	"context"
	"errors"
	"strings"

	"github.com/antchfx/htmlquery"
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
)

type FC2PPVDB struct {
	client  *httpclient.Client
	baseURL string
}

func NewFC2PPVDB(client *httpclient.Client, baseURL string) *FC2PPVDB {
	return &FC2PPVDB{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

func (f *FC2PPVDB) ID() model.CrawlerID {
	return "fc2ppvdb"
}

func (f *FC2PPVDB) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	fc2ID := strings.TrimPrefix(strings.ToUpper(input.DVDID), "FC2-")
	pageURL := f.baseURL + "/articles/" + fc2ID
	doc, err := f.client.GetHTML(ctx, pageURL)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		return nil, err
	}
	container := htmlquery.FindOne(doc, "//div[@class='container lg:px-5 px-2 py-12 mx-auto']/div[1]")
	if container == nil {
		return nil, crawler.ErrMovieNotFound
	}
	title := firstText(container, "//h2/a")
	if title == "" {
		return nil, crawler.ErrMovieNotFound
	}
	cover := absURL(pageURL, firstAttr(container, "//img[@alt='"+fc2ID+"']", "src"))
	duration := firstText(container, "//div[starts-with(text(),'収録時間：')]/span")
	actress := allTexts(container, "//div[starts-with(text(),'女優：')]/span/a")
	genre := allTexts(container, "//div[starts-with(text(),'タグ：')]/span/a")
	publishDate := firstText(container, "//div[starts-with(text(),'販売日：')]/span")
	publisher := firstText(container, "//div[starts-with(text(),'販売者：')]/span/a")
	uncensored := firstText(container, "//div[starts-with(text(),'モザイク：')]/span") == "無"
	previewVideo := absURL(pageURL, firstAttr(container, "//a[starts-with(text(),'サンプル動画')]", "href"))
	return &model.MovieInfo{
		DVDID:        input.DVDID,
		URL:          pageURL,
		Cover:        cover,
		Genre:        genre,
		Title:        title,
		Actress:      actress,
		Duration:     duration,
		Publisher:    publisher,
		Uncensored:   &uncensored,
		PublishDate:  publishDate,
		PreviewVideo: previewVideo,
	}, nil
}
