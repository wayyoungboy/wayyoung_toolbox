package sites

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/antchfx/htmlquery"
	"github.com/yuukiy/javsp-go/internal/avid"
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
	"golang.org/x/net/html"
)

type JavDB struct {
	client         *httpclient.Client
	baseURL        string
	cookieProvider JavDBCookieProvider
}

type JavDBCookieProvider interface {
	NextJavDBCookies(context.Context) ([]*http.Cookie, bool)
}

func NewJavDB(client *httpclient.Client, baseURL string) *JavDB {
	return NewJavDBWithCookieProvider(client, baseURL, nil)
}

func NewJavDBWithCookieProvider(client *httpclient.Client, baseURL string, cookieProvider JavDBCookieProvider) *JavDB {
	return &JavDB{client: client, baseURL: strings.TrimRight(baseURL, "/"), cookieProvider: cookieProvider}
}

func (j *JavDB) ID() model.CrawlerID {
	return "javdb"
}

func (j *JavDB) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	return j.parse(ctx, input, true)
}

// ParseResources requires the detail page, not the metadata-only search-card
// fallback. A login/paywall response is not proof that a work has no resources.
func (j *JavDB) ParseResources(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	return j.parse(ctx, input, false)
}

func (j *JavDB) parse(ctx context.Context, input crawler.MovieRef, allowFallback bool) (*model.MovieInfo, error) {
	searchURL := j.baseURL + "/search?q=" + url.QueryEscape(input.DVDID)
	searchDoc, _, err := j.getHTML(ctx, searchURL)
	if err != nil {
		return nil, err
	}

	box, detailURL, err := j.pickSearchResult(searchURL, searchDoc, input.DVDID)
	if err != nil {
		return nil, err
	}
	detailDoc, _, err := j.getHTML(ctx, detailURL)
	if err != nil {
		if allowFallback && (errors.Is(err, crawler.ErrCredential) || errors.Is(err, crawler.ErrSitePermission)) {
			return j.parseSearchCard(detailURL, box), nil
		}
		return nil, err
	}
	if !allowFallback {
		container := htmlquery.FindOne(detailDoc, "/html/body/section/div/div[@class='video-detail']")
		if container == nil {
			return nil, crawler.ErrWebsite
		}
		info := htmlquery.FindOne(container, "//nav[@class='panel movie-panel-info']")
		if info == nil {
			return nil, crawler.ErrWebsite
		}
		if !strings.EqualFold(javdbLabelValue(info, "番號:"), input.DVDID) {
			return nil, crawler.ErrMovieDuplicate
		}
	}
	return j.parseDetail(detailURL, detailDoc, input.DVDID)
}

func (j *JavDB) getHTML(ctx context.Context, rawURL string) (*html.Node, string, error) {
	for {
		doc, finalURL, err := j.getHTMLOnce(ctx, rawURL)
		if !errors.Is(err, crawler.ErrCredential) || j.cookieProvider == nil {
			return doc, finalURL, err
		}
		cookies, ok := j.cookieProvider.NextJavDBCookies(ctx)
		if !ok || len(cookies) == 0 {
			return doc, finalURL, err
		}
		j.client = j.client.WithCookies(cookies)
	}
}

func (j *JavDB) getHTMLOnce(ctx context.Context, rawURL string) (*html.Node, string, error) {
	resp, err := j.client.Get(ctx, rawURL)
	if err != nil {
		return nil, "", javdbHTTPError(err)
	}
	defer resp.Body.Close()

	finalURL := resp.Request.URL.String()
	if finalURL != rawURL {
		finalParsed, err := url.Parse(finalURL)
		if err == nil {
			if strings.Contains(finalParsed.Path, "/login") {
				return nil, finalURL, crawler.ErrCredential
			}
			if strings.Contains(lastPathSegment(finalURL), "pay") {
				return nil, finalURL, crawler.ErrSitePermission
			}
		}
	}

	doc, err := htmlquery.Parse(resp.Body)
	if err != nil {
		return nil, finalURL, err
	}
	return doc, finalURL, nil
}

func javdbHTTPError(err error) error {
	if errors.Is(err, httpclient.ErrSiteBlocked) {
		return crawler.ErrSiteBlocked
	}
	msg := err.Error()
	if strings.Contains(msg, "http status 403") || strings.Contains(msg, "http status 503") {
		return fmt.Errorf("%w: %v", crawler.ErrSiteBlocked, err)
	}
	return err
}

func (j *JavDB) pickSearchResult(searchURL string, doc *html.Node, dvdid string) (*html.Node, string, error) {
	var matches []*html.Node
	for _, box := range htmlquery.Find(doc, "//a[@class='box']") {
		foundID := firstText(box, "div[@class='video-title']/strong/text()")
		if strings.EqualFold(foundID, dvdid) {
			matches = append(matches, box)
		}
	}

	switch len(matches) {
	case 0:
		return nil, "", crawler.ErrMovieNotFound
	case 1:
		return matches[0], absURL(searchURL, htmlquery.SelectAttr(matches[0], "href")), nil
	default:
		return nil, "", fmt.Errorf("%w: %s has %d matches", crawler.ErrMovieDuplicate, dvdid, len(matches))
	}
}

