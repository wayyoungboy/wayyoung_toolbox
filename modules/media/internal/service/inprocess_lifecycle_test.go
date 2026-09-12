package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServiceRunsInProcessWithoutListener(t *testing.T) {
	service, err := NewService(t.TempDir(), t.TempDir(), t.TempDir(), "/app/wayyoung_toolbox/modules/media", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/app/wayyoung_toolbox/modules/media/api/health", nil)
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", response.Code, response.Body.String())
	}

	cancel()
	shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := service.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
}
