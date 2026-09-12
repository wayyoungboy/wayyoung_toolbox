package sites

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"regexp"
	"strings"

	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
)

type AirAV struct {
	client      *httpclient.Client
	baseURL     string
	hardworking bool
}

func NewAirAV(client *httpclient.Client, baseURL string, hardworking bool) *AirAV {
	return &AirAV{client: client, baseURL: strings.TrimRight(baseURL, "/"), hardworking: hardworking}
}

func (a *AirAV) ID() model.CrawlerID {
	return "airav"
}

func (a *AirAV) Parse(ctx context.Context, input crawler.MovieRef) (*model.MovieInfo, error) {
	resp, err := a.getVideo(ctx, input.DVDID)
	if err != nil {
		return nil, err
	}
	data := resp.Result
	info := &model.MovieInfo{
		DVDID:       data.Barcode,
		URL:         a.baseURL + "/video/" + data.Barcode,
		Plot:        cleanAirAVText(data.Description),
		Cover:       data.ImgURL,
		Genre:       names(data.Tags),
		Title:       cleanAirAVText(data.Name),
		Actress:     names(data.Actors),
		PublishDate: data.PublishDate,
		PreviewPics: append([]string(nil), data.Images...),
	}
	if len(data.Factories) > 0 {
		info.Producer = data.Factories[0].Name
	}
	if a.hardworking {
		info.PreviewVideo = a.fetchPreviewVideo(ctx, data.Barcode, data.VID)
	}
	for _, keyword := range []string{"馬賽克破壞版", "馬賽克破解版", "無碼流出版"} {
		if strings.Contains(info.Title, keyword) {
			info.Title = ""
			info.Genre = nil
		}
		if strings.Contains(info.Plot, keyword) {
			info.Plot = ""
			info.Genre = nil
		}
	}
	return info, nil
}

func (a *AirAV) getVideo(ctx context.Context, dvdid string) (*airAVResponse, error) {
	resp, err := a.fetchVideo(ctx, dvdid)
	if err != nil {
		return nil, err
	}
	if resp.Count == 0 && regexp.MustCompile(`\d{6}[-_]\d{2,3}`).MatchString(dvdid) {
		barcode, err := a.search(ctx, dvdid)
		if err != nil {
			return nil, err
		}
		resp, err = a.fetchVideo(ctx, barcode)
		if err != nil {
			return nil, err
		}
	}
	if resp.Count == 0 {
		return nil, crawler.ErrMovieNotFound
	}
	return resp, nil
}

func (a *AirAV) fetchVideo(ctx context.Context, barcode string) (*airAVResponse, error) {
	raw, err := a.client.Get(ctx, a.baseURL+"/api/video/barcode/"+barcode+"?lng=zh-TW")
	if err != nil {
		if errors.Is(err, httpclient.ErrSiteBlocked) {
			return nil, crawler.ErrSiteBlocked
		}
		return nil, err
	}
	defer raw.Body.Close()
	var resp airAVResponse
	if err := json.NewDecoder(raw.Body).Decode(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (a *AirAV) search(ctx context.Context, dvdid string) (string, error) {
	raw, err := a.client.Get(ctx, a.baseURL+"/api/video/list?lang=zh-TW&lng=zh-TW&search="+dvdid+"&page=0")
	if err != nil {
		return "", err
	}
	defer raw.Body.Close()
	var resp struct {
		Result []struct {
			Barcode string `json:"barcode"`
		} `json:"result"`
	}
	if err := json.NewDecoder(raw.Body).Decode(&resp); err != nil {
		return "", err
	}
	target := strings.ReplaceAll(dvdid, "-", "_")
	for _, item := range resp.Result {
		if strings.Contains(strings.ReplaceAll(item.Barcode, "-", "_"), target) {
			return item.Barcode, nil
		}
	}
	return "", crawler.ErrMovieNotFound
}

func (a *AirAV) fetchPreviewVideo(ctx context.Context, barcode string, vid string) string {
	raw, err := a.client.Get(ctx, a.baseURL+"/api/video/getVideoMedia?barcode="+barcode+"&vid="+vid)
	if err != nil {
		return ""
	}
	defer raw.Body.Close()
	var resp struct {
		Data map[string]string `json:"data"`
	}
	if err := json.NewDecoder(raw.Body).Decode(&resp); err != nil {
		return ""
	}
	return resp.Data["url"]
}

type airAVResponse struct {
	Count  int       `json:"count"`
	Result airAVData `json:"result"`
}

type airAVData struct {
	Barcode     string      `json:"barcode"`
	VID         string      `json:"vid"`
	Description string      `json:"description"`
	ImgURL      string      `json:"img_url"`
	Tags        []airAVName `json:"tags"`
	Name        string      `json:"name"`
	Actors      []airAVName `json:"actors"`
	PublishDate string      `json:"publish_date"`
	Images      []string    `json:"images"`
	Factories   []airAVName `json:"factories"`
}

type airAVName struct {
	Name string `json:"name"`
}

func names(values []airAVName) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value.Name != "" {
			out = append(out, value.Name)
		}
	}
	return out
}

func cleanAirAVText(value string) string {
	value = html.UnescapeString(value)
	if value == "" {
		return ""
	}
	return value
}
