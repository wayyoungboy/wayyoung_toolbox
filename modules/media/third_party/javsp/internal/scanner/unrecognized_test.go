package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanReportsUnrecognizedMediaWithoutChangingFiles(t *testing.T) {
	root := t.TempDir()
	unknown := "489155.com@[中文字幕]Blacked.26.07.12.Emiri.Momota.4k-C.mp4"
	makeFile(t, root, unknown, 32)
	makeFile(t, root, "ABC-123.mp4", 32)
	makeFile(t, root, "unknown-small.mp4", 4)
	makeFile(t, root, "unknown.txt", 32)
	makeFile(t, root, ".ignored/unknown.mp4", 32)
	var reports []UnrecognizedFile
	opts := Options{
		FilenameExtensions: []string{".mp4"}, MinimumSize: 16,
		IgnoredFolderNamePattern: []string{`^\.`},
		OnUnrecognized:           func(file UnrecognizedFile) { reports = append(reports, file) },
	}
	movies, err := Scan(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 1 || movies[0].DVDID != "ABC-123" {
		t.Fatalf("movies = %+v; want only ABC-123", movies)
	}
	if len(reports) != 1 || reports[0].Path != filepath.Join(root, unknown) || reports[0].Reason != "no_supported_identifier" {
		t.Fatalf("reports = %+v; want unsupported filename reported once", reports)
	}
	info, err := os.Stat(filepath.Join(root, unknown))
	if err != nil || info.Size() != 32 {
		t.Fatalf("unrecognized media changed: info=%v err=%v", info, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 5 {
		t.Fatalf("scanner created artifacts: entries=%v err=%v", entries, err)
	}
}

func TestScanUnrecognizedMediaWithoutReporterStillWorks(t *testing.T) {
	root := t.TempDir()
	makeFile(t, root, "[未识别].mp4", 32)
	movies, err := Scan(root, Options{FilenameExtensions: []string{".mp4"}})
	if err != nil || len(movies) != 0 {
		t.Fatalf("movies=%+v err=%v", movies, err)
	}
}
