package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuukiy/javsp-go/internal/scanner"
)

func TestScanMoviesWithDiagnosticsDistinguishesSkippedAndMissingFiles(t *testing.T) {
	for _, tc := range []struct {
		name      string
		files     []string
		wantCount int
		wantErr   string
		wantLog   string
	}{
		{"unknown only", []string{"Site.26.07.12.Person.Name.4k-C.mp4"}, 0, "未识别出番号或 CID", "文件未改动"},
		{"mixed", []string{"ABC-123.mp4", "Site.26.07.12.Person.Name.4k-C.mp4"}, 1, "", "未识别文件 1 个"},
		{"normal", []string{"ABC-123.mp4"}, 1, "", ""},
		{"empty", nil, 0, "no movie files found", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range tc.files {
				if err := os.WriteFile(filepath.Join(root, name), []byte("test"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			movies, err := scanMoviesWithDiagnostics(root, scanner.Options{FilenameExtensions: []string{".mp4"}}, &output)
			if len(movies) != tc.wantCount {
				t.Fatalf("movies=%+v", movies)
			}
			if tc.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("error=%v want=%q", err, tc.wantErr)
			}
			if tc.wantLog == "" && output.Len() > 0 {
				t.Fatalf("unexpected log: %s", &output)
			}
			if tc.wantLog != "" && !strings.Contains(output.String(), tc.wantLog) {
				t.Fatalf("log=%s want=%q", &output, tc.wantLog)
			}
		})
	}
}

func TestScanMoviesWithDiagnosticsPropagatesScanErrors(t *testing.T) {
	_, err := scanMoviesWithDiagnostics(filepath.Join(t.TempDir(), "missing"), scanner.Options{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("missing directory must fail")
	}
}

func TestScanMoviesWithDiagnosticsPropagatesOutputErrors(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "[未识别].mp4"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := scanMoviesWithDiagnostics(root, scanner.Options{FilenameExtensions: []string{".mp4"}}, diagnosticsFailWriter{})
	if err == nil || !strings.Contains(err.Error(), "permission") {
		t.Fatalf("error=%v", err)
	}
}

type diagnosticsFailWriter struct{}

func (diagnosticsFailWriter) Write([]byte) (int, error) {
	return 0, os.ErrPermission
}

func TestScanMoviesWithDiagnosticsLimitsDetailsAndKeepsCallback(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 105; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("[未识别]%d.mp4", i)), []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	count := 0
	_, err := scanMoviesWithDiagnostics(root, scanner.Options{
		FilenameExtensions: []string{".mp4"},
		OnUnrecognized:     func(scanner.UnrecognizedFile) { count++ },
	}, &output)
	if err == nil || count != 105 || strings.Count(output.String(), "跳过：") != 100 || !strings.Contains(output.String(), "未识别文件 105 个") {
		t.Fatalf("err=%v count=%d output=%s", err, count, &output)
	}
}
