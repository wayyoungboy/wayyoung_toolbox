package service

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type nfoProbeReader struct{ read func([]byte) (int, error) }

func (r nfoProbeReader) Read(p []byte) (int, error) { return r.read(p) }

func TestPublishQueuedNFOIsAtomicAndCleansFailedWrites(t *testing.T) {
	for _, failure := range []string{"none", "read_error", "canceled", "competing_file"} {
		t.Run(failure, func(t *testing.T) {
			root, err := os.OpenRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			data := "<movie><title>Atomic title</title></movie>"
			reads := 0
			injected := errors.New("interrupted NFO writer")
			reader := nfoProbeReader{read: func(p []byte) (int, error) {
				reads++
				if reads == 1 {
					return copy(p, data[:12]), nil
				}
				if _, err := root.Lstat("TEST-001.nfo"); !os.IsNotExist(err) {
					t.Errorf("partial final NFO was visible before publication: %v", err)
				}
				switch failure {
				case "read_error":
					return 0, injected
				case "canceled":
					cancel()
				case "competing_file":
					if err := os.WriteFile(filepath.Join(root.Name(), "TEST-001.nfo"), []byte("other owner"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				return copy(p, data[12:]), io.EOF
			}}
			err = publishQueuedNFO(ctx, root, "TEST-001.nfo", reader)
			switch failure {
			case "none":
				if err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(filepath.Join(root.Name(), "TEST-001.nfo"))
				if err != nil || string(got) != data {
					t.Fatalf("wrong published NFO: %q %v", got, err)
				}
			case "read_error":
				if !errors.Is(err, injected) {
					t.Fatalf("lost write error: %v", err)
				}
			case "canceled":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
			case "competing_file":
				if !errors.Is(err, errIntakeConflict) {
					t.Fatalf("lost publication conflict: %v", err)
				}
				got, err := os.ReadFile(filepath.Join(root.Name(), "TEST-001.nfo"))
				if err != nil || string(got) != "other owner" {
					t.Fatalf("overwrote competing file: %q %v", got, err)
				}
			}
			entries, err := os.ReadDir(root.Name())
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() != "TEST-001.nfo" {
					t.Errorf("temporary file leaked: %s", entry.Name())
				}
			}
			if failure == "read_error" || failure == "canceled" {
				if len(entries) != 0 {
					t.Fatal("failed writer published a file")
				}
				if err := publishQueuedNFO(context.Background(), root, "TEST-001.nfo", strings.NewReader(data)); err != nil {
					t.Fatalf("clean retry failed: %v", err)
				}
			}
		})
	}
}
