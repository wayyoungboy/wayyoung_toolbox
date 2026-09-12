package sites

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/antchfx/htmlquery"
	"github.com/yuukiy/javsp-go/internal/avid"
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
)

type AVSox struct {
	client  *httpclient.Client
	baseURL string
}

func NewAVSox(client *httpclient.Client, baseURL string) *AVSox {
	return &AVSox{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

func (a *AVSox) ID() model.CrawlerID {
	return "avsox"
}

func (a *AVSox) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	fullID := input.DVDID
	if regexp.MustCompile(`^\d{6}-\d{2,3}$`).MatchString(fullID) {
		fullID = strings.ReplaceAll(fullID, "-", "_")
	}
	searchURL := a.baseURL + "/tw/search/" + fullID
	searchDoc, err := a.client.GetHTML(ctx, searchURL)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		return nil, err
	}

	ids := htmlquery.Find(searchDoc, "//div[@class='photo-info']/span/date[1]")
	urls := allAttrs(searchDoc, "//a[contains(@class, 'movie-box')]", "href")
	index := -1
	for i, idNode := range ids {
		if strings.EqualFold(strings.TrimSpace(htmlquery.InnerText(idNode)), input.DVDID) {
			index = i
			break
		}
	}
	if index < 0 || index >= len(urls) {
		return nil, crawler.ErrMovieNotFound
	}

	detailURL := absURL(a.baseURL, urls[index])
	doc, err := a.client.GetHTML(ctx, detailURL)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		return nil, err
	}
	container := htmlquery.FindOne(doc, "/html/body/div[@class='container']")
	if container == nil {
		return nil, crawler.ErrMovieNotFound
	}
	rawTitle := firstText(container, "h3")
	title := strings.TrimSpace(strings.Replace(rawTitle, input.DVDID, "", 1))
	cover := absURL(detailURL, firstAttr(container, "//a[@class='bigImage']", "href"))
	infoNode := htmlquery.FindOne(container, "div/div[@class='col-md-3 info']")
	if infoNode == nil {
		return nil, crawler.ErrMovieNotFound
	}

	dvdid := firstText(infoNode, "p/span[@style]")
	// AVSox has used detail-page markup where the label prefix sits outside the
	// styled span and only "-822" remains inside it. The search result above
	// already matched the full requested ID, so keep that trusted ID whenever
	// the detail value is incomplete or identifies a different movie.
	requestedID := avid.GetID(input.DVDID, nil)
	detailID := avid.GetID(dvdid, nil)
	if detailID == "" || (requestedID != "" && !strings.EqualFold(detailID, requestedID)) {
		dvdid = input.DVDID
	}
	publishDate := firstText(infoNode, "p/span[text()='发行时间:']/following-sibling::text()")
	duration := strings.TrimSpace(strings.ReplaceAll(firstText(infoNode, "p/span[text()='长度:']/following-sibling::text()"), "分钟", ""))
	producer := firstText(infoNode, "p[contains(text(),'制作商')]/following-sibling::*[1]/a")
	serial := firstText(infoNode, "p[contains(text(),'系列')]/following-sibling::*[1]/a")

	var genre []string
	for _, tag := range htmlquery.Find(infoNode, "p/span[@class='genre']/a") {
		if value := strings.TrimSpace(htmlquery.InnerText(tag)); value != "" {
			genre = append(genre, value)
		}
	}
	var actress []string
	for _, tag := range htmlquery.Find(container, "//a[@class='avatar-box']/span") {
		if value := strings.TrimSpace(htmlquery.InnerText(tag)); value != "" {
			actress = append(actress, value)
		}
	}
	uncensored := regexp.MustCompile(`^\d{6}[-_]\d{2,3}`).MatchString(input.DVDID)
	return &model.MovieInfo{
		DVDID:       dvdid,
		URL:         detailURL,
		Cover:       cover,
		Genre:       genre,
		Title:       title,
		Serial:      serial,
		Actress:     actress,
		Duration:    duration,
		Producer:    producer,
		Uncensored:  &uncensored,
		PublishDate: publishDate,
	}, nil
}
