package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewServiceMigratesLegacyJavspStateWithoutStartingScan(t *testing.T) {
	bundle, data := javspTestRuntime(t)
	templatePath := filepath.Join(bundle, "runtime", "config.yml")
	template, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	template = []byte(strings.Replace(string(template), "  retry: 3", "  retry: 7", 1))
	if err := os.WriteFile(templatePath, template, 0o600); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(data, "legacy-input")
	if err := os.MkdirAll(input, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(input, "TEST-001.mp4")
	if err := os.WriteFile(marker, []byte("legacy media marker"), 0o600); err != nil {
		t.Fatal(err)
	}

	db, err := OpenDatabase(filepath.Join(data, "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	task, err := validateTask(Task{
		Name: "legacy RSS", FeedURL: "https://example.com/feed.xml", Enabled: false,
		ScheduleType: "interval", ScheduleMinutes: 60, ScheduleTime: "03:00", Extractor: "direct",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	runID, err := db.StartJavspRun("manual")
	if err != nil {
		t.Fatal(err)
	}
	exitCode := 0
	if err := db.FinishJavspRun(runID, "success", &exitCode, "legacy output", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec("UPDATE settings SET value=? WHERE key='javsp_input_directory'", encodeJSON(input)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	public := filepath.Join(data, "public")
	if err := os.MkdirAll(public, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(public, "index.html"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(data, public, bundle, "/app/media_acquisition_center", false)
	if err != nil {
		t.Fatal(err)
	}
	defer service.db.Close()

	config, err := service.db.JavspConfigPrivate()
	if err != nil {
		t.Fatal(err)
	}
	if config.InputDirectory != input || config.NetworkRetry != 7 {
		t.Fatalf("migrated config input=%q retry=%d", config.InputDirectory, config.NetworkRetry)
	}
	tasks, err := service.db.Tasks()
	if err != nil || len(tasks) != 1 || tasks[0].Name != "legacy RSS" {
		t.Fatalf("tasks=%#v err=%v", tasks, err)
	}
	runs, err := service.db.ListJavspRuns(10)
	if err != nil || len(runs) != 1 || runs[0]["output"] != "legacy output" {
		t.Fatalf("runs=%#v err=%v", runs, err)
	}
	if progress := service.javsp.LastProgress(); progress.Phase != "" {
		t.Fatalf("constructor started JAVSP: %#v", progress)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "legacy media marker" {
		t.Fatalf("media marker changed: %q err=%v", got, err)
	}
	if matches, err := filepath.Glob(filepath.Join(input, "*.nfo")); err != nil || len(matches) != 0 {
		t.Fatalf("constructor produced metadata: %v err=%v", matches, err)
	}
}