func (j *JavDB) parseSearchCard(detailURL string, box *html.Node) *model.MovieInfo {
	return &model.MovieInfo{
		DVDID:       firstText(box, "div[@class='video-title']/strong/text()"),
		URL:         detailURL,
		Cover:       javdbAssetURL(detailURL, firstAttr(box, "div/img", "src")),
		Score:       javdbScore(firstText(box, "div[@class='score']")),
		Title:       strings.TrimSpace(htmlquery.SelectAttr(box, "title")),
		PublishDate: firstText(box, "div[@class='meta']/text()"),
	}
}

func (j *JavDB) parseDetail(detailURL string, doc *html.Node, requestedDVDID string) (*model.MovieInfo, error) {
	container := htmlquery.FindOne(doc, "/html/body/section/div/div[@class='video-detail']")
	if container == nil {
		return nil, crawler.ErrMovieNotFound
	}
	infoNode := htmlquery.FindOne(container, "//nav[@class='panel movie-panel-info']")
	if infoNode == nil {
		return nil, crawler.ErrMovieNotFound
	}

	dvdid := javdbLabelValue(infoNode, "番號:")
	if dvdid == "" {
		dvdid = requestedDVDID
	}
	title := strings.TrimSpace(strings.Replace(firstText(container, "h2/strong[@class='current-title']/text()"), dvdid, "", 1))
	originalTitle := ""
	if htmlquery.FindOne(container, "//a[contains(@class, 'meta-link') and not(contains(@style, 'display: none'))]") != nil {
		originalTitle = firstText(container, "h2/span[@class='origin-title']/text()")
	}

	previewPics := allAttrs(container, "//a[@class='tile-item'][@data-fancybox='gallery']", "href")
	for i, pic := range previewPics {
		previewPics[i] = javdbAssetURL(detailURL, pic)
	}
	previewVideo := javdbAssetURL(detailURL, firstAttr(container, "//video[@id='preview-video']/source", "src"))
	genre, genreID, uncensored := javdbGenres(infoNode)
	actress := javdbActress(infoNode)
	magnet := allAttrs(container, "//div[@class='magnet-name column is-four-fifths']/a", "href")
	for i, item := range magnet {
		magnet[i] = strings.ReplaceAll(item, "[javdb.com]", "")
	}

	producerLabel := "片商:"
	if avid.GuessAVType(requestedDVDID) == model.DataSourceFC2 {
		producerLabel = "賣家:"
	}

	return &model.MovieInfo{
		DVDID:         dvdid,
		URL:           detailURL,
		Cover:         javdbAssetURL(detailURL, firstAttr(container, "//img[@class='video-cover']", "src")),
		Genre:         genre,
		GenreID:       genreID,
		Score:         javdbScore(javdbScoreText(infoNode)),
		Title:         title,
		OriginalTitle: originalTitle,
		Magnet:        magnet,
		Serial:        javdbLabelValue(infoNode, "系列:"),
		Actress:       actress,
		Director:      javdbLabelValue(infoNode, "導演:"),
		Duration:      strings.TrimSpace(strings.ReplaceAll(javdbLabelValue(infoNode, "時長:"), "分鍾", "")),
		Producer:      javdbLabelValue(infoNode, producerLabel),
		Publisher:     javdbLabelValue(infoNode, "發行:"),
		Uncensored:    uncensored,
		PublishDate:   javdbLabelValue(infoNode, "日期:"),
		PreviewPics:   previewPics,
		PreviewVideo:  previewVideo,
	}, nil
}

func javdbLabelValue(node *html.Node, label string) string {
	expr := fmt.Sprintf("div/strong[text()='%s']/following-sibling::*[1]", label)
	return firstText(node, expr)
}

func javdbScoreText(node *html.Node) string {
	scoreNode := htmlquery.FindOne(node, "//span[@class='score-stars']")
	if scoreNode == nil {
		return ""
	}
	return tailText(scoreNode)
}

func javdbScore(raw string) string {
	match := regexp.MustCompile(`([\d.]+)分`).FindStringSubmatch(raw)
	if len(match) < 2 {
		return ""
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%.2f", value*2)
}

func javdbGenres(node *html.Node) ([]string, []string, *bool) {
	var genre []string
	var genreID []string
	var uncensored *bool
	for _, tag := range htmlquery.Find(node, "//strong[text()='類別:']/../span/a") {
		text := strings.TrimSpace(htmlquery.InnerText(tag))
		if text != "" {
			genre = append(genre, text)
		}
		id := javdbHrefLastSegment(htmlquery.SelectAttr(tag, "href"))
		if id != "" {
			genreID = append(genreID, id)
		}
		switch strings.SplitN(id, "?", 2)[0] {
		case "uncensored":
			value := true
			uncensored = &value
		case "tags":
			value := false
			uncensored = &value
		}
	}
	return genre, genreID, uncensored
}

func javdbHrefLastSegment(href string) string {
	parts := strings.Split(strings.TrimRight(href, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func javdbActress(node *html.Node) []string {
	actorsTag := htmlquery.FindOne(node, "//strong[text()='演員:']/../span")
	if actorsTag == nil {
		return nil
	}
	names := allTexts(actorsTag, "a")
	genders := allTexts(actorsTag, "strong")
	var actress []string
	for i, name := range names {
		if i < len(genders) && genders[i] == "♀" {
			actress = append(actress, name)
		}
	}
	return actress
}

func javdbAssetURL(baseURL string, rawURL string) string {
	if rawURL == "" {
		return ""
	}
	if strings.HasPrefix(rawURL, "//") {
		return "https:" + rawURL
	}
	return absURL(baseURL, rawURL)
}
