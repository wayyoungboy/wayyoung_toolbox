package service

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestDatabasePreservesReservedCharactersInStoragePath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "媒体#one?two%three")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "tasks.db")
	db, err := OpenDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("SQLite did not open the exact locked data path: %v", err)
	}
}

func TestMaintenanceConfigScheduleAndRunLifecycle(t *testing.T) {
	db, err := OpenDatabase(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	config := defaultMaintenanceConfig()
	config.RootDirectory = root
	config.CleanupEnabled = true
	config.ScheduleType = "interval"
	config.ScheduleMinutes = 60
	config.MinimumAgeHours = 48
	updated, err := db.UpdateMaintenanceConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := canonicalDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	if updated.NextRunAt == nil || updated.RootDirectory != canonicalRoot {
		t.Fatalf("updated config = %+v", updated)
	}
	due, err := db.MaintenanceDue()
	if err != nil || due {
		t.Fatalf("new schedule due = %v, err = %v", due, err)
	}
	if _, err := db.db.Exec(`UPDATE settings SET value=? WHERE key='maintenance_next_run_at'`, encodeJSON(time.Now().Add(-time.Minute).UTC().Format(time.RFC3339))); err != nil {
		t.Fatal(err)
	}
	due, err = db.MaintenanceDue()
	if err != nil || !due {
		t.Fatalf("past schedule due = %v, err = %v", due, err)
	}
	config, err = db.MaintenanceConfig()
	if err != nil || db.MarkMaintenanceStarted(config) != nil {
		t.Fatalf("mark started: config=%+v err=%v", config, err)
	}
	marked, err := db.MaintenanceConfig()
	if err != nil || marked.LastRunAt == nil || marked.NextRunAt == nil {
		t.Fatalf("marked config = %+v, err = %v", marked, err)
	}
	runID, err := db.StartMaintenanceRun("empty_directories", "schedule")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishMaintenanceRun(runID, "success", 12, 3, 2, []string{"a", "b"}, ""); err != nil {
		t.Fatal(err)
	}
	runs, err := db.ListMaintenanceRuns(10)
	if err != nil || len(runs) != 1 || fmt.Sprint(runs[0]["action_count"]) != "2" {
		t.Fatalf("runs = %#v, err = %v", runs, err)
	}
}

func TestIntakeConfigScheduleLifecycle(t *testing.T) {
	db, err := OpenDatabase(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	for _, path := range []string{source, destination} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := defaultIntakeConfig()
	config.Enabled = true
	config.SourceDirectory = source
	config.DestinationDirectory = destination
	config.ScheduleMinutes = 30
	updated, err := db.UpdateIntakeConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if updated.NextRunAt == nil || updated.LastRunAt != nil {
		t.Fatalf("updated=%+v", updated)
	}
	if err := db.MarkIntakeStarted(updated); err != nil {
		t.Fatal(err)
	}
	marked, err := db.IntakeConfig()
	if err != nil || marked.LastRunAt == nil || marked.NextRunAt == nil {
		t.Fatalf("marked=%+v err=%v", marked, err)
	}
}

func TestMaintenanceRunRetentionIsIsolatedAndPreservesRunning(t *testing.T) {
	db, err := OpenDatabase(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	intakeID, err := db.StartMaintenanceRun("intake", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishMaintenanceRun(intakeID, "success", 1, 1, 1, []string{}, ""); err != nil {
		t.Fatal(err)
	}
	runningID, err := db.StartMaintenanceRun("empty_directories", "manual")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 101; index++ {
		id, startErr := db.StartMaintenanceRun("empty_directories", "manual")
		if startErr != nil {
			t.Fatal(startErr)
		}
		if finishErr := db.FinishMaintenanceRun(id, "success", 1, 1, 0, []string{}, ""); finishErr != nil {
			t.Fatal(finishErr)
		}
	}

	intakeRuns, err := db.ListMaintenanceRunsByKinds(20, "intake")
	if err != nil || len(intakeRuns) != 1 {
		t.Fatalf("intake runs=%v err=%v", intakeRuns, err)
	}
	maintenanceRuns, err := db.ListMaintenanceRunsByKinds(200, "empty_directories")
	if err != nil {
		t.Fatal(err)
	}
	if len(maintenanceRuns) != 101 {
		t.Fatalf("maintenance runs=%d, want 100 completed plus one running", len(maintenanceRuns))
	}
	foundRunning := false
	for _, run := range maintenanceRuns {
		if fmt.Sprint(run["id"]) == fmt.Sprint(runningID) && run["status"] == "running" {
			foundRunning = true
		}
	}
	if !foundRunning {
		t.Fatalf("running record %d was removed: %#v", runningID, maintenanceRuns)
	}
	if err := db.FinishMaintenanceRun(-1, "failed", 0, 0, 0, []string{}, "missing"); err == nil {
		t.Fatal("finishing a missing run should fail")
	}
}

func TestOpeningLegacyDatabaseAppliesDefaultRunRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`CREATE TABLE task_runs (
id INTEGER PRIMARY KEY AUTOINCREMENT, task_id INTEGER NOT NULL, trigger_type TEXT NOT NULL,
status TEXT NOT NULL, dry_run INTEGER NOT NULL DEFAULT 0, started_at TEXT NOT NULL,
finished_at TEXT, entries_count INTEGER NOT NULL DEFAULT 0, matched_count INTEGER NOT NULL DEFAULT 0,
submitted_count INTEGER NOT NULL DEFAULT 0, skipped_count INTEGER NOT NULL DEFAULT 0,
failed_count INTEGER NOT NULL DEFAULT 0, details TEXT NOT NULL DEFAULT '[]', error TEXT NOT NULL DEFAULT '');
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL);`); err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`INSERT INTO settings(key,value,updated_at) VALUES('run_retention_count','1000','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 105; index++ {
		if _, err = raw.Exec(`INSERT INTO task_runs(task_id,trigger_type,status,started_at) VALUES(1,'schedule','failed',?)`, fmt.Sprintf("2026-01-01T00:%02d:00Z", index)); err != nil {
			t.Fatal(err)
		}
	}
	if err = raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := OpenDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err = db.db.QueryRow("SELECT COUNT(*) FROM task_runs").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 100 {
		t.Fatalf("legacy database retained %d runs, want 100", count)
	}
	settings, err := db.Settings(false)
	if err != nil {
		t.Fatal(err)
	}
	if settings.RunRetentionCount != 100 {
		t.Fatalf("legacy retention setting = %d, want repaired value 100", settings.RunRetentionCount)
	}
}

func TestDatabaseTaskLifecycle(t *testing.T) {
	db, err := OpenDatabase(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task, err := validateTask(Task{Name: "示例", FeedURL: "https://example.com/rss", Enabled: true, ScheduleType: "interval", ScheduleMinutes: 60, ScheduleTime: "03:00", Extractor: "direct", IncludeAll: []string{"HD"}, IncludeAny: []string{}, ExcludeAny: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	created, err := db.CreateTask(task)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID < 1 || created.NextRunAt == nil {
		t.Fatalf("unexpected task: %+v", created)
	}
	items, err := db.Tasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "示例" {
		t.Fatalf("unexpected items: %+v", items)
	}
	deleted, err := db.DeleteTask(created.ID)
	if err != nil || !deleted {
		t.Fatalf("delete: %v %v", deleted, err)
	}
}

func TestSeenItemsRetentionIsBounded(t *testing.T) {
	db, err := OpenDatabase(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task, err := validateTask(Task{
		Name: "test", FeedURL: "https://example.com/rss", Enabled: true,
		ScheduleType: "interval", ScheduleMinutes: 60, ScheduleTime: "03:00", Extractor: "direct",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := db.CreateTask(task)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	statement, err := tx.Prepare(`INSERT INTO seen_items(task_id,fingerprint,title,status,created_at) VALUES(?,?,?,'submitted',?)`)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 10005; index++ {
		if _, err = statement.Exec(created.ID, fmt.Sprintf("fp-%d", index), "item", nowUTC()); err != nil {
			t.Fatal(err)
		}
	}
	if err = statement.Close(); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = db.MarkSeen(created.ID, FeedEntry{Title: "last"}, "magnet:?xt=urn:btih:last", "submitted"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.db.QueryRow("SELECT COUNT(*) FROM seen_items WHERE task_id=?", created.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 10000 {
		t.Fatalf("retained %d items", count)
	}
}

func TestDatabaseDoesNotReturnPassword(t *testing.T) {
	db, err := OpenDatabase(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.UpdateSettings(map[string]any{"qb_password": "secret"}); err != nil {
		t.Fatal(err)
	}
	public, err := db.Settings(false)
	if err != nil {
		t.Fatal(err)
	}
	if public.QBPassword != "" || !public.QBPasswordSet {
		t.Fatalf("password leaked or marker missing: %+v", public)
	}
	private, err := db.Settings(true)
	if err != nil {
		t.Fatal(err)
	}
	if private.QBPassword != "secret" {
		t.Fatal("private password was not retained")
	}
	if !private.QBVerifyCertificate {
		t.Fatal("TLS certificate verification must be enabled by default")
	}
}

func TestSettingsDefaultToDirectFeedsAndOneHundredRuns(t *testing.T) {
	settings := defaultSettings()
	if settings.FeedUseProxy {
		t.Fatal("RSS feeds must be direct by default")
	}
	if settings.RunRetentionCount != 100 {
		t.Fatalf("run retention = %d, want 100", settings.RunRetentionCount)
	}
}

func TestJavspAdvancedConfigRoundTripAndSecretMasking(t *testing.T) {
	db, err := OpenDatabase(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	config := defaultJavspConfig()
	config.InputDirectory = t.TempDir()
	config.NormalCrawlers = []string{"javdb", "javbus"}
	config.ProxyFree = map[string]string{"javdb": "https://javdb.example"}
	config.PathHardLink = true
	config.TranslatorEngine = "openai"
	config.TranslatorURL = "https://translator.example/v1"
	config.TranslatorModel = "example"
	config.TranslatorAPIKey = "secret-key"
	if _, err := db.UpdateJavspConfig(config); err != nil {
		t.Fatal(err)
	}
	private, err := db.JavspConfigPrivate()
	if err != nil {
		t.Fatal(err)
	}
	if private.TranslatorAPIKey != "secret-key" || !reflect.DeepEqual(private.NormalCrawlers, config.NormalCrawlers) || !reflect.DeepEqual(private.ProxyFree, config.ProxyFree) {
		t.Fatalf("advanced config not retained: %#v", private)
	}
	public, err := db.JavspConfig()
	if err != nil {
		t.Fatal(err)
	}
	if public.TranslatorAPIKey != "" || !public.TranslatorAPIKeySet {
		t.Fatalf("translator secret leaked or marker missing: %#v", public)
	}
}

func TestOpeningLegacyDatabaseMigratesGenericNFOBasename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	db, err := OpenDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`INSERT INTO settings(key,value,updated_at) VALUES('javsp_nfo_basename_pattern',?,?)`, encodeJSON("movie"), nowUTC()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = OpenDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	config, err := db.JavspConfigPrivate()
	if err != nil {
		t.Fatal(err)
	}
	if config.NFOBasenamePattern != "{num}" {
		t.Fatalf("migrated NFO basename = %q, want {num}", config.NFOBasenamePattern)
	}
}

func TestLegacyJavspYAMLMigrationUsesSameNameNFOAndPreservesCustomValue(t *testing.T) {
	t.Run("legacy movie default", func(t *testing.T) {
		db, err := OpenDatabase(filepath.Join(t.TempDir(), "tasks.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		legacy := defaultJavspConfig()
		legacy.NFOBasenamePattern = "movie"
		if err := db.MigrateJavspAdvancedConfig(legacy); err != nil {
			t.Fatal(err)
		}
		config, err := db.JavspConfigPrivate()
		if err != nil {
			t.Fatal(err)
		}
		if config.NFOBasenamePattern != "{num}" {
			t.Fatalf("migrated NFO basename = %q, want {num}", config.NFOBasenamePattern)
		}
	})

	t.Run("custom value", func(t *testing.T) {
		db, err := OpenDatabase(filepath.Join(t.TempDir(), "tasks.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		custom := defaultJavspConfig()
		custom.NFOBasenamePattern = "custom-{num}"
		if err := db.MigrateJavspAdvancedConfig(custom); err != nil {
			t.Fatal(err)
		}
		config, err := db.JavspConfigPrivate()
		if err != nil {
			t.Fatal(err)
		}
		if config.NFOBasenamePattern != custom.NFOBasenamePattern {
			t.Fatalf("custom NFO basename = %q, want %q", config.NFOBasenamePattern, custom.NFOBasenamePattern)
		}
	})
}

func TestTaskRunRetentionIsAppliedImmediatelyAndAfterCompletion(t *testing.T) {
	db, err := OpenDatabase(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task, err := validateTask(Task{
		Name: "test", FeedURL: "https://example.com/rss", Enabled: true,
		ScheduleType: "interval", ScheduleMinutes: 60, ScheduleTime: "03:00", Extractor: "direct",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := db.CreateTask(task)
	if err != nil {
		t.Fatal(err)
	}
	finishRun := func() {
		t.Helper()
		id, startErr := db.StartRun(created.ID, "test", true)
		if startErr != nil {
			t.Fatal(startErr)
		}
		if finishErr := db.FinishRun(id, RunSummary{}, "success", ""); finishErr != nil {
			t.Fatal(finishErr)
		}
	}
	for range 5 {
		finishRun()
	}
	runningID, err := db.StartRun(created.ID, "test", true)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := db.UpdateSettings(map[string]any{"run_retention_count": 3})
	if err != nil {
		t.Fatal(err)
	}
	if updated.RunRetentionCount != 3 {
		t.Fatalf("saved retention = %d, want 3", updated.RunRetentionCount)
	}
	runs, err := db.ListRuns(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 4 {
		t.Fatalf("runs after settings update = %d, want 3 completed plus 1 running", len(runs))
	}
	var runningCount int
	for _, run := range runs {
		if run["status"] == "running" && run["id"] == runningID {
			runningCount++
		}
	}
	if runningCount != 1 {
		t.Fatalf("active run was removed during retention pruning: %#v", runs)
	}
	finishRun()
	runs, err = db.ListRuns(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 4 {
		t.Fatalf("runs after completion = %d, want 3 completed plus 1 running", len(runs))
	}
}
