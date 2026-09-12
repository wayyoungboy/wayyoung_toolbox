package translate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuukiy/javsp-go/internal/model"
)

func TestApplyTranslatesTitleAndPlot(t *testing.T) {
	engine := &fakeEngine{
		results: map[string]Result{
			"Raw Title": {
				Text:       "中文标题",
				OrigBreak:  []string{"Raw", " Title"},
				TransBreak: []string{"中文", "标题"},
			},
			"Raw Plot": {Text: "中文剧情"},
		},
	}
	info := &model.MovieInfo{
		Title:   "Raw Title",
		Plot:    "Raw Plot",
		Actress: []string{"Actor A"},
	}

	ok, err := Apply(context.Background(), info, engine, Fields{Title: true, Plot: true})
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if !ok {
		t.Fatalf("Apply returned ok=false")
	}
	if info.Title != "中文标题" || info.OriginalTitle != "Raw Title" {
		t.Fatalf("title/original = %q / %q", info.Title, info.OriginalTitle)
	}
	if info.Plot != "中文剧情" || info.OriginalPlot != "Raw Plot" {
		t.Fatalf("plot/original = %q / %q", info.Plot, info.OriginalPlot)
	}
	if len(info.TitleBreak) != 2 || len(info.OriginalTitleBreak) != 2 {
		t.Fatalf("breaks = %#v / %#v", info.TitleBreak, info.OriginalTitleBreak)
	}
	if len(engine.calls) != 2 || engine.calls[0].text != "Raw Title" || engine.calls[0].actress[0] != "Actor A" {
		t.Fatalf("calls = %#v", engine.calls)
	}
}

func TestOpenAITranslate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer key" {
			t.Fatalf("Authorization = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "model-a" {
			t.Fatalf("model = %#v", body["model"])
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":" translated text "}}]}`))
	}))
	defer server.Close()

	engine := OpenAIEngine{
		URL:    server.URL,
		APIKey: "key",
		Model:  "model-a",
		Client: server.Client(),
	}
	result, err := engine.Translate(context.Background(), "raw text", nil)
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if result.Text != "translated text" {
		t.Fatalf("Text = %q", result.Text)
	}
}

func TestNewEngineFromConfig(t *testing.T) {
	engine, err := NewEngineFromConfig(map[string]any{
		"name":    "openai",
		"url":     "https://api.example/chat",
		"api_key": "key",
		"model":   "model-a",
	}, nil)
	if err != nil {
		t.Fatalf("NewEngineFromConfig returned error: %v", err)
	}
	openai, ok := engine.(OpenAIEngine)
	if !ok {
		t.Fatalf("engine type = %T, want OpenAIEngine", engine)
	}
	if openai.URL != "https://api.example/chat" || openai.APIKey != "key" || openai.Model != "model-a" {
		t.Fatalf("openai config = %#v", openai)
	}

	engine, err = NewEngineFromConfig(nil, nil)
	if err != nil {
		t.Fatalf("nil NewEngineFromConfig returned error: %v", err)
	}
	if engine != nil {
		t.Fatalf("nil engine = %#v, want nil", engine)
	}

}

func TestGoogleTranslate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "raw text" {
			t.Fatalf("q = %q", got)
		}
		_, _ = w.Write([]byte(`{"sentences":[{"orig":"raw ","trans":"中文"},{"orig":"text","trans":"文本"}]}`))
	}))
	defer server.Close()

	engine := GoogleEngine{BaseURL: server.URL, Client: server.Client(), PostRequestDelay: -1}
	result, err := engine.Translate(context.Background(), "raw text", nil)
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if result.Text != "中文文本" || len(result.OrigBreak) != 2 || len(result.TransBreak) != 2 {
		t.Fatalf("result = %#v", result)
	}
}

