package sites

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/antchfx/htmlquery"
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
	"golang.org/x/net/html"
)

type Fanza struct {
	client      *httpclient.Client
	baseURL     string
	hardworking bool
}

func NewFanza(client *httpclient.Client, baseURL string, hardworking bool) *Fanza {
	return &Fanza{client: client, baseURL: strings.TrimRight(baseURL, "/"), hardworking: hardworking}
}

func (f *Fanza) ID() model.CrawlerID {
	return "fanza"
}

func (f *Fanza) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	cid := input.CID
	if cid == "" {
		cid = input.DVDID
	}
	pageURL := f.baseURL + "/digital/videoa/-/detail/=/cid=" + cid + "/"
	doc, err := f.client.GetHTML(ctx, pageURL)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		if isHTTPStatus(err, http.StatusNotFound) {
			return f.parseSearchFallback(ctx, cid)
		}
		return nil, err
	}
	return f.parseVideoa(pageURL, doc)
}

func (f *Fanza) parseSearchFallback(ctx context.Context, cid string) (*model.MovieInfo, error) {
	searchURL := f.baseURL + "/search/?redirect=1&enc=UTF-8&category=&searchstr=" + url.QueryEscape(cid) + "&commit.x=0&commit.y=0"
	doc, err := f.client.GetHTML(ctx, searchURL)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		if isHTTPStatus(err, http.StatusNotFound) {
			return nil, crawler.ErrMovieNotFound
		}
		return nil, err
	}
	items := f.searchResults(searchURL, doc, cid)
	if len(items) == 0 {
		return nil, crawler.ErrMovieNotFound
	}
	sort.SliceStable(items, func(i, j int) bool {
		left := fanzaProductPriority[items[i].product]
		right := fanzaProductPriority[items[j].product]
		if left != right {
			return left > right
		}
		return fanzaTypePriority[items[i].pageType] > fanzaTypePriority[items[j].pageType]
	})

	var lastErr error
	for _, item := range items {
		parser := f.parserForType(item.pageType)
		if parser == nil {
			continue
		}
		detailDoc, err := f.client.GetHTML(ctx, item.url)
		if err != nil {
			lastErr = err
			continue
		}
		info, err := parser(item.url, detailDoc)
		if err != nil {
			lastErr = err
			continue
		}
		return info, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, crawler.ErrMovieNotFound
}

func (f *Fanza) parseVideoa(pageURL string, node *html.Node) (*model.MovieInfo, error) {
	title := firstText(node, "//div[@class='hreview']/h1")
	container := htmlquery.FindOne(node, "//table[@class='mg-b12']/tr/td")
	if container == nil {
		container = node
	}
	if title == "" {
		return nil, crawler.ErrMovieNotFound
	}
	duration := regexp.MustCompile(`\d+`).FindString(firstText(container, "//td[text()='収録時間：']/following-sibling::td/text()"))
	genre, genreID := fanzaGenre(container, "//td[text()='ジャンル：']/following-sibling::td/a[contains(@href,'?keyword=') or contains(@href,'article=keyword')]")
	score := ""
	if raw := firstText(container, "//p[@class='d-review__average']/strong"); raw != "" {
		value, _ := strconv.ParseFloat(regexp.MustCompile(`\d+`).FindString(raw), 64)
		score = fmt.Sprintf("%.2f", value*2)
	}
	uncensored := false
	return &model.MovieInfo{
		CID:         firstText(container, "//td[text()='品番：']/following-sibling::td/text()"),
		URL:         pageURL,
		Plot:        firstText(container, "//div[contains(@class, 'mg-b20 lh4')]/text()"),
		Cover:       firstAttr(container, "//div[@id='sample-video']/a", "href"),
		Genre:       genre,
		GenreID:     genreID,
		Score:       score,
		Title:       title,
		Serial:      firstText(container, "//td[text()='シリーズ：']/following-sibling::td/a"),
		Actress:     allTexts(container, "//span[@id='performer']/a"),
		Director:    firstText(container, "//td[text()='監督：']/following-sibling::td/a"),
		Duration:    duration,
		Producer:    firstText(container, "//td[text()='メーカー：']/following-sibling::td/a"),
		Uncensored:  &uncensored,
		PublishDate: strings.ReplaceAll(firstText(container, "//td[text()='配信開始日：']/following-sibling::td/text()"), "/", "-"),
		PreviewPics: allAttrs(container, "//a[@name='sample-image']/img", "src"),
	}, nil
}

