package integrity

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestFile(t *testing.T, directory, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func installTestTool(t *testing.T, name, body string) string {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	return path
}

func TestDetectKindUsesNamesAndContent(t *testing.T) {
	directory := t.TempDir()
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"manual.pdf", []byte("%PDF"), kindPDF},
		{"movie.mkv", []byte("media"), kindMedia},
		{"cover.webp", []byte("image"), kindImage},
		{"bundle.7z", []byte("archive"), kindArchive},
		{"data.sqlite", []byte("SQLite format 3\x00"), kindSQLite},
		{"metadata.xml", []byte("<root/>"), kindXML},
		{"metadata.nfo", []byte("  <movie/>"), kindXML},
		{"link.nfo", []byte("https://example.test/title"), kindGeneric},
		{"unknown.db", []byte("application data"), kindGeneric},
		{"database.db", append([]byte("SQLite format 3\x00"), make([]byte, 20)...), kindSQLite},
		{"movie.mp4.downloading", []byte("partial"), kindIncomplete},
		{"._cover.jpg", []byte("metadata"), kindSystem},
		{"Thumbs.db", []byte("metadata"), kindSystem},
	}
	for _, test := range tests {
		path := writeTestFile(t, directory, test.name, test.data)
		if got := detectKind(path); got != test.want {
			t.Errorf("detectKind(%s)=%q want=%q", test.name, got, test.want)
		}
	}
}

func TestCheckFileHandlesGenericMetadataIncompleteAndXML(t *testing.T) {
	directory := t.TempDir()
	cases := []struct {
		path string
		mode string
		want string
	}{
		{writeTestFile(t, directory, "plain.txt", bytes.Repeat([]byte("a"), 2<<20)), "quick", statusHealthy},
		{writeTestFile(t, directory, "deep.bin", []byte("checksum me")), "deep", statusHealthy},
		{writeTestFile(t, directory, "empty.txt", nil), "quick", statusWarning},
		{writeTestFile(t, directory, ".DS_Store", []byte("metadata")), "quick", statusSkipped},
		{writeTestFile(t, directory, "video.mkv.part", []byte("partial")), "quick", statusWarning},
		{writeTestFile(t, directory, "good.xml", []byte("<root><item/></root>")), "quick", statusHealthy},
		{writeTestFile(t, directory, "bad.xml", []byte("<root>")), "quick", statusDamaged},
		{writeTestFile(t, directory, "bad.sqlite", []byte("not sqlite")), "quick", statusDamaged},
	}
	for _, test := range cases {
		result := checkFile(t.Context(), test.path, test.mode)
		if result.Status != test.want {
			t.Errorf("checkFile(%s)=%q want=%q message=%s", filepath.Base(test.path), result.Status, test.want, result.Message)
		}
		if result.DurationMS < 0 || result.Method == "" {
			t.Errorf("result metadata missing: %+v", result)
		}
		if test.mode == "deep" && test.want == statusHealthy && len(result.Checksum) != 64 {
			t.Errorf("deep checksum missing: %+v", result)
		}
	}
}

