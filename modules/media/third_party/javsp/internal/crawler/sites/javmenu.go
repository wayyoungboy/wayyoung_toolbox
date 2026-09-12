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

type JavMenu struct {
	client  *httpclient.Client
	baseURL string
}

func NewJavMenu(client *httpclient.Client, baseURL string) *JavMenu {
	return &JavMenu{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

func (j *JavMenu) ID() model.CrawlerID {
	return "javmenu"
}

func (j *JavMenu) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	pageURL := j.baseURL + "/" + input.DVDID
	doc, err := j.client.GetHTML(ctx, pageURL)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		return nil, err
	}
	container := htmlquery.FindOne(doc, "//div[@class='col-md-9 px-0']")
	if container == nil {
		return structuredVideo(doc, pageURL, input.DVDID, false)
	}
	title := firstText(container, "div[@class='col-12 mb-3']/h1/strong")
	title = strings.ReplaceAll(title, "  | JAV目錄大全 | 每日更新", "")
	title = strings.ReplaceAll(title, " 免費AV在線看", "")
	title = strings.ReplaceAll(title, " 免費在線看", "")
	title = strings.TrimSpace(strings.ReplaceAll(title, input.DVDID, ""))

	cover := ""
	if video := htmlquery.FindOne(container, "//div[@class='single-video']/video"); video != nil {
		cover = strings.TrimSpace(htmlquery.SelectAttr(video, "data-poster"))
	}
	if cover == "" {
		cover = strings.TrimSpace(firstAttr(container, "//img[@class='lazy rounded']", "data-src"))
	}
	previewPics := make([]string, 0)
	for _, href := range allAttrs(container, "//a[@data-fancybox='gallery']", "href") {
		previewPics = append(previewPics, absURL(pageURL, href))
	}
	if cover == "" && len(previewPics) > 0 {
		cover = previewPics[0]
	}
	cover = absURL(pageURL, strings.TrimSpace(cover))

	infoNode := htmlquery.FindOne(container, "//div[@class='card-body']")
	if infoNode == nil {
		return nil, crawler.ErrMovieNotFound
	}
	var genre []string
	var genreID []string
	for _, tag := range htmlquery.Find(infoNode, "//a[@class='genre']") {
		text := strings.TrimSpace(htmlquery.InnerText(tag))
		if text != "" {
			genre = append(genre, text)
		}
		href := strings.Trim(htmlquery.SelectAttr(tag, "href"), "/")
		parts := strings.Split(href, "/")
		if len(parts) >= 3 {
			genreID = append(genreID, parts[len(parts)-3]+"/"+parts[len(parts)-1])
		}
	}
	var magnets []string
	for _, href := range allAttrs(container, "//table[contains(@class, 'magnet-table')]/tbody/tr/td/a", "href") {
		magnets = append(magnets, strings.ReplaceAll(href, "[javdb.com]", ""))
	}
	return &model.MovieInfo{
		DVDID:       input.DVDID,
		URL:         pageURL,
		Cover:       cover,
		Genre:       genre,
		GenreID:     genreID,
		Title:       title,
		Magnet:      magnets,
		Actress:     allTexts(infoNode, "div/span[contains(text(), '女優:')]/following-sibling::*/a"),
		Duration:    strings.ReplaceAll(firstText(infoNode, "div/span[contains(text(), '時長:')]/following-sibling::*[1]"), "分鐘", ""),
		Producer:    firstText(infoNode, "div/span[contains(text(), '製作:')]/following-sibling::a/span"),
		PublishDate: firstText(infoNode, "div/span[contains(text(), '日期:')]/following-sibling::*[1]"),
		PreviewPics: previewPics,
	}, nil
}
