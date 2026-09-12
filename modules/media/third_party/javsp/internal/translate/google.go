package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"time"
)

type GoogleEngine struct {
	BaseURL             string
	Client              *http.Client
	RateLimitWait       time.Duration
	MaxRateLimitRetries int
	PostRequestDelay    time.Duration
}

func (e GoogleEngine) Translate(ctx context.Context, text string, actress []string) (Result, error) {
	client := defaultClient(e.Client)
	endpoint := e.BaseURL
	if endpoint == "" {
		endpoint = "https://translate.google.com.hk/translate_a/single"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return Result{}, err
	}
	query := parsed.Query()
	query.Set("client", "gtx")
	query.Set("dt", "t")
	query.Set("dj", "1")
	query.Set("ie", "UTF-8")
	query.Set("sl", "auto")
	query.Set("tl", "zh_CN")
	query.Set("q", text)
	parsed.RawQuery = query.Encode()

	rateLimitWait := e.RateLimitWait
	useOriginalBackoff := rateLimitWait == 0
	if useOriginalBackoff {
		rateLimitWait = 60 * time.Second
	} else if rateLimitWait < 0 {
		rateLimitWait = 0
	}
	postRequestDelay := e.PostRequestDelay
	if postRequestDelay == 0 {
		postRequestDelay = 4 * time.Second
	} else if postRequestDelay < 0 {
		postRequestDelay = 0
	}
	maxRateLimitRetries := e.MaxRateLimitRetries
	if maxRateLimitRetries == 0 {
		maxRateLimitRetries = 2
	} else if maxRateLimitRetries < 0 {
		maxRateLimitRetries = 0
	}

	var resp *http.Response
	rateLimitedBefore := false
	rateLimitRetries := 0
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return Result{}, err
		}
		resp, err = client.Do(req)
		if err != nil {
			return Result{}, err
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			break
		}
		_ = resp.Body.Close()
		if rateLimitRetries >= maxRateLimitRetries {
			return Result{}, fmt.Errorf("google: http %d", resp.StatusCode)
		}
		rateLimitRetries++
		if rateLimitedBefore && useOriginalBackoff {
			rateLimitWait += time.Duration(60+rand.Intn(31)) * time.Second
		}
		rateLimitedBefore = true
		if err := waitGoogleContext(ctx, rateLimitWait); err != nil {
			return Result{}, err
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if err := waitGoogleContext(ctx, postRequestDelay); err != nil {
			return Result{}, err
		}
		return Result{}, fmt.Errorf("google: http %d", resp.StatusCode)
	}
	var decoded googleResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Result{}, err
	}
	if len(decoded.Sentences) == 0 {
		return Result{}, fmt.Errorf("google: empty sentences")
	}
	if err := waitGoogleContext(ctx, postRequestDelay); err != nil {
		return Result{}, err
	}
	result := Result{}
	for _, sentence := range decoded.Sentences {
		result.Text += sentence.Trans
		result.OrigBreak = append(result.OrigBreak, sentence.Orig)
		result.TransBreak = append(result.TransBreak, sentence.Trans)
	}
	return result, nil
}

func waitGoogleContext(ctx context.Context, delay time.Duration) error {
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

type googleResponse struct {
	Sentences []struct {
		Orig  string `json:"orig"`
		Trans string `json:"trans"`
	} `json:"sentences"`
}
