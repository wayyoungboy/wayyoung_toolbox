package translate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewDeepLEngineFromConfig(t *testing.T) {
	engine, err := NewEngineFromConfig(map[string]any{
		"name":    "deepl",
		"api_key": "test-key:fx",
	}, nil)
	if err != nil {
		t.Fatalf("NewEngineFromConfig returned error: %v", err)
	}
	deepl, ok := engine.(DeepLEngine)
	if !ok {
		t.Fatalf("engine type = %T, want DeepLEngine", engine)
	}
	if deepl.APIKey != "test-key:fx" {
		t.Fatalf("API key = %q", deepl.APIKey)
	}
}

func TestDeepLTranslateUsesOfficialRequestContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "DeepL-Auth-Key test-key:fx" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		var body struct {
			Text       []string `json:"text"`
			TargetLang string   `json:"target_lang"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Text) != 1 || body.Text[0] != "raw text" || body.TargetLang != "ZH-HANS" {
			t.Fatalf("request body = %#v", body)
		}
		_, _ = w.Write([]byte(`{"translations":[{"detected_source_language":"JA","text":"翻译结果"}]}`))
	}))
	defer server.Close()

	engine := DeepLEngine{BaseURL: server.URL, APIKey: "test-key:fx", Client: server.Client()}
	result, err := engine.Translate(context.Background(), "raw text", nil)
	if err != nil {
		t.Fatalf("Translate returned error: %v", err)
	}
	if result.Text != "翻译结果" {
		t.Fatalf("Text = %q", result.Text)
	}
}

func TestDeepLEndpointSelectsFreeAPIForFreeKey(t *testing.T) {
	if got := deepLEndpoint("test-key:fx", ""); got != "https://api-free.deepl.com/v2/translate" {
		t.Fatalf("free endpoint = %q", got)
	}
	if got := deepLEndpoint("paid-key", ""); got != "https://api.deepl.com/v2/translate" {
		t.Fatalf("paid endpoint = %q", got)
	}
	if got := deepLEndpoint("test-key:fx", "https://translator.example/v2/translate"); got != "https://translator.example/v2/translate" {
		t.Fatalf("override endpoint = %q", got)
	}
}

func TestDeepLTranslateReturnsBoundedRateLimitError(t *testing.T) {
	client := &http.Client{Transport: translateRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return translateResponse(http.StatusTooManyRequests, `{"message":"Too many requests"}`), nil
	})}
	engine := DeepLEngine{BaseURL: "https://translate.example/v2/translate", APIKey: "test-key:fx", Client: client}
	_, err := engine.Translate(context.Background(), "raw", nil)
	if err == nil || !strings.Contains(err.Error(), "deepl: http 429") {
		t.Fatalf("error = %v, want bounded DeepL 429 error", err)
	}
}
