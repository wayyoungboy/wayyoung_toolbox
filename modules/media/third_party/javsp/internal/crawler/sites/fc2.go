package sites

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/antchfx/htmlquery"
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
	"github.com/yuukiy/javsp-go/internal/util"
	"golang.org/x/net/html"
)

type FC2 struct {
	client      *httpclient.Client
	baseURL     string
	hardworking bool
}

func NewFC2(client *httpclient.Client, baseURL string, hardworking bool) *FC2 {
	return &FC2{client: client, baseURL: strings.TrimRight(baseURL, "/"), hardworking: hardworking}
}

func (f *FC2) ID() model.CrawlerID {
	return "fc2"
}

func (f *FC2) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	id := strings.ToUpper(input.DVDID)
	if !strings.HasPrefix(id, "FC2-") {
		return nil, crawler.ErrMovieNotFound
	}
	fc2ID := strings.TrimPrefix(id, "FC2-")
	pageURL := f.baseURL + "/article/" + fc2ID + "/"
	doc, err := f.client.GetHTML(ctx, pageURL)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		return nil, err
	}
	container := htmlquery.FindOne(doc, "//div[@class='items_article_left']")
	if container == nil {
		return nil, crawler.ErrMovieNotFound
	}
	var titleParts []string
	for _, tag := range htmlquery.Find(container, "//div[@class='items_article_headerInfo']/h3") {
		titleParts = append(titleParts, htmlquery.InnerText(tag))
	}
	title := strings.Join(titleParts, "")
	thumbPic := absURL(pageURL, firstAttr(container, "//div[@class='items_article_MainitemThumb']/span/img", "src"))
	duration, _ := util.StrftimeToMinutes(firstText(container, "//div[@class='items_article_MainitemThumb']/span/p[@class='items_article_info']"))
	producer := firstText(container, "//li[text()='by ']/following-sibling::li[1]/a")
	genre := allTexts(container, "//a[@class='tag tagTag']")
	publishDate := ""
	if dateText := firstText(container, "//div[@class='items_article_Releasedate']/p"); len(dateText) >= 10 {
		publishDate = strings.ReplaceAll(dateText[len(dateText)-10:], "/", "-")
	}
	var previewPics []string
	for _, href := range allAttrs(container, "//ul[@data-feed='sample-images']/li/a", "href") {
		previewPics = append(previewPics, absURL(pageURL, href))
	}
	score := ""
	previewVideo := ""
	if f.hardworking {
		previewVideo = f.fetchSampleVideo(ctx, fc2ID, container)
	} else if class := firstAttr(container, "//a[@class='items_article_Stars']/p/span", "class"); class != "" {
		digit := regexp.MustCompile(`\d$`).FindString(class)
		if digit != "" {
			value, _ := strconv.Atoi(digit)
			score = fmt.Sprintf("%.2f", float64(value*2))
		}
	}
	cover := thumbPic
	if len(previewPics) > 0 {
		cover = previewPics[0]
	}
	return &model.MovieInfo{
		DVDID:        id,
		URL:          pageURL,
		Cover:        cover,
		Genre:        genre,
		Score:        score,
		Title:        title,
		Duration:     strconv.Itoa(duration),
		Producer:     producer,
		PublishDate:  publishDate,
		PreviewPics:  previewPics,
		PreviewVideo: previewVideo,
	}, nil
}

func (f *FC2) fetchSampleVideo(ctx context.Context, fc2ID string, container *html.Node) string {
	frameURL := firstAttr(container, "//section[@class='items_article_Contents']/iframe", "src")
	if frameURL == "" {
		return ""
	}
	key := frameURL
	if idx := strings.LastIndex(frameURL, "="); idx >= 0 {
		key = frameURL[idx+1:]
	}
	resp, err := f.client.Get(ctx, f.baseURL+"/api/v2/videos/"+fc2ID+"/sample?key="+key)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ""
	}
	return payload.Path
}
