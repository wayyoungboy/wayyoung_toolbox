package httpclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadLocalFileHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.jpg")
	destination := filepath.Join(root, "destination.jpg")
	if err := os.WriteFile(source, []byte("poster"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Download(ctx, nil, source, destination)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Download error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination exists after cancellation: %v", err)
	}
}
