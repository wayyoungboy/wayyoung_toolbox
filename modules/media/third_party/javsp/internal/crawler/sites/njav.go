package sites

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/antchfx/htmlquery"
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
	"github.com/yuukiy/javsp-go/internal/util"
)

type NJAV struct {
	client  *httpclient.Client
	baseURL string
}

func NewNJAV(client *httpclient.Client, baseURL string) *NJAV {
	return &NJAV{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

func (n *NJAV) ID() model.CrawlerID {
	return "njav"
}

func (n *NJAV) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	videoURL, err := n.search(ctx, input.DVDID)
	if err != nil {
		return nil, err
	}
	doc, err := n.client.GetHTML(ctx, videoURL)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		return nil, err
	}
	container := htmlquery.FindOne(doc, "//div[@class='container']/div/div[@class='col']")
	if container == nil {
		return nil, crawler.ErrMovieNotFound
	}
	title := firstText(container, "//div[@class='d-flex justify-content-between align-items-start']/div/h1")
	plot := strings.Join(allTexts(container, "//div[@class='description']/p"), " ")
	magnet := allAttrs(container, "//div[@class='magnet']/a", "href")
	var genre []string
	var actress []string
	var serial string
	var producer string
	var realID string
	var publishDate string
	var durationText string
	for _, item := range htmlquery.Find(container, "//div[@class='detail-item']/div") {
		itemTitle := firstText(item, "span[1]")
		switch {
		case strings.Contains(itemTitle, "タグ:"), strings.Contains(itemTitle, "ジャンル:"), strings.Contains(itemTitle, "レーベル:"):
			genre = append(genre, allTexts(item, "span[2]/a")...)
		case strings.Contains(itemTitle, "女優:"):
			actress = allTexts(item, "span[2]/a")
		case strings.Contains(itemTitle, "シリーズ:"):
			serial = firstText(item, "span[2]/a")
		case strings.Contains(itemTitle, "メーカー:"):
			producer = firstText(item, "span[2]/a")
		case strings.Contains(itemTitle, "コード:"):
			realID = firstText(item, "span[2]/text()")
		case strings.Contains(itemTitle, "公開日:"):
			publishDate = firstText(item, "span[2]/text()")
		case strings.Contains(itemTitle, "再生時間:"):
			durationText = firstText(item, "span[2]/text()")
		}
	}
	for _, keyword := range titleRemovalKeywords(input.DVDID, realID) {
		if keyword != "" {
			title = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(keyword)).ReplaceAllString(title, "")
		}
	}
	duration := ""
	if durationText != "" {
		if minutes, err := util.StrftimeToMinutes(durationText); err == nil {
			duration = strconv.Itoa(minutes)
		}
	}
	uncensored := false
	for _, value := range append(append([]string{}, magnet...), title) {
		if strings.Contains(strings.ToLower(value), "uncensored") {
			uncensored = true
		}
	}
	return &model.MovieInfo{
		DVDID:       realID,
		URL:         videoURL,
		Plot:        plot,
		Cover:       absURL(videoURL, firstAttr(container, "//div[@id='player']", "data-poster")),
		Genre:       genre,
		Title:       title,
		Magnet:      magnet,
		Serial:      serial,
		Actress:     actress,
		Duration:    duration,
		Producer:    producer,
		Uncensored:  &uncensored,
		PublishDate: publishDate,
	}, nil
}

func (n *NJAV) search(ctx context.Context, dvdid string) (string, error) {
	searchURL := n.baseURL + "/search?keyword=" + url.QueryEscape(dvdid)
	doc, err := n.client.GetHTML(ctx, searchURL)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return "", crawler.ErrSiteBlocked
		}
		return "", err
	}
	for _, item := range htmlquery.Find(doc, "//div[@class='box-item']/div[@class='detail']/a") {
		searchTitle := strings.TrimSpace(htmlquery.InnerText(item))
		if strings.Contains(searchTitle, dvdid) ||
			(strings.HasPrefix(dvdid, "FC2-") && strings.Contains(searchTitle, "FC2") && strings.Contains(searchTitle, strings.TrimPrefix(dvdid, "FC2-"))) {
			return absURL(searchURL, htmlquery.SelectAttr(item, "href")), nil
		}
	}
	return "", crawler.ErrMovieNotFound
}

func titleRemovalKeywords(dvdid string, realID string) []string {
	keywords := []string{realID, " "}
	if strings.HasPrefix(dvdid, "FC2") {
		keywords = append(keywords, "FC2", "PPV", "-", strings.Split(dvdid, "-")[len(strings.Split(dvdid, "-"))-1])
	}
	return keywords
}
