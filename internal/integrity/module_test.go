package integrity

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveTargetStaysInsideConfiguredRoots(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	resolved, err := resolveTarget(inside, []string{root})
	if err != nil {
		t.Fatalf("inside path rejected: %v", err)
	}
	want, err := filepath.EvalSymlinks(inside)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("resolved=%q want=%q", resolved, want)
	}
	if _, err := resolveTarget(outside, []string{root}); err == nil {
		t.Fatal("outside path must be rejected")
	}
	if _, err := resolveTarget(link, []string{root}); err == nil {
		t.Fatal("symlink escaping a root must be rejected")
	}
}

func TestClassifyTransportStreamDoesNotMistakeTypeScriptForVideo(t *testing.T) {
	directory := t.TempDir()
	typeScript := filepath.Join(directory, "app.ts")
	video := filepath.Join(directory, "capture.ts")
	if err := os.WriteFile(typeScript, []byte("export const answer: number = 42;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	packet := bytes.Repeat([]byte{0}, 188*3)
	packet[0], packet[188], packet[376] = 0x47, 0x47, 0x47
	if err := os.WriteFile(video, packet, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := detectKind(typeScript); got != kindGeneric {
		t.Fatalf("TypeScript classified as %q", got)
	}
	if got := detectKind(video); got != kindMedia {
		t.Fatalf("MPEG-TS classified as %q", got)
	}
}

func TestPDFStructureRequiresHeaderAndEOF(t *testing.T) {
	directory := t.TempDir()
	valid := filepath.Join(directory, "valid.pdf")
	cutOff := filepath.Join(directory, "cut-off.pdf")
	if err := os.WriteFile(valid, []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\nstartxref\n9\n%%EOF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cutOff, []byte("%PDF-1.7\n1 0 obj\n<<>>"), 0o600); err != nil {
		t.Fatal(err)
	}

	if status, _ := checkPDFMarkers(valid); status != statusHealthy {
		t.Fatalf("valid PDF status=%q", status)
	}
	if status, _ := checkPDFMarkers(cutOff); status != statusDamaged {
		t.Fatalf("cut-off PDF status=%q", status)
	}
}

func TestModuleScansAndPersistsAReport(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()
	public := t.TempDir()
	if err := os.WriteFile(filepath.Join(public, "index.html"), []byte("integrity"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "good.pdf"), []byte("%PDF-1.4\nstartxref\n0\n%%EOF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.pdf"), []byte("%PDF-1.4\ntruncated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "movie.mkv.part"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	module, err := New(Options{
		DataDir: data, PublicDir: public,
		Prefix: "/app/wayyoung_toolbox/modules/integrity", BrowseRoots: []string{root},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer module.Close(t.Context())

	denied := httptest.NewRecorder()
	deniedRequest := httptest.NewRequest(http.MethodPost, "/app/wayyoung_toolbox/modules/integrity/api/scans", strings.NewReader(`{"path":"`+root+`","mode":"quick"}`))
	module.Handler().ServeHTTP(denied, deniedRequest)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("expected admin check, got %d", denied.Code)
	}

	started := httptest.NewRecorder()
	startRequest := httptest.NewRequest(http.MethodPost, "/app/wayyoung_toolbox/modules/integrity/api/scans", strings.NewReader(`{"path":"`+root+`","mode":"quick"}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startRequest.Header.Set("X-Trim-Isadmin", "true")
	module.Handler().ServeHTTP(started, startRequest)
	if started.Code != http.StatusAccepted {
		t.Fatalf("start status=%d body=%s", started.Code, started.Body.String())
	}

	deadline := time.Now().Add(3 * time.Second)
	var status Status
	for {
		response := httptest.NewRecorder()
		module.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app/wayyoung_toolbox/modules/integrity/api/status", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("status endpoint=%d", response.Code)
		}
		if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if !status.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scan did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status.Total != 3 || status.Checked != 3 || status.Healthy != 1 || status.Damaged != 1 || status.Warning != 1 {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.ReportJSON == "" || status.ReportCSV == "" {
		t.Fatalf("report paths missing: %+v", status)
	}
	for _, path := range []string{status.ReportJSON, status.ReportCSV} {
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("report unavailable at %q: %v", path, err)
		}
	}

	report := httptest.NewRecorder()
	module.Handler().ServeHTTP(report, httptest.NewRequest(http.MethodGet, "/app/wayyoung_toolbox/modules/integrity/api/report?format=csv", nil))
	if report.Code != http.StatusOK || !strings.Contains(report.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("report status=%d type=%q", report.Code, report.Header().Get("Content-Type"))
	}
	jsonReport := httptest.NewRecorder()
	module.Handler().ServeHTTP(jsonReport, httptest.NewRequest(http.MethodGet, "/app/wayyoung_toolbox/modules/integrity/api/report", nil))
	if jsonReport.Code != http.StatusOK || !strings.Contains(jsonReport.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("JSON report status=%d type=%q", jsonReport.Code, jsonReport.Header().Get("Content-Type"))
	}
	filtered := httptest.NewRecorder()
	module.Handler().ServeHTTP(filtered, httptest.NewRequest(http.MethodGet, "/app/wayyoung_toolbox/modules/integrity/api/results?status=damaged&limit=1", nil))
	if filtered.Code != http.StatusOK || !strings.Contains(filtered.Body.String(), "broken.pdf") {
		t.Fatalf("filtered results status=%d body=%s", filtered.Code, filtered.Body.String())
	}
}
