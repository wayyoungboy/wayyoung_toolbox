package sites

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/antchfx/htmlquery"
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
	"golang.org/x/net/html"
)

type JavLib struct {
	client  *httpclient.Client
	baseURL string
}

func NewJavLib(client *httpclient.Client, baseURL string) *JavLib {
	return &JavLib{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

func (j *JavLib) ID() model.CrawlerID {
	return "javlib"
}

func (j *JavLib) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	searchURL := j.baseURL + "/cn/vl_searchbyid.php?keyword=" + url.QueryEscape(input.DVDID)
	resp, err := j.client.Get(ctx, searchURL)
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

	detailURL := resp.Request.URL.String()
	if sameHost(searchURL, detailURL) && detailURL != searchURL {
		return j.parseDetail(detailURL, doc)
	}

	selectedURL, err := j.pickSearchResult(searchURL, doc, input.DVDID)
	if err != nil {
		return nil, err
	}
	detailDoc, err := j.client.GetHTML(ctx, selectedURL)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		return nil, err
	}
	return j.parseDetail(selectedURL, detailDoc)
}

func (j *JavLib) pickSearchResult(searchURL string, doc *html.Node, dvdid string) (string, error) {
	var matches []*html.Node
	for _, tag := range htmlquery.Find(doc, "//div[@class='video'][@id]/a") {
		tagDVDID := firstText(tag, "div[@class='id']/text()")
		if strings.EqualFold(tagDVDID, dvdid) {
			matches = append(matches, tag)
		}
	}

	switch len(matches) {
	case 0:
		return "", crawler.ErrMovieNotFound
	case 1:
		return absURL(searchURL, htmlquery.SelectAttr(matches[0], "href")), nil
	case 2:
		var nonBluRay []*html.Node
		for _, tag := range matches {
			if !strings.Contains(htmlquery.SelectAttr(tag, "title"), "ブルーレイディスク") {
				nonBluRay = append(nonBluRay, tag)
			}
		}
		if len(nonBluRay) == 1 {
			return absURL(searchURL, htmlquery.SelectAttr(nonBluRay[0], "href")), nil
		}
	}
	return "", fmt.Errorf("%w: %s has %d matches", crawler.ErrMovieDuplicate, dvdid, len(matches))
}

func (j *JavLib) parseDetail(detailURL string, doc *html.Node) (*model.MovieInfo, error) {
	container := htmlquery.FindOne(doc, "/html/body/div/div[@id='rightcolumn']")
	if container == nil {
		return nil, crawler.ErrMovieNotFound
	}
	infoNode := htmlquery.FindOne(container, "//div[@id='video_info']")
	if infoNode == nil {
		return nil, crawler.ErrMovieNotFound
	}

	dvdid := firstText(infoNode, "div[@id='video_id']//td[@class='text']/text()")
	title := strings.TrimSpace(strings.Replace(firstText(container, "div/h3/a/text()"), dvdid, "", 1))
	cover := firstAttr(container, "//img[@id='video_jacket_img']", "src")
	if strings.HasPrefix(cover, "//") {
		cover = "https:" + cover
	} else {
		cover = absURL(detailURL, cover)
	}
	score := strings.Trim(firstText(infoNode, "//span[@class='score']/text()"), "()")

	return &model.MovieInfo{
		DVDID:       dvdid,
		URL:         detailURL,
		Cover:       cover,
		Genre:       allTexts(infoNode, "//span[@class='genre']/a"),
		Score:       score,
		Title:       title,
		Actress:     allTexts(infoNode, "//span[@class='star']/a"),
		Director:    firstText(infoNode, "//span[@class='director']/a"),
		Duration:    firstText(infoNode, "div[@id='video_length']//span[@class='text']/text()"),
		Producer:    firstText(infoNode, "//span[@class='maker']/a"),
		Publisher:   firstText(infoNode, "//span[@class='label']/a"),
		PublishDate: firstText(infoNode, "div[@id='video_date']//td[@class='text']/text()"),
	}, nil
}

func sameHost(left string, right string) bool {
	leftURL, leftErr := url.Parse(left)
	rightURL, rightErr := url.Parse(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return leftURL.Host == rightURL.Host
}