func TestGoogleTranslateRetriesRateLimitLikeOriginal(t *testing.T) {
	var attempts atomic.Int32
	client := &http.Client{Transport: translateRoundTripFunc(func(*http.Request) (*http.Response, error) {
		if attempts.Add(1) < 3 {
			return translateResponse(http.StatusTooManyRequests, "rate limited"), nil
		}
		return translateResponse(http.StatusOK, `{"sentences":[{"orig":"raw","trans":"译文"}]}`), nil
	})}

	engine := GoogleEngine{
		BaseURL:          "https://translate.example/single",
		Client:           client,
		RateLimitWait:    time.Millisecond,
		PostRequestDelay: -1,
	}
	result, err := engine.Translate(context.Background(), "raw", nil)
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if result.Text != "译文" || attempts.Load() != 3 {
		t.Fatalf("result=%#v attempts=%d, want translated result after 3 attempts", result, attempts.Load())
	}
}

func TestGoogleTranslateRateLimitWaitHonorsContext(t *testing.T) {
	client := &http.Client{Transport: translateRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return translateResponse(http.StatusTooManyRequests, "rate limited"), nil
	})}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	engine := GoogleEngine{BaseURL: "https://translate.example/single", Client: client, RateLimitWait: time.Hour, PostRequestDelay: -1}
	_, err := engine.Translate(ctx, "raw", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestBingTranslateProtectsActressNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), `<mstrans:dictionary translation=\"Actor A\">Actor A</mstrans:dictionary>`) {
			t.Fatalf("body = %s", string(body))
		}
		_, _ = w.Write([]byte(`[{"translations":[{"text":"done ","sentLen":{"srcSentLen":[63],"transSentLen":[5]}}]}]`))
	}))
	defer server.Close()

	engine := BingEngine{URL: server.URL, APIKey: "key", Client: server.Client()}
	result, err := engine.Translate(context.Background(), "Actor A", []string{"Actor A"})
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if result.Text != "done" || len(result.OrigBreak) != 1 || len(result.TransBreak) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestBaiduTranslate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "raw text" {
			t.Fatalf("q = %q", got)
		}
		_, _ = w.Write([]byte(`{"trans_result":[{"dst":"段落一"},{"dst":"段落二"}]}`))
	}))
	defer server.Close()

	engine := BaiduEngine{URL: server.URL, AppID: "appid", APIKey: "secret", Client: server.Client(), MinInterval: -1}
	result, err := engine.Translate(context.Background(), "raw text", nil)
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if result.Text != "段落一\n段落二" {
		t.Fatalf("Text = %q", result.Text)
	}
}

func TestBaiduTranslateRateLimitWaitHonorsContext(t *testing.T) {
	baiduRateMu.Lock()
	baiduLastAccess = time.Now()
	baiduRateMu.Unlock()
	t.Cleanup(func() {
		baiduRateMu.Lock()
		baiduLastAccess = time.Time{}
		baiduRateMu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	engine := BaiduEngine{URL: "https://example.invalid", AppID: "appid", APIKey: "secret", MinInterval: time.Hour}
	_, err := engine.Translate(ctx, "raw", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestClaudeTranslate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "key" {
			t.Fatalf("x-api-key = %q", got)
		}
		_, _ = w.Write([]byte(`{"content":[{"text":" translated by claude "}]}`))
	}))
	defer server.Close()

	engine := ClaudeEngine{URL: server.URL, APIKey: "key", Client: server.Client()}
	result, err := engine.Translate(context.Background(), "raw text", nil)
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if result.Text != "translated by claude" {
		t.Fatalf("Text = %q", result.Text)
	}
}

type fakeEngine struct {
	results map[string]Result
	calls   []fakeCall
}

type translateRoundTripFunc func(*http.Request) (*http.Response, error)

func (f translateRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func translateResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

type fakeCall struct {
	text    string
	actress []string
}

func (f *fakeEngine) Translate(ctx context.Context, text string, actress []string) (Result, error) {
	f.calls = append(f.calls, fakeCall{text: text, actress: append([]string(nil), actress...)})
	return f.results[text], nil
}
