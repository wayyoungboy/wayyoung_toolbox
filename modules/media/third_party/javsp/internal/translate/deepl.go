package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	deepLFreeEndpoint = "https://api-free.deepl.com/v2/translate"
	deepLProEndpoint  = "https://api.deepl.com/v2/translate"
)

type DeepLEngine struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

type deepLRequest struct {
	Text               []string `json:"text"`
	TargetLang         string   `json:"target_lang"`
	PreserveFormatting bool     `json:"preserve_formatting"`
}

type deepLResponse struct {
	Translations []struct {
		Text string `json:"text"`
	} `json:"translations"`
}

func (e DeepLEngine) Translate(ctx context.Context, text string, actress []string) (Result, error) {
	payload, err := json.Marshal(deepLRequest{
		Text:               []string{text},
		TargetLang:         "ZH-HANS",
		PreserveFormatting: true,
	})
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deepLEndpoint(e.APIKey, e.BaseURL), bytes.NewReader(payload))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "DeepL-Auth-Key "+e.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := defaultClient(e.Client).Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("deepl: http %d", resp.StatusCode)
	}
	var decoded deepLResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Result{}, err
	}
	if len(decoded.Translations) == 0 || strings.TrimSpace(decoded.Translations[0].Text) == "" {
		return Result{}, fmt.Errorf("deepl: empty translations")
	}
	return Result{Text: strings.TrimSpace(decoded.Translations[0].Text)}, nil
}

func deepLEndpoint(apiKey, override string) string {
	if endpoint := strings.TrimSpace(override); endpoint != "" {
		return endpoint
	}
	if strings.HasSuffix(strings.TrimSpace(apiKey), ":fx") {
		return deepLFreeEndpoint
	}
	return deepLProEndpoint
}
