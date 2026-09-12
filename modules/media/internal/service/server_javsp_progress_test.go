package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestJavspStatusIncludesStructuredEmbeddedProgress(t *testing.T) {
	service := testService(t)
	service.javsp.mu.Lock()
	service.javsp.lastProgress = JavspProgress{
		Phase: "processing", Movie: "TEST-001", Site: "javbus", Message: "正在抓取",
		Current: 2, Total: 5, Processed: 1,
	}
	service.javsp.mu.Unlock()

	request := httptest.NewRequest(http.MethodGet, "/api/javsp", nil)
	request.Header.Set("X-Trim-Isadmin", "true")
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Progress  JavspProgress  `json:"progress"`
		Component map[string]any `json:"component"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Progress.Phase != "processing" || payload.Progress.Movie != "TEST-001" || payload.Progress.Site != "javbus" || payload.Progress.Current != 2 || payload.Progress.Total != 5 {
		t.Fatalf("progress=%#v", payload.Progress)
	}
	if payload.Component["mode"] != "embedded" {
		t.Fatalf("component=%#v", payload.Component)
	}
}

func TestAdminCanCancelRunningEmbeddedJavsp(t *testing.T) {
	service := testService(t)
	bundle, data := javspTestRuntime(t)
	fake := &fakeEmbeddedJavsp{started: make(chan struct{})}
	service.javsp = newJavspRunnerWithEngine(bundle, data, fake)
	config, err := service.db.JavspConfigPrivate()
	if err != nil {
		t.Fatal(err)
	}
	config.InputDirectory = t.TempDir()
	config.MinimumSize = "0B"
	config.TimeoutSeconds = 30
	if _, err := service.db.UpdateJavspConfig(config); err != nil {
		t.Fatal(err)
	}
	writeIntakeFixture(t, config.InputDirectory, map[string]string{"TEST-001.mp4": "video"})
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()

	runRequest := httptest.NewRequest(http.MethodPost, "/api/javsp/run", bytes.NewBufferString("{}"))
	runRequest.Header.Set("X-Trim-Isadmin", "true")
	runResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(runResponse, runRequest)
	if runResponse.Code != http.StatusAccepted {
		t.Fatalf("run status=%d: %s", runResponse.Code, runResponse.Body.String())
	}
	service.wait.Wait() // The HTTP action discovers and queues; execution is independent.
	done := make(chan error, 1)
	go func() { done <- service.drainPendingMovies(ctx) }()
	select {
	case <-fake.started:
	case <-time.After(2 * time.Second):
		t.Fatal("embedded JAVSP did not start")
	}

	cancelRequest := httptest.NewRequest(http.MethodPost, "/api/javsp/cancel", http.NoBody)
	cancelRequest.Header.Set("X-Trim-Isadmin", "true")
	cancelResponse := httptest.NewRecorder()
	service.Handler().ServeHTTP(cancelResponse, cancelRequest)
	if cancelResponse.Code != http.StatusAccepted {
		t.Fatalf("cancel status=%d: %s", cancelResponse.Code, cancelResponse.Body.String())
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("queue did not settle cancellation")
	}
	if progress := service.javsp.LastProgress(); progress.Phase != "canceled" {
		t.Fatalf("progress=%#v, want canceled", progress)
	}
	jobs, err := service.db.ListMediaJobs("", 10)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "canceled" {
		t.Fatalf("queue did not preserve cancellation: %+v %v", jobs, err)
	}
}
