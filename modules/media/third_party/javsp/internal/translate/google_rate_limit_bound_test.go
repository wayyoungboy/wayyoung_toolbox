package translate

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGoogleTranslateStopsAfterBoundedRateLimitRetries(t *testing.T) {
	var attempts atomic.Int32
	client := &http.Client{Transport: translateRoundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts.Add(1)
		return translateResponse(http.StatusTooManyRequests, "rate limited"), nil
	})}
	engine := GoogleEngine{
		BaseURL:             "https://translate.example/single",
		Client:              client,
		RateLimitWait:       time.Millisecond,
		MaxRateLimitRetries: 2,
		PostRequestDelay:    -1,
	}
	_, err := engine.Translate(context.Background(), "raw", nil)
	if err == nil || !strings.Contains(err.Error(), "google: http 429") {
		t.Fatalf("error = %v, want bounded Google 429 error", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want initial request plus 2 retries", got)
	}
}
