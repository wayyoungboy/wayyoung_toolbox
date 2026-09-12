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
	"golang.org/x/net/html"
)

type Arzon struct {
	client  *httpclient.Client
	baseURL string
	iv      bool
}

func NewArzon(client *httpclient.Client, baseURL string, iv bool) *Arzon {
	return &Arzon{client: client, baseURL: strings.TrimRight(baseURL, "/"), iv: iv}
}

func NewArzonIV(client *httpclient.Client, baseURL string) *Arzon {
	return NewArzon(client, baseURL, true)
}

func (a *Arzon) ID() model.CrawlerID {
	if a.iv {
		return "arzon_iv"
	}
	return "arzon"
}

func (a *Arzon) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	listPath := "/itemlist.html?t=&m=all&s=&q="
	if a.iv {
		listPath = "/imagelist.html?q="
	}
	listURL := a.baseURL + listPath + input.DVDID
	listDoc, err := a.client.GetHTML(ctx, listURL)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		return nil, err
	}
	itemHref := firstAttr(listDoc, "//h2/a", "href")
	if itemHref == "" {
		return nil, crawler.ErrMovieNotFound
	}
	itemURL := absURL(a.baseURL, itemHref)
	itemDoc, err := a.client.GetHTML(ctx, itemURL)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		return nil, err
	}
	titleClass := "detail_title_new2"
	if a.iv {
		titleClass = "detail_title_new"
	}
	info := &model.MovieInfo{
		DVDID: input.DVDID,
		URL:   itemURL,
		Title: firstText(itemDoc, "//div[@class='"+titleClass+"']//h1"),
		Plot:  firstText(itemDoc, "//div[@class='item_text']/text()"),
		Cover: protocolHTTPS(firstAttr(itemDoc, "//td[@align='center']//a/img", "src")),
	}
	if info.Title == "" {
		return nil, crawler.ErrMovieNotFound
	}
	if !a.iv {
		for _, src := range allAttrs(itemDoc, "//div[@class='detail_img']//img", "src") {
			info.PreviewPics = append(info.PreviewPics, strings.Replace(protocolHTTPS(src), "m_", "", 1))
		}
	}
	registerRows := htmlquery.Find(itemDoc, "//div[@class='item_register']/table//tr")
	values := arzonRows(registerRows)
	if a.iv {
		info.Actress = values["タレント："]
		info.Producer = firstString(values["イメージメーカー："])
		info.Genre = appendNonEmpty([]string{}, firstString(values["イメージレーベル："]), firstString(values["タグ："]))
	} else {
		info.Actress = values["AV女優："]
		info.Producer = firstString(values["AVメーカー："])
		info.Serial = firstString(values["シリーズ："])
		info.Genre = appendNonEmpty([]string{}, firstString(values["AVレーベル："]), firstString(values["タグ："]))
	}
	info.Director = firstString(values["監督："])
	if date := regexp.MustCompile(`\d{4}/\d{2}/\d{2}`).FindString(firstString(values["発売日："])); date != "" {
		info.PublishDate = strings.ReplaceAll(date, "/", "-")
	}
	if duration := regexp.MustCompile(`([\d.]+)分`).FindStringSubmatch(firstString(values["収録時間："])); len(duration) > 1 {
		info.Duration = duration[1]
	}
	return info, nil
}

func arzonRows(rows []*html.Node) map[string][]string {
	out := make(map[string][]string)
	for _, row := range rows {
		key := firstText(row, "./td[1]/text()")
		if key == "" {
			continue
		}
		out[key] = allTexts(row, "./td[2]//text()")
	}
	return out
}

func protocolHTTPS(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if strings.HasPrefix(rawURL, "//") {
		return "https:" + rawURL
	}
	return rawURL
}

func appendNonEmpty(values []string, items ...string) []string {
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			values = append(values, item)
		}
	}
	return values
}
