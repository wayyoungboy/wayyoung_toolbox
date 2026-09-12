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

type JavBus struct {
	client  *httpclient.Client
	baseURL string
}

func NewJavBus(client *httpclient.Client, baseURL string) *JavBus {
	return &JavBus{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

func (j *JavBus) ID() model.CrawlerID {
	return "javbus"
}

func (j *JavBus) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	doc, err := j.client.GetHTML(ctx, j.baseURL+"/"+input.DVDID)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		return nil, err
	}
	if strings.Contains(firstText(doc, "/html/head/title"), "404 Page Not Found") {
		return nil, crawler.ErrMovieNotFound
	}

	container := htmlquery.FindOne(doc, "//div[@class='container']")
	if container == nil {
		return nil, crawler.ErrMovieNotFound
	}
	rawTitle := firstText(container, "h3")
	title := strings.TrimSpace(strings.Replace(rawTitle, input.DVDID, "", 1))
	cover := absURL(j.baseURL, firstAttr(container, "//a[@class='bigImage']/img", "src"))
	previewPics := make([]string, 0)
	for _, href := range allAttrs(container, "//div[@id='sample-waterfall']/a", "href") {
		previewPics = append(previewPics, absURL(j.baseURL, href))
	}

	infoNode := htmlquery.FindOne(container, "//div[@class='col-md-3 info']")
	if infoNode == nil {
		return nil, crawler.ErrMovieNotFound
	}

	dvdid := firstText(infoNode, "p/span[text()='識別碼:']/following-sibling::*[1]")
	if dvdid == "" {
		dvdid = input.DVDID
	}
	publishDate := firstText(infoNode, "p/span[text()='發行日期:']/following-sibling::text()")
	duration := strings.TrimSpace(strings.ReplaceAll(firstText(infoNode, "p/span[text()='長度:']/following-sibling::text()"), "分鐘", ""))
	director := firstText(infoNode, "p/span[text()='導演:']/following-sibling::a")
	producer := firstText(infoNode, "p/span[text()='製作商:']/following-sibling::a")
	publisher := firstText(infoNode, "p/span[text()='發行商:']/following-sibling::a")
	if publisher == producer {
		publisher = ""
	}
	serial := firstText(infoNode, "p/span[text()='系列:']/following-sibling::a")

	var genre []string
	var genreID []string
	for _, tag := range htmlquery.Find(infoNode, "//span[@class='genre']/label/a") {
		text := strings.TrimSpace(htmlquery.InnerText(tag))
		if text != "" {
			genre = append(genre, text)
		}
		href := htmlquery.SelectAttr(tag, "href")
		if href != "" {
			genreID = append(genreID, lastPathSegment(href))
		}
	}

	actressPics := make(map[string]string)
	var actress []string
	for _, tag := range htmlquery.Find(doc, "//a[@class='avatar-box']/div/img") {
		name := strings.TrimSpace(htmlquery.SelectAttr(tag, "title"))
		if name == "" {
			continue
		}
		actress = append(actress, name)
		if src := htmlquery.SelectAttr(tag, "src"); src != "" {
			actressPics[name] = absURL(j.baseURL, src)
		}
	}

	uncensored := regexp.MustCompile(`^\d{6}-\d{2,3}`).MatchString(dvdid)
	return &model.MovieInfo{
		DVDID:       dvdid,
		URL:         j.baseURL + "/" + input.DVDID,
		Cover:       cover,
		Genre:       genre,
		GenreID:     genreID,
		Title:       title,
		Serial:      serial,
		Actress:     actress,
		ActressPics: actressPics,
		Director:    director,
		Duration:    duration,
		Producer:    producer,
		Publisher:   publisher,
		Uncensored:  &uncensored,
		PublishDate: publishDate,
		PreviewPics: previewPics,
	}, nil
}
