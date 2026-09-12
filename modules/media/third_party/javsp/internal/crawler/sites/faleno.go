package sites

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
	"golang.org/x/net/html"
)

type Faleno struct {
	client  *httpclient.Client
	baseURL string
}

func NewFaleno(client *httpclient.Client, baseURL string) *Faleno {
	return &Faleno{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

func (f *Faleno) ID() model.CrawlerID {
	return "faleno"
}

func (f *Faleno) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	dvdID := strings.ToUpper(strings.TrimSpace(input.DVDID))
	slug := strings.ToLower(regexp.MustCompile(`[^a-zA-Z0-9]+`).ReplaceAllString(dvdID, ""))
	if slug == "" {
		return nil, crawler.ErrMovieNotFound
	}
	pageURL := f.baseURL + "/works/" + slug + "/"
	doc, err := f.client.GetHTML(ctx, pageURL)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		if isHTTPStatus(err, http.StatusNotFound) {
			return nil, crawler.ErrMovieNotFound
		}
		return nil, err
	}

	title := firstText(doc, "//div[contains(@class,'bar02')]/h1")
	if title == "" {
		return nil, crawler.ErrMovieNotFound
	}
	cover := absURL(pageURL, firstAttr(doc, "//div[contains(@class,'box_works01_img')]/a/img", "src"))
	actress := falenoDetail(doc, "出演女優")
	actresses := []string(nil)
	if actress != "" {
		actresses = []string{actress}
	}
	duration := regexp.MustCompile(`\d+`).FindString(falenoDetail(doc, "収録時間"))
	publishDate := normalizeFalenoDate(falenoDetail(doc, "配信日"))
	if publishDate == "" {
		publishDate = normalizeFalenoDate(falenoDetail(doc, "発売日"))
	}
	previewPics := allAttrs(doc, "//div[contains(@class,'box_works01_ga')]//a[contains(@class,'pop_img')]", "href")
	for i := range previewPics {
		previewPics[i] = absURL(pageURL, previewPics[i])
	}

	uncensored := false
	return &model.MovieInfo{
		DVDID:        dvdID,
		URL:          pageURL,
		Plot:         firstText(doc, "//div[contains(@class,'box_works01_text')]/p"),
		Cover:        cover,
		Title:        title,
		Actress:      actresses,
		Duration:     duration,
		Producer:     "FALENO",
		Publisher:    "FALENO",
		Uncensored:   &uncensored,
		PublishDate:  publishDate,
		PreviewPics:  previewPics,
		PreviewVideo: absURL(pageURL, firstAttr(doc, "//div[contains(@class,'box_works01_img')]/a[contains(@class,'pop_sample')]", "href")),
	}, nil
}

func falenoDetail(node *html.Node, label string) string {
	expr := "//div[contains(@class,'box_works01_list')]//li[span[normalize-space(.)='" + label + "']]/p[1]"
	return firstText(node, expr)
}

func normalizeFalenoDate(value string) string {
	parsed, err := time.Parse("2006/1/2", strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return parsed.Format("2006-01-02")
}
