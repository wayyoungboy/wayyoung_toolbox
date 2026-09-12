package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMediaIDFromTitle(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  string
		ok    bool
	}{
		{name: "standard", title: "[HD] 新作 [VENX-345]", want: "VENX-345", ok: true},
		{name: "numeric prefix", title: "259LUXU-1234 title", want: "259LUXU-1234", ok: true},
		{name: "fc2", title: "FC2-PPV-123456", want: "FC2-123456", ok: true},
		{name: "resolution is not an id", title: "FHD 1080P H265", ok: false},
		{name: "date is not an id", title: "release 2026-08", ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := mediaIDFromTitle(test.title)
			if got != test.want || ok != test.ok {
				t.Fatalf("mediaIDFromTitle(%q) = %q, %v; want %q, %v", test.title, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestCompletedMediaIndexRequiresVideoFile(t *testing.T) {
	input := t.TempDir()
	completed := filepath.Join(input, "#整理完成")
	organized := filepath.Join(completed, "actor", "[VENX-345] title")
	if err := os.MkdirAll(organized, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(organized, "VENX-345-C.mp4"), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(completed, "actor", "[VENX-346] empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := defaultJavspConfig()
	config.InputDirectory = input
	config.OutputFolderPattern = "#整理完成/{actress}/[{num}] {title}"
	index, err := buildCompletedMediaIndex(config)
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := index.MatchTitle("HD VENX-345"); !ok || id != "VENX-345" {
		t.Fatalf("organized movie was not indexed: id=%q ok=%v", id, ok)
	}
	if id, ok := index.MatchTitle("HD VENX-346"); ok {
		t.Fatalf("empty directory was indexed as completed: id=%q", id)
	}
}

func TestCompletedMediaIndexDoesNotScanWholeInput(t *testing.T) {
	config := defaultJavspConfig()
	config.InputDirectory = t.TempDir()
	config.OutputFolderPattern = "{actress}/[{num}] {title}"
	index, err := buildCompletedMediaIndex(config)
	if err != nil {
		t.Fatal(err)
	}
	if index.Available() {
		t.Fatal("placeholder-only output pattern must not scan the entire input directory")
	}
}
