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
	"golang.org/x/net/html"
)

type MGStage struct {
	client      *httpclient.Client
	baseURL     string
	hardworking bool
}

func NewMGStage(client *httpclient.Client, baseURL string, hardworking bool) *MGStage {
	return &MGStage{client: client, baseURL: strings.TrimRight(baseURL, "/"), hardworking: hardworking}
}

func (m *MGStage) ID() model.CrawlerID {
	return "mgstage"
}

func (m *MGStage) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	pageURL := m.baseURL + "/product/product_detail/" + input.DVDID + "/"
	doc, err := m.client.GetHTML(ctx, pageURL)
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		return nil, err
	}
	title := firstText(doc, "//div[@class='common_detail_cover']/h1")
	container := htmlquery.FindOne(doc, "//div[@class='detail_left']")
	if title == "" || container == nil {
		return nil, crawler.ErrMovieNotFound
	}

	actress := append(allTexts(container, "//th[text()='出演：']/following-sibling::td/text()"), allTexts(container, "//th[text()='出演：']/following-sibling::td/a")...)
	actress = cleanStrings(actress)
	duration := regexp.MustCompile(`\d+`).FindString(firstText(container, "//th[text()='収録時間：']/following-sibling::td/text()"))
	publishDate := strings.ReplaceAll(firstText(container, "//th[text()='配信開始日：']/following-sibling::td/text()"), "/", "-")
	score := ""
	if scoreText := firstText(container, "//td[@class='review']/span/following-sibling::text()"); scoreText != "" {
		if raw := regexp.MustCompile(`^[\.\d]+`).FindString(scoreText); raw != "" {
			value, _ := strconv.ParseFloat(raw, 64)
			score = fmt.Sprintf("%.2f", value*2)
		}
	}

	previewPics := make([]string, 0)
	for _, href := range allAttrs(container, "//a[@class='sample_image']", "href") {
		previewPics = append(previewPics, absURL(pageURL, href))
	}
	previewVideo := ""
	if m.hardworking {
		previewVideo = m.fetchPreviewVideo(ctx, pageURL, container)
	}
	uncensored := false
	return &model.MovieInfo{
		DVDID:        firstText(container, "//th[text()='品番：']/following-sibling::td/text()"),
		URL:          pageURL,
		Plot:         firstText(container, "//dl[@id='introduction']/dd/p[not(@class='more')]"),
		Cover:        absURL(pageURL, firstAttr(container, "//a[@id='EnlargeImage']", "href")),
		Genre:        allTexts(container, "//th[text()='ジャンル：']/following-sibling::td/a"),
		Score:        score,
		Title:        title,
		Serial:       firstText(container, "//th[text()='シリーズ：']/following-sibling::td/a"),
		Actress:      actress,
		Duration:     duration,
		Producer:     firstText(container, "//th[text()='メーカー：']/following-sibling::td/a"),
		Uncensored:   &uncensored,
		PublishDate:  publishDate,
		PreviewPics:  previewPics,
		PreviewVideo: previewVideo,
	}, nil
}

func (m *MGStage) fetchPreviewVideo(ctx context.Context, pageURL string, container *html.Node) string {
	buttonURL := firstAttr(container, "//a[@class='button_sample']", "href")
	if buttonURL == "" {
		return ""
	}
	pid := lastPathSegment(buttonURL)
	resp, err := m.client.Get(ctx, m.baseURL+"/sampleplayer/sampleRespons.php?pid="+pid)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var payload struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil || payload.URL == "" {
		return ""
	}
	if strings.Contains(payload.URL, ".ism/") {
		return strings.Split(payload.URL, ".ism/")[0] + ".mp4"
	}
	return absURL(pageURL, payload.URL)
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
