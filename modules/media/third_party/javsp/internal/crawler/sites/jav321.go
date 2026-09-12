package sites

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/antchfx/htmlquery"
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
)

type Jav321 struct {
	client  *httpclient.Client
	baseURL string
}

func NewJav321(client *httpclient.Client, baseURL string) *Jav321 {
	return &Jav321{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

func (j *Jav321) ID() model.CrawlerID {
	return "jav321"
}

func (j *Jav321) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	resp, err := j.client.PostForm(ctx, j.baseURL+"/search", url.Values{"sn": []string{input.DVDID}})
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		return nil, err
	}
	defer resp.Body.Close()
	doc, err := htmlquery.Parse(resp.Body)
	if err != nil {
		return nil, err
	}

	pageURLRaw := firstAttr(doc, "//ul[@class='dropdown-menu']/li/a", "href")
	pageURL := absURL(j.baseURL+"/search", pageURLRaw)
	cid := lastPathSegment(pageURL)
	if cid == "" || cid == "search" {
		return nil, crawler.ErrMovieNotFound
	}

	infoNode := htmlquery.FindOne(doc, "//div[@class='col-md-9']")
	if infoNode == nil {
		return nil, crawler.ErrMovieNotFound
	}
	title := firstText(doc, "//div[@class='panel-heading']/h3")
	company := allTexts(infoNode, "a[contains(@href,'/company/')]")
	producer := ""
	if len(company) > 0 {
		producer = company[0]
	}

	var actress []string
	actressPics := make(map[string]string)
	for _, img := range htmlquery.Find(doc, "//div[@class='thumbnail']/a[contains(@href,'/star/')]/img") {
		name := tailText(img)
		if name == "" {
			continue
		}
		actress = append(actress, name)
		if src := htmlquery.SelectAttr(img, "src"); src != "" {
			actressPics[name] = absURL(pageURL, src)
		}
	}

	var genre []string
	var genreID []string
	for _, tag := range htmlquery.Find(infoNode, "a[contains(@href,'/genre/')]") {
		text := strings.TrimSpace(htmlquery.InnerText(tag))
		if text != "" {
			genre = append(genre, text)
		}
		if href := htmlquery.SelectAttr(tag, "href"); href != "" {
			genreID = append(genreID, pathSegmentFromEnd(href, 1))
		}
	}

	dvdid := strings.ToUpper(strings.TrimPrefix(firstText(infoNode, "b[text()='品番']/following-sibling::text()"), ": "))
	publishDate := strings.TrimPrefix(firstText(infoNode, "b[text()='配信開始日']/following-sibling::text()"), ": ")
	duration := ""
	if durationText := firstText(infoNode, "b[text()='収録時間']/following-sibling::text()"); durationText != "" {
		duration = regexp.MustCompile(`\d+`).FindString(durationText)
	}
	score := ""
	if scoreImage := firstAttr(infoNode, "//b[text()='平均評価']/following-sibling::img", "data-original"); scoreImage != "" {
		if digits := regexp.MustCompile(`\d{2}`).FindString(scoreImage); digits != "" {
			value, _ := strconv.Atoi(digits)
			score = fmt.Sprintf("%.1f", float64(value)/5)
		}
	}
	serial := firstText(infoNode, "a[contains(@href,'/series/')]")
	previewVideo := absURL(pageURL, firstAttr(infoNode, "//video/source", "src"))
	plot := firstText(doc, "//div[@class='panel-body']/div[@class='row']/div[@class='col-md-12']/text()")

	previewPics := allAttrs(doc, "//div[@class='col-xs-12 col-md-12']/p/a/img[@class='img-responsive']", "src")
	if len(previewPics) == 0 {
		previewPics = allAttrs(doc, "//div/div/div[@class='col-md-3']/img[@onerror and @class='img-responsive']", "src")
	}
	for i, pic := range previewPics {
		previewPics[i] = cleanURLSlashes(absURL(pageURL, pic))
	}
	cover := ""
	if len(previewPics) > 0 {
		cover = previewPics[0]
		previewPics = previewPics[1:]
	}

	return &model.MovieInfo{
		DVDID:        dvdid,
		CID:          cid,
		URL:          pageURL,
		Plot:         plot,
		Cover:        cover,
		Genre:        genre,
		GenreID:      genreID,
		Score:        score,
		Title:        title,
		Serial:       serial,
		Actress:      actress,
		ActressPics:  actressPics,
		Duration:     duration,
		Producer:     producer,
		PublishDate:  publishDate,
		PreviewPics:  previewPics,
		PreviewVideo: previewVideo,
	}, nil
}
