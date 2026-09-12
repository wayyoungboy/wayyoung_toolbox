package translate

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuukiy/javsp-go/internal/model"
)

type Result struct {
	Text       string
	OrigBreak  []string
	TransBreak []string
}

type Fields struct {
	Title bool
	Plot  bool
}

type Engine interface {
	Translate(ctx context.Context, text string, actress []string) (Result, error)
}

func NewEngineFromConfig(raw any, client *http.Client) (Engine, error) {
	if raw == nil {
		return nil, nil
	}
	values, ok := raw.(map[string]any)
	if !ok {
		if converted, ok := raw.(map[interface{}]interface{}); ok {
			values = make(map[string]any, len(converted))
			for key, value := range converted {
				if keyString, ok := key.(string); ok {
					values[keyString] = value
				}
			}
		} else {
			return nil, fmt.Errorf("translator engine must be a map")
		}
	}
	name := stringValue(values, "name")
	switch name {
	case "", "none":
		return nil, nil
	case "openai":
		return OpenAIEngine{
			URL:    stringValue(values, "url"),
			APIKey: stringValue(values, "api_key"),
			Model:  stringValue(values, "model"),
			Client: client,
		}, nil
	case "google":
		return GoogleEngine{Client: client}, nil
	case "deepl":
		return DeepLEngine{
			BaseURL: stringValue(values, "url"),
			APIKey:  stringValue(values, "api_key"),
			Client:  client,
		}, nil
	case "bing":
		return BingEngine{APIKey: stringValue(values, "api_key"), Client: client}, nil
	case "baidu":
		return BaiduEngine{
			AppID:  stringValue(values, "app_id"),
			APIKey: stringValue(values, "api_key"),
			Client: client,
		}, nil
	case "claude":
		return ClaudeEngine{APIKey: stringValue(values, "api_key"), Client: client}, nil
	default:
		return nil, fmt.Errorf("unsupported translator engine: %s", name)
	}
}

func stringValue(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

type NoopEngine struct{}

func (NoopEngine) Translate(ctx context.Context, text string, actress []string) (Result, error) {
	return Result{Text: text}, nil
}

func Apply(ctx context.Context, info *model.MovieInfo, engine Engine, fields Fields) (bool, error) {
	if engine == nil {
		engine = NoopEngine{}
	}
	if fields.Title && info.Title != "" && info.OriginalTitle == "" {
		result, err := engine.Translate(ctx, info.Title, info.Actress)
		if err != nil {
			return false, err
		}
		info.OriginalTitle = info.Title
		info.Title = result.Text
		info.OriginalTitleBreak = append([]string(nil), result.OrigBreak...)
		info.TitleBreak = append([]string(nil), result.TransBreak...)
	}
	if fields.Plot && info.Plot != "" {
		result, err := engine.Translate(ctx, info.Plot, info.Actress)
		if err != nil {
			return false, err
		}
		info.OriginalPlot = info.Plot
		info.Plot = result.Text
	}
	return true, nil
}

type OpenAIEngine struct {
	URL    string
	APIKey string
	Model  string
	Client *http.Client
}

func (e OpenAIEngine) Translate(ctx context.Context, text string, actress []string) (Result, error) {
	client := e.Client
	if client == nil {
		client = http.DefaultClient
	}
	body := map[string]any{
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "Translate the following Japanese paragraph into zh_CN, while leaving non-Japanese text, names, or text that does not look like Japanese untranslated. Reply with the translated text only, do not add any text that is not in the original content.",
			},
			{"role": "user", "content": text},
		},
		"model":       e.Model,
		"temperature": 0,
		"max_tokens":  1024,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.URL, bytes.NewReader(payload))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	var decoded openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Result{}, err
	}
	if resp.StatusCode != http.StatusOK {
		if decoded.Error.Message != "" {
			return Result{}, fmt.Errorf("openai: http %d: %s", resp.StatusCode, decoded.Error.Message)
		}
		return Result{}, fmt.Errorf("openai: http %d", resp.StatusCode)
	}
	if decoded.Error.Message != "" {
		return Result{}, fmt.Errorf("openai: %s", decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 {
		return Result{}, fmt.Errorf("openai: empty choices")
	}
	return Result{Text: strings.TrimSpace(decoded.Choices[0].Message.Content)}, nil
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

type BingEngine struct {
	URL    string
	APIKey string
	Client *http.Client
}

func (e BingEngine) Translate(ctx context.Context, text string, actress []string) (Result, error) {
	client := defaultClient(e.Client)
	endpoint := e.URL
	if endpoint == "" {
		endpoint = "https://api.cognitive.microsofttranslator.com/translate"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return Result{}, err
	}
	query := parsed.Query()
	query.Set("api-version", "3.0")
	query.Set("to", "zh-Hans")
	query.Set("includeSentenceLength", "true")
	parsed.RawQuery = query.Encode()

	protected := protectActressNames(text, actress)
	body := []map[string]string{{"text": protected}}
	payload, err := marshalJSONNoEscape(body)
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), bytes.NewReader(payload))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.APIKey != "" {
		req.Header.Set("Ocp-Apim-Subscription-Key", e.APIKey)
		req.Header.Set("Ocp-Apim-Subscription-Region", "global")
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	var decoded []bingResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Result{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("bing: http %d", resp.StatusCode)
	}
	if len(decoded) == 0 || len(decoded[0].Translations) == 0 {
		return Result{}, fmt.Errorf("bing: empty translations")
	}
	translation := decoded[0].Translations[0]
	result := Result{Text: translation.Text}
	for _, size := range translation.SentLen.SrcSentLen {
		result.OrigBreak = append(result.OrigBreak, takeRunes(&protected, size))
	}
	remaining := translation.Text
	var transParts []string
	for _, size := range translation.SentLen.TransSentLen {
		part := strings.TrimRight(takeRunes(&remaining, size), " ")
		result.TransBreak = append(result.TransBreak, part)
		transParts = append(transParts, part)
	}
	if len(transParts) > 0 {
		result.Text = strings.Join(transParts, "")
	}
	return result, nil
}

func protectActressNames(text string, actress []string) string {
	out := text
	for _, name := range actress {
		if name == "" {
			continue
		}
		tag := fmt.Sprintf(`<mstrans:dictionary translation="%s">%s</mstrans:dictionary>`, name, name)
		out = strings.ReplaceAll(out, name, tag)
	}
	return out
}

type bingResponse struct {
	Translations []struct {
		Text    string `json:"text"`
		SentLen struct {
			SrcSentLen   []int `json:"srcSentLen"`
			TransSentLen []int `json:"transSentLen"`
		} `json:"sentLen"`
	} `json:"translations"`
}

type BaiduEngine struct {
	URL         string
	AppID       string
	APIKey      string
	Client      *http.Client
	MinInterval time.Duration
}

var (
	baiduRateMu     sync.Mutex
	baiduLastAccess time.Time
)

func (e BaiduEngine) Translate(ctx context.Context, text string, actress []string) (Result, error) {
	minimumInterval := e.MinInterval
	if minimumInterval == 0 {
		minimumInterval = time.Second
	} else if minimumInterval < 0 {
		minimumInterval = 0
	}
	baiduRateMu.Lock()
	defer baiduRateMu.Unlock()
	if !baiduLastAccess.IsZero() {
		remaining := minimumInterval - time.Since(baiduLastAccess)
		if err := waitContext(ctx, remaining); err != nil {
			return Result{}, err
		}
	}

	client := defaultClient(e.Client)
	endpoint := e.URL
	if endpoint == "" {
		endpoint = "https://api.fanyi.baidu.com/api/trans/vip/translate"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return Result{}, err
	}
	salt := strconv.Itoa(rand.Intn(0x7fffffff))
	sum := md5.Sum([]byte(e.AppID + text + salt + e.APIKey))
	query := parsed.Query()
	query.Set("appid", e.AppID)
	query.Set("q", text)
	query.Set("from", "auto")
	query.Set("to", "zh")
	query.Set("salt", salt)
	query.Set("sign", hex.EncodeToString(sum[:]))
	parsed.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	var decoded baiduResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Result{}, err
	}
	baiduLastAccess = time.Now()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("baidu: http %d", resp.StatusCode)
	}
	if decoded.ErrorCode != "" {
		return Result{}, fmt.Errorf("baidu: %s: %s", decoded.ErrorCode, decoded.ErrorMsg)
	}
	parts := make([]string, 0, len(decoded.TransResult))
	for _, item := range decoded.TransResult {
		parts = append(parts, item.Dst)
	}
	return Result{Text: strings.Join(parts, "\n")}, nil
}