func (f *Fanza) parseAnime(pageURL string, node *html.Node) (*model.MovieInfo, error) {
	title := firstText(node, "//h1[@id='title']/text()")
	container := htmlquery.FindOne(node, "//table[@class='mg-b12']/tr/td")
	if container == nil {
		container = node
	}
	if title == "" {
		return nil, crawler.ErrMovieNotFound
	}
	duration := strings.TrimSpace(strings.ReplaceAll(firstText(container, "//td[text()='収録時間：']/following-sibling::td/text()"), "分", ""))
	genre, genreID := fanzaGenre(container, "//td[text()='ジャンル：']/following-sibling::td/a[contains(@href,'article=keyword')]")
	score := ""
	if scoreImage := firstAttr(container, "//td[text()='平均評価：']/following-sibling::td/img", "src"); scoreImage != "" {
		if raw := regexp.MustCompile(`\d+`).FindString(lastPathSegment(scoreImage)); raw != "" {
			value, _ := strconv.Atoi(raw)
			score = fmt.Sprintf("%.2f", float64(value)/5)
		}
	}
	uncensored := false
	return &model.MovieInfo{
		CID:         firstText(container, "//td[text()='品番：']/following-sibling::td/text()"),
		URL:         pageURL,
		Plot:        firstText(container, "//div[@class='mg-b20 lh4']/p"),
		Cover:       firstAttr(container, "//img[@name='package-image']", "src"),
		Genre:       genre,
		GenreID:     genreID,
		Score:       score,
		Title:       title,
		Serial:      firstText(container, "//td[text()='シリーズ：']/following-sibling::td/a"),
		Duration:    duration,
		Producer:    firstText(container, "//td[text()='メーカー：']/following-sibling::td/a"),
		Uncensored:  &uncensored,
		PublishDate: strings.ReplaceAll(firstText(container, "//td[text()='発売日：']/following-sibling::td/text()"), "/", "-"),
		PreviewPics: allAttrs(container, "//a[@name='sample-image']/img", "data-lazy"),
	}, nil
}

type fanzaSearchResult struct {
	product  string
	pageType string
	url      string
}

var fanzaProductPriority = map[string]int{
	"digital": 10,
	"mono":    5,
	"monthly": 2,
	"rental":  1,
}

var fanzaTypePriority = map[string]int{
	"videoa":     10,
	"anime":      8,
	"nikkatsu":   6,
	"doujin":     4,
	"dvd":        3,
	"ppr":        2,
	"paradisetv": 1,
}

func (f *Fanza) searchResults(searchURL string, node *html.Node, cid string) []fanzaSearchResult {
	var items []fanzaSearchResult
	for _, rawURL := range allAttrs(node, "//ul[@id='list']/li/div/p/a", "href") {
		item, ok := parseFanzaSearchURL(searchURL, rawURL, cid)
		if ok {
			items = append(items, item)
		}
	}
	return items
}

func parseFanzaSearchURL(searchURL string, rawURL string, cid string) (fanzaSearchResult, bool) {
	parts := strings.Split(rawURL, "/")
	item := fanzaSearchResult{}
	for i, part := range parts {
		if part == "-" && i >= 2 {
			item.product = parts[i-2]
			item.pageType = parts[i-1]
			continue
		}
		if strings.HasPrefix(part, "cid=") {
			foundCID := strings.TrimPrefix(part, "cid=")
			if foundCID != cid {
				return fanzaSearchResult{}, false
			}
			var cleanParts []string
			for _, value := range parts {
				if strings.HasPrefix(value, "?") {
					continue
				}
				cleanParts = append(cleanParts, value)
			}
			item.url = strings.Join(cleanParts, "/")
			if !strings.HasSuffix(item.url, "/") {
				item.url += "/"
			}
			item.url = absURL(searchURL, item.url)
			return item, item.product != "" && item.pageType != ""
		}
	}
	return fanzaSearchResult{}, false
}

func (f *Fanza) parserForType(pageType string) func(string, *html.Node) (*model.MovieInfo, error) {
	switch pageType {
	case "videoa", "ppr", "nikkatsu":
		return f.parseVideoa
	case "anime", "doujin":
		return f.parseAnime
	default:
		return nil
	}
}

func isHTTPStatus(err error, status int) bool {
	return strings.Contains(err.Error(), fmt.Sprintf("http status %d", status))
}

func fanzaGenre(node *html.Node, expr string) ([]string, []string) {
	var genre []string
	var genreID []string
	for _, tag := range htmlquery.Find(node, expr) {
		genre = append(genre, strings.TrimSpace(htmlquery.InnerText(tag)))
		href := htmlquery.SelectAttr(tag, "href")
		id := ""
		if idx := strings.LastIndex(href, "="); idx >= 0 {
			id = strings.Trim(href[idx+1:], "/")
		}
		genreID = append(genreID, id)
	}
	return genre, genreID
}
