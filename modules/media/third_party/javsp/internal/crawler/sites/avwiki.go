package sites

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/antchfx/htmlquery"
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
	"golang.org/x/net/html"
)

type AVWiki struct {
	client  *httpclient.Client
	baseURL string
}

func NewAVWiki(client *httpclient.Client, baseURL string) *AVWiki {
	return &AVWiki{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

func (a *AVWiki) ID() model.CrawlerID {
	return "avwiki"
}

func (a *AVWiki) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	pageURL := a.baseURL + "/" + input.DVDID
	doc, err := a.client.GetHTML(ctx, pageURL)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		return nil, err
	}
	body := htmlquery.FindOne(doc, "//section[@class='article-body']")
	if body == nil {
		return nil, crawler.ErrMovieNotFound
	}
	title := strings.ReplaceAll(firstText(body, "div/p/text()"), "【"+input.DVDID+"】", "")
	infoNode := htmlquery.FindOne(body, "dl[@class='dltable']")
	if infoNode == nil {
		return nil, crawler.ErrMovieNotFound
	}
	data := dlData(infoNode)
	uncensored := false
	return &model.MovieInfo{
		DVDID:       firstString(data["メーカー品番"]),
		URL:         pageURL,
		Cover:       bestSrcsetImage(pageURL, htmlquery.FindOne(doc, "//header/div/a[@class='image-link-border']/img")),
		Title:       strings.TrimSpace(title),
		Serial:      firstString(data["シリーズ"]),
		Actress:     data["AV女優名"],
		Producer:    firstString(data["メーカー"]),
		Uncensored:  &uncensored,
		PublishDate: firstString(data["配信開始日"]),
	}, nil
}

func dlData(node *html.Node) map[string][]string {
	data := make(map[string][]string)
	dts := htmlquery.Find(node, "dt")
	dds := htmlquery.Find(node, "dd")
	for i, dt := range dts {
		if i >= len(dds) {
			break
		}
		key := strings.TrimSpace(htmlquery.InnerText(dt))
		values := allTexts(dds[i], "a")
		if len(values) == 0 {
			values = []string{strings.TrimSpace(htmlquery.InnerText(dds[i]))}
		}
		data[key] = trimSlice(values)
	}
	return data
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func bestSrcsetImage(baseURL string, node *html.Node) string {
	if node == nil {
		return ""
	}
	srcset := htmlquery.SelectAttr(node, "srcset")
	if srcset == "" {
		return absURL(baseURL, htmlquery.SelectAttr(node, "src"))
	}
	type candidate struct {
		url   string
		width int
	}
	var candidates []candidate
	for _, item := range strings.Split(srcset, ",") {
		parts := strings.Fields(strings.TrimSpace(item))
		if len(parts) != 2 {
			continue
		}
		width, err := strconv.Atoi(strings.TrimSuffix(parts[1], "w"))
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{url: parts[0], width: width})
	}
	if len(candidates) == 0 {
		return absURL(baseURL, htmlquery.SelectAttr(node, "src"))
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].width > candidates[j].width })
	return absURL(baseURL, candidates[0].url)
}