type baiduResponse struct {
	ErrorCode   string `json:"error_code"`
	ErrorMsg    string `json:"error_msg"`
	TransResult []struct {
		Dst string `json:"dst"`
	} `json:"trans_result"`
}

type ClaudeEngine struct {
	URL    string
	APIKey string
	Client *http.Client
}

func (e ClaudeEngine) Translate(ctx context.Context, text string, actress []string) (Result, error) {
	client := defaultClient(e.Client)
	endpoint := e.URL
	if endpoint == "" {
		endpoint = "https://api.anthropic.com/v1/messages"
	}
	body := map[string]any{
		"model":      "claude-3-haiku-20240307",
		"system":     "Translate the following Japanese paragraph into zh_CN, while leaving non-Japanese text, names, or text that does not look like Japanese untranslated. Reply with the translated text only, do not add any text that is not in the original content.",
		"max_tokens": 1024,
		"messages":   []map[string]string{{"role": "user", "content": text}},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if e.APIKey != "" {
		req.Header.Set("x-api-key", e.APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	var decoded claudeResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Result{}, err
	}
	if resp.StatusCode != http.StatusOK {
		if decoded.Error.Message != "" {
			return Result{}, fmt.Errorf("claude: http %d: %s", resp.StatusCode, decoded.Error.Message)
		}
		return Result{}, fmt.Errorf("claude: http %d", resp.StatusCode)
	}
	if len(decoded.Content) == 0 {
		return Result{}, fmt.Errorf("claude: empty content")
	}
	return Result{Text: strings.TrimSpace(decoded.Content[0].Text)}, nil
}

type claudeResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func defaultClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return http.DefaultClient
}

func waitContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func marshalJSONNoEscape(value any) ([]byte, error) {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(b.Bytes()), nil
}

func takeRunes(s *string, count int) string {
	if count <= 0 || *s == "" {
		return ""
	}
	runes := []rune(*s)
	if count > len(runes) {
		count = len(runes)
	}
	part := string(runes[:count])
	*s = string(runes[count:])
	return part
}
