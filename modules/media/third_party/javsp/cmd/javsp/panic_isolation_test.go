package main

import (
	"context"
	"strings"
	"testing"
)

func TestRunMovieJobsConvertsMoviePanicToFailure(t *testing.T) {
	results := runMovieJobs(context.Background(), 1, 1, 0, func(int) (bool, error) {
		panic("crawler fixture panic")
	})
	result, ok := <-results
	if !ok {
		t.Fatal("result channel closed without reporting the failed movie")
	}
	if result.err == nil || !strings.Contains(result.err.Error(), "panic") {
		t.Fatalf("result error = %v, want isolated panic error", result.err)
	}
	if _, open := <-results; open {
		t.Fatal("result channel remained open")
	}
}