func TestCheckImageAndGzipWithoutExternalTools(t *testing.T) {
	directory := t.TempDir()
	imagePath := filepath.Join(directory, "pixel.png")
	file, err := os.Create(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	picture := image.NewRGBA(image.Rect(0, 0, 2, 2))
	picture.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(file, picture); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if result := checkFile(t.Context(), imagePath, "deep"); result.Status != statusHealthy {
		t.Fatalf("PNG result=%+v", result)
	}

	gzipPath := filepath.Join(directory, "valid.gz")
	gzipFile, err := os.Create(gzipPath)
	if err != nil {
		t.Fatal(err)
	}
	zipWriter := gzip.NewWriter(gzipFile)
	_, _ = zipWriter.Write([]byte("complete payload"))
	_ = zipWriter.Close()
	_ = gzipFile.Close()
	if result := checkFile(t.Context(), gzipPath, "deep"); result.Status != statusHealthy {
		t.Fatalf("gzip result=%+v", result)
	}
	broken := writeTestFile(t, directory, "broken.gz", []byte("not gzip"))
	if result := checkFile(t.Context(), broken, "deep"); result.Status != statusDamaged {
		t.Fatalf("broken gzip result=%+v", result)
	}
}

func TestExternalValidatorsClassifySuccessFailureProtectionAndTimeout(t *testing.T) {
	tool := installTestTool(t, "validator", `case "$1" in
success) echo ok; exit 0 ;;
protected) echo 'Wrong password' >&2; exit 2 ;;
timeout) /bin/sleep 1; exit 0 ;;
*) echo corrupt >&2; exit 2 ;;
esac`)
	if status, _, _ := runValidator(t.Context(), 5*time.Second, tool, []string{"success"}, "test", false); status != statusHealthy {
		t.Fatalf("success status=%q", status)
	}
	if status, _, _ := runValidator(t.Context(), 5*time.Second, tool, []string{"failure"}, "test", false); status != statusDamaged {
		t.Fatalf("failure status=%q", status)
	}
	if status, _, _ := runValidator(t.Context(), 5*time.Second, tool, []string{"protected"}, "test", true); status != statusWarning {
		t.Fatalf("protected status=%q", status)
	}
	if status, _, _ := runValidator(t.Context(), 10*time.Millisecond, tool, []string{"timeout"}, "test", false); status != statusWarning {
		t.Fatalf("timeout status=%q", status)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if status, _, _ := runValidator(ctx, time.Second, tool, []string{"timeout"}, "test", false); status != statusSkipped {
		t.Fatalf("cancelled status=%q", status)
	}
}

func TestFormatCheckersUseAvailableTools(t *testing.T) {
	directory := t.TempDir()
	pdf := writeTestFile(t, directory, "book.pdf", []byte("%PDF-1.7\n%%EOF\n"))
	installTestTool(t, "gs", "echo rendered; exit 0")
	if status, _, method := checkPDFDeep(t.Context(), pdf, "fallback"); status != statusHealthy || !strings.Contains(method, "Ghostscript") {
		t.Fatalf("PDF deep status=%q method=%q", status, method)
	}

	archive := writeTestFile(t, directory, "bundle.zip", []byte("archive"))
	installTestTool(t, "7z", "echo Everything-is-Ok; exit 0")
	if status, _, _ := checkArchive(t.Context(), archive, "quick"); status != statusHealthy {
		t.Fatalf("archive status=%q", status)
	}

	media := writeTestFile(t, directory, "movie.mkv", []byte("media"))
	installTestTool(t, "ffprobe", "echo 10.0; exit 0")
	if status, _, _ := checkMedia(t.Context(), media, "quick"); status != statusHealthy {
		t.Fatalf("media status=%q", status)
	}
	installTestTool(t, "ffmpeg", "exit 0")
	if status, _, _ := checkMedia(t.Context(), media, "deep"); status != statusHealthy {
		t.Fatalf("deep media status=%q", status)
	}

	database := writeTestFile(t, directory, "data.sqlite", []byte("SQLite format 3\x00payload"))
	installTestTool(t, "sqlite3", "echo ok; exit 0")
	if status, _, _ := checkSQLite(t.Context(), database); status != statusHealthy {
		t.Fatalf("sqlite status=%q", status)
	}
}

func TestCheckerFallbacksRemainInconclusiveInsteadOfFalseDamage(t *testing.T) {
	directory := t.TempDir()
	pdf := writeTestFile(t, directory, "book.pdf", []byte("%PDF-1.7\n%%EOF\n"))
	archive := writeTestFile(t, directory, "bundle.rar", []byte("archive"))
	validDatabase := writeTestFile(t, directory, "valid.sqlite", append([]byte("SQLite format 3\x00"), make([]byte, 128)...))
	invalidDatabase := writeTestFile(t, directory, "invalid.sqlite", []byte("invalid"))
	badImage := writeTestFile(t, directory, "bad.jpg", []byte("not a jpeg"))
	t.Setenv("PATH", t.TempDir())

	if status, _, _ := checkPDFDeep(t.Context(), pdf, "markers"); status != statusWarning {
		t.Fatalf("PDF fallback status=%q", status)
	}
	if status, _, _ := checkArchive(t.Context(), archive, "quick"); status != statusWarning {
		t.Fatalf("archive fallback status=%q", status)
	}
	if status, _, _ := checkSQLite(t.Context(), validDatabase); status != statusWarning {
		t.Fatalf("SQLite fallback status=%q", status)
	}
	if status, _, _ := checkSQLite(t.Context(), invalidDatabase); status != statusDamaged {
		t.Fatalf("invalid SQLite status=%q", status)
	}
	if status, _, _ := checkImage(t.Context(), badImage, "quick"); status != statusWarning {
		t.Fatalf("unsupported image status=%q", status)
	}
}

func TestAlternatePDFAndArchiveTools(t *testing.T) {
	directory := t.TempDir()
	pdf := writeTestFile(t, directory, "book.pdf", []byte("%PDF-1.7\n%%EOF\n"))
	archive := writeTestFile(t, directory, "bundle.zip", []byte("archive"))

	installTestTool(t, "qpdf", "exit 0")
	if status, _, method := checkPDFDeep(t.Context(), pdf, "fallback"); status != statusHealthy || !strings.Contains(method, "qpdf") {
		t.Fatalf("qpdf status=%q method=%q", status, method)
	}
	installTestTool(t, "pdfinfo", "exit 0")
	if status, _, method := checkPDFDeep(t.Context(), pdf, "fallback"); status != statusHealthy || !strings.Contains(method, "pdfinfo") {
		t.Fatalf("pdfinfo status=%q method=%q", status, method)
	}
	installTestTool(t, "7zz", "exit 0")
	if status, _, method := checkArchive(t.Context(), archive, "deep"); status != statusHealthy || !strings.Contains(method, "7-Zip") {
		t.Fatalf("7zz status=%q method=%q", status, method)
	}
	installTestTool(t, "unzip", "exit 0")
	if status, _, method := checkArchive(t.Context(), archive, "quick"); status != statusHealthy || !strings.Contains(method, "ZIP") {
		t.Fatalf("unzip status=%q method=%q", status, method)
	}
}

func TestAPKSigningBlockWarningIsNotReportedAsCorruption(t *testing.T) {
	directory := t.TempDir()
	apk := writeTestFile(t, directory, "signed.apk", []byte("archive"))
	installTestTool(t, "7z", "echo 'WARNING: There are data after the end of archive' >&2; exit 1")
	status, message, _ := checkArchive(t.Context(), apk, "quick")
	if status != statusWarning || !strings.Contains(message, "兼容") {
		t.Fatalf("APK warning status=%q message=%q", status, message)
	}
}

func TestLargeJPEGWithAppendedPayloadIsRecognized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resource.jpg")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate((512 << 20) + 1); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if !imageMayContainAppendedPayload(path) {
		t.Fatal("large JPEG should be treated as possibly carrying appended data")
	}
	if imageMayContainAppendedPayload(writeTestFile(t, t.TempDir(), "small.png", []byte("x"))) {
		t.Fatal("PNG must not be treated as appended JPEG payload")
	}
}

func TestDiscoverFilesDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "one.txt", []byte("one"))
	outside := t.TempDir()
	writeTestFile(t, outside, "secret.txt", []byte("secret"))
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	files, issues, err := discoverFiles(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || len(issues) != 1 || issues[0].Status != statusSkipped {
		t.Fatalf("files=%v issues=%+v", files, issues)
	}
	files, issues, err = discoverFiles(t.Context(), files[0])
	if err != nil || len(files) != 1 || len(issues) != 0 {
		t.Fatalf("single-file discovery failed: files=%v issues=%+v err=%v", files, issues, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := discoverFiles(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled discovery err=%v", err)
	}
}

func TestModuleConfigResultsValidationAndCancellation(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()
	public := t.TempDir()
	writeTestFile(t, public, "index.html", []byte("module"))
	module, err := New(Options{DataDir: data, PublicDir: public, Prefix: "/app/wayyoung_toolbox/modules/integrity", BrowseRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	defer module.Close(t.Context())

	for _, path := range []string{"/api/config", "/api/status", "/api/results?limit=bad"} {
		response := httptest.NewRecorder()
		module.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, module.options.Prefix+path, nil))
		if response.Code != http.StatusOK {
			t.Errorf("GET %s status=%d", path, response.Code)
		}
	}

	for _, body := range []string{`{`, `{"path":"relative","mode":"quick"}`, `{"path":"` + root + `","mode":"unknown"}`} {
		request := httptest.NewRequest(http.MethodPost, module.options.Prefix+"/api/scans", strings.NewReader(body))
		request.Header.Set("X-Trim-Isadmin", "true")
		response := httptest.NewRecorder()
		module.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("invalid scan body=%q status=%d", body, response.Code)
		}
	}

	module.mu.Lock()
	module.status.Running = true
	_, module.scanCancel = context.WithCancel(module.ctx)
	module.mu.Unlock()
	conflictRequest := httptest.NewRequest(http.MethodPost, module.options.Prefix+"/api/scans", strings.NewReader(`{"path":"`+root+`","mode":"quick"}`))
	conflictRequest.Header.Set("X-Trim-Isadmin", "true")
	conflict := httptest.NewRecorder()
	module.Handler().ServeHTTP(conflict, conflictRequest)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("duplicate scan status=%d", conflict.Code)
	}
	cancelRequest := httptest.NewRequest(http.MethodDelete, module.options.Prefix+"/api/scans/current", nil)
	cancelRequest.Header.Set("X-Trim-Isadmin", "true")
	cancelResponse := httptest.NewRecorder()
	module.Handler().ServeHTTP(cancelResponse, cancelRequest)
	if cancelResponse.Code != http.StatusAccepted {
		t.Fatalf("cancel status=%d", cancelResponse.Code)
	}
	module.mu.Lock()
	module.status.Running = false
	module.scanCancel = nil
	module.mu.Unlock()
	noScan := httptest.NewRecorder()
	module.Handler().ServeHTTP(noScan, cancelRequest)
	if noScan.Code != http.StatusConflict {
		t.Fatalf("idle cancel status=%d", noScan.Code)
	}
}
