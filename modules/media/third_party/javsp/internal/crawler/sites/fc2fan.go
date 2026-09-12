package sites

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/antchfx/htmlquery"
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
	"golang.org/x/net/html"
)

type FC2Fan struct {
	client    *httpclient.Client
	baseURL   string
	localPath string
}

func NewFC2Fan(client *httpclient.Client, baseURL string, localPath string) *FC2Fan {
	return &FC2Fan{client: client, baseURL: strings.TrimRight(baseURL, "/"), localPath: localPath}
}

func (f *FC2Fan) ID() model.CrawlerID {
	return "fc2fan"
}

func (f *FC2Fan) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	doc, local, err := f.load(ctx, input.DVDID)
	if err != nil {
		return nil, err
	}
	container := htmlquery.FindOne(doc, "//div[@class='col-sm-8']")
	if container == nil {
		return nil, crawler.ErrWebsite
	}
	score := ""
	if scoreText := tailText(htmlquery.FindOne(container, "h5/strong[text()='影片评分']")); scoreText != "" {
		if raw := regexp.MustCompile(`\d+`).FindString(scoreText); raw != "" {
			value, _ := strconv.Atoi(raw)
			score = fmt.Sprintf("%.1f", float64(value)/10)
		}
	}
	var uncensored *bool
	resourceInfo := tailText(htmlquery.FindOne(container, "h5/strong[text()='资源参数']"))
	if strings.Contains(resourceInfo, "无码") {
		value := true
		uncensored = &value
	} else if strings.Contains(resourceInfo, "有码") {
		value := false
		uncensored = &value
	}
	previewPics := make([]string, 0)
	for _, src := range allAttrs(container, "//ul[@class='slides']/li/img", "src") {
		if local {
			previewPics = append(previewPics, filepath.Clean(filepath.Join(f.localPath, src)))
		} else {
			previewPics = append(previewPics, absURL(f.baseURL, src))
		}
	}
	cover := ""
	if len(previewPics) > 0 {
		cover = previewPics[0]
	}
	return &model.MovieInfo{
		DVDID:       input.DVDID,
		Cover:       cover,
		Genre:       allTexts(container, "h5/strong[text()='影片标签']/../a"),
		Score:       score,
		Title:       firstText(container, "h3"),
		Actress:     allTexts(container, "h5/strong[text()='女优名字']/../a"),
		Producer:    firstText(container, "h5/strong[text()='卖家信息']/following-sibling::*[1]"),
		Uncensored:  uncensored,
		PreviewPics: previewPics,
	}, nil
}

func (f *FC2Fan) load(ctx context.Context, dvdid string) (*html.Node, bool, error) {
	if f.localPath != "" {
		htmlFile := filepath.Join(f.localPath, dvdid+".html")
		file, err := os.Open(htmlFile)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, true, crawler.ErrMovieNotFound
			}
			return nil, true, err
		}
		defer file.Close()
		doc, err := htmlquery.Parse(file)
		return doc, true, err
	}
	doc, err := f.client.GetHTML(ctx, f.baseURL+"/html/"+dvdid+".html")
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, false, crawler.ErrSiteBlocked
		}
		return nil, false, err
	}
	return doc, false, nil
}
