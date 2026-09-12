package sites

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/antchfx/htmlquery"
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
)

type DLGetchu struct {
	client  *httpclient.Client
	baseURL string
}

func NewDLGetchu(client *httpclient.Client, baseURL string) *DLGetchu {
	return &DLGetchu{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

func (d *DLGetchu) ID() model.CrawlerID {
	return "dl_getchu"
}

func (d *DLGetchu) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	id := strings.ToUpper(input.DVDID)
	if !strings.HasPrefix(id, "GETCHU-") {
		return nil, crawler.ErrMovieNotFound
	}
	getchuID := strings.TrimPrefix(id, "GETCHU-")
	pageURL := d.baseURL + "/i/item" + getchuID
	doc, err := d.client.GetHTML(ctx, pageURL)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		return nil, err
	}

	data := make(map[string][]string)
	for _, row := range htmlquery.Find(doc, "//td[@class='bluetext']/parent::tr") {
		key := firstText(row, "td[@class='bluetext']")
		if key == "" {
			continue
		}
		values := allTexts(row, "td[2]/a")
		if len(values) == 0 {
			values = allTexts(row, "td[2]/text()")
		}
		data[key] = values
	}

	duration := ""
	if values := data["画像数&ページ数"]; len(values) > 0 {
		duration = regexp.MustCompile(`(?:動画)?(\d+)分`).FindStringSubmatch(strings.Join(values, " "))[1]
	}
	plot := ""
	if values := data["作品内容"]; len(values) > 0 {
		joined := strings.Join(values, "")
		if idx := strings.Index(joined, "※"); idx >= 0 {
			plot = joined[:idx]
		} else {
			plot = joined
		}
	}

	cover := ""
	for _, src := range allAttrs(doc, "//img[contains(@src, '"+getchuID+"top.jpg')]", "src") {
		cover = absURL(pageURL, src)
		break
	}
	var previews []string
	for _, src := range allAttrs(doc, "//img[contains(@src, '"+getchuID+"_')]", "src") {
		previews = append(previews, absURL(pageURL, src))
	}
	return &model.MovieInfo{
		DVDID:       id,
		URL:         pageURL,
		Plot:        plot,
		Cover:       cover,
		Genre:       data["趣向"],
		Title:       firstText(doc, "//form[@action='https://dl.getchu.com/cart/']/div/table[2]//td/div"),
		Actress:     trimSlice(data["作者"]),
		Duration:    duration,
		Producer:    firstValue(data["サークル"]),
		PublishDate: strings.ReplaceAll(firstValue(data["配信開始日"]), "/", "-"),
		PreviewPics: previews,
	}, nil
}

func firstValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func trimSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
