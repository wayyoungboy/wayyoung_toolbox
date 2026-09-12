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

type Prestige struct {
	client  *httpclient.Client
	baseURL string
}

func NewPrestige(client *httpclient.Client, baseURL string) *Prestige {
	return &Prestige{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

func (p *Prestige) ID() model.CrawlerID {
	return "prestige"
}

func (p *Prestige) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	pageURL := p.baseURL + "/goods/goods_detail.php?sku=" + input.DVDID
	doc, err := p.client.GetHTML(ctx, pageURL)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		return nil, err
	}
	container := htmlquery.FindOne(doc, "//section[@class='px-4 mb-4 md:px-8 md:mb-16']")
	if container == nil {
		return nil, crawler.ErrMovieNotFound
	}

	title := strings.TrimSpace(tailText(htmlquery.FindOne(container, "h1/span")))
	cover := splitQuery(absURL(pageURL, firstAttr(container, "//div[@class='c-ratio-image mr-8']//img", "src")))
	actress := allTexts(container, "//p[text()='出演者：']/following-sibling::div/p/a")
	for i := range actress {
		actress[i] = strings.ReplaceAll(strings.TrimSpace(actress[i]), " ", "")
	}
	duration := regexp.MustCompile(`\d+`).FindString(firstText(container, "//p[text()='収録時間：']/following-sibling::*[1]"))
	publishDate := ""
	if dateURL := firstAttr(container, "//p[text()='発売日：']/following-sibling::div/a", "href"); strings.Contains(dateURL, "?date=") {
		publishDate = strings.Split(dateURL, "?date=")[1]
	}
	previewPics := make([]string, 0)
	for _, src := range allAttrs(container, "//h2[text()='サンプル画像']/following-sibling::div/div//img", "src") {
		previewPics = append(previewPics, splitQuery(absURL(pageURL, src)))
	}
	uncensored := false
	return &model.MovieInfo{
		DVDID:       firstText(container, "//p[text()='品番：']/following-sibling::div/p"),
		URL:         pageURL,
		Plot:        firstText(container, "//h2[text()='商品紹介']/following-sibling::div/p"),
		Cover:       cover,
		Genre:       allTexts(container, "//p[text()='ジャンル：']/following-sibling::div[1]/a"),
		Title:       title,
		Serial:      firstText(container, "//p[text()='レーベル：']/following-sibling::div/a"),
		Actress:     actress,
		Duration:    duration,
		Producer:    firstText(container, "//p[text()='メーカー：']/following-sibling::div/a"),
		Uncensored:  &uncensored,
		PublishDate: publishDate,
		PreviewPics: previewPics,
	}, nil
}

func splitQuery(rawURL string) string {
	if idx := strings.Index(rawURL, "?"); idx >= 0 {
		return rawURL[:idx]
	}
	return rawURL
}
