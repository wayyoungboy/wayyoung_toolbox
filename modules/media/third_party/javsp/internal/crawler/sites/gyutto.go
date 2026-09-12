package sites

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/antchfx/htmlquery"
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
)

type Gyutto struct {
	client  *httpclient.Client
	baseURL string
}

func NewGyutto(client *httpclient.Client, baseURL string) *Gyutto {
	return &Gyutto{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

func (g *Gyutto) ID() model.CrawlerID {
	return "gyutto"
}

func (g *Gyutto) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	id := strings.ToUpper(input.DVDID)
	if !strings.HasPrefix(id, "GYUTTO-") {
		return nil, crawler.ErrMovieNotFound
	}
	gyuttoID := strings.TrimPrefix(id, "GYUTTO-")
	pageURL := g.baseURL + "/i/item" + gyuttoID + "?select_uaflag=1"
	doc, err := g.client.GetHTML(ctx, pageURL)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		return nil, err
	}

	images := make([]string, 0)
	for _, src := range allAttrs(doc, "//a[@class='highslide']/img", "src") {
		images = append(images, absURL(pageURL, src))
	}
	var cover string
	if len(images) > 0 {
		cover = images[0]
	}
	var producer string
	var genre []string
	var publishDate string
	for _, row := range htmlquery.Find(doc, "//dl[@class='BasicInfo clearfix']") {
		key := firstText(row, ".//dt")
		switch key {
		case "サークル":
			producer = strings.Join(allTexts(row, ".//dd/a"), "")
		case "ジャンル":
			genre = allTexts(row, ".//dd/a")
		case "配信開始日":
			raw := firstText(row, ".//dd")
			if parsed, err := time.Parse("2006年01月02日", raw); err == nil {
				publishDate = parsed.Format("2006-01-02")
			}
		}
	}
	return &model.MovieInfo{
		DVDID:       id,
		URL:         pageURL,
		Plot:        firstText(doc, "//div[@class='unit_DetailLead']/p"),
		Cover:       cover,
		Genre:       genre,
		Title:       firstText(doc, "//h1"),
		Producer:    producer,
		PublishDate: publishDate,
		PreviewPics: images,
	}, nil
}
