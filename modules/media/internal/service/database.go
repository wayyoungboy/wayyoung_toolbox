package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
	_ "modernc.org/sqlite"
)

type Database struct {
	db        *sql.DB
	catalog   *catalog.Store
	lockFile  *os.File
	closeOnce sync.Once
	closeErr  error
}

func OpenDatabase(path string) (*Database, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	lockFile, err := lockDatabaseFile(abs + ".lock")
	if err != nil {
		return nil, err
	}
	dataURL := url.URL{Scheme: "file", Path: abs}
	db, err := sql.Open("sqlite", dataURL.String()+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)")
	if err != nil {
		lockFile.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	database := &Database{db: db, lockFile: lockFile}
	if err := database.initialize(); err != nil {
		database.Close()
		return nil, err
	}
	if err := database.initializeMediaQueue(); err != nil {
		database.Close()
		return nil, err
	}
	database.catalog, err = catalog.New(db, unambiguousMediaID)
	if err != nil {
		database.Close()
		return nil, err
	}
	return database, nil
}

func (d *Database) Close() error {
	d.closeOnce.Do(func() {
		d.closeErr = d.db.Close()
		if d.lockFile != nil {
			d.closeErr = errors.Join(d.closeErr, d.lockFile.Close())
		}
	})
	return d.closeErr
}

func (d *Database) initialize() error {
	const schema = `
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS rss_tasks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL, feed_url TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
  schedule_type TEXT NOT NULL DEFAULT 'interval', schedule_minutes INTEGER NOT NULL DEFAULT 60,
  schedule_time TEXT NOT NULL DEFAULT '03:00', include_all TEXT NOT NULL DEFAULT '[]',
  include_any TEXT NOT NULL DEFAULT '[]', exclude_any TEXT NOT NULL DEFAULT '[]',
  extractor TEXT NOT NULL DEFAULT 'rmdown', proxy_url TEXT NOT NULL DEFAULT '',
  qb_category TEXT NOT NULL DEFAULT '', save_path TEXT NOT NULL DEFAULT '',
  last_run_at TEXT, next_run_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS task_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT, task_id INTEGER NOT NULL,
  trigger_type TEXT NOT NULL, status TEXT NOT NULL, dry_run INTEGER NOT NULL DEFAULT 0,
  started_at TEXT NOT NULL, finished_at TEXT, entries_count INTEGER NOT NULL DEFAULT 0,
  matched_count INTEGER NOT NULL DEFAULT 0, submitted_count INTEGER NOT NULL DEFAULT 0,
  skipped_count INTEGER NOT NULL DEFAULT 0, failed_count INTEGER NOT NULL DEFAULT 0,
  details TEXT NOT NULL DEFAULT '[]', error TEXT NOT NULL DEFAULT '',
  FOREIGN KEY(task_id) REFERENCES rss_tasks(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS seen_items (
  task_id INTEGER NOT NULL, fingerprint TEXT NOT NULL, title TEXT NOT NULL,
  source_url TEXT NOT NULL DEFAULT '', magnet TEXT NOT NULL DEFAULT '', status TEXT NOT NULL,
  created_at TEXT NOT NULL, PRIMARY KEY(task_id, fingerprint),
  FOREIGN KEY(task_id) REFERENCES rss_tasks(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS javsp_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT, trigger_type TEXT NOT NULL, status TEXT NOT NULL,
  started_at TEXT NOT NULL, finished_at TEXT, exit_code INTEGER,
  output TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS maintenance_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, trigger_type TEXT NOT NULL,
  status TEXT NOT NULL, started_at TEXT NOT NULL, finished_at TEXT,
  scanned_count INTEGER NOT NULL DEFAULT 0, found_count INTEGER NOT NULL DEFAULT 0,
  action_count INTEGER NOT NULL DEFAULT 0, details TEXT NOT NULL DEFAULT '[]',
  error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_runs_started_at ON task_runs(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_tasks_next_run ON rss_tasks(enabled, next_run_at);
CREATE INDEX IF NOT EXISTS idx_javsp_runs_started_at ON javsp_runs(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_maintenance_runs_started_at ON maintenance_runs(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_seen_items_created_at ON seen_items(task_id, created_at DESC);`
	if _, err := d.db.Exec(schema); err != nil {
		return err
	}
	columns, err := d.tableColumns("rss_tasks")
	if err != nil {
		return err
	}
	if !columns["schedule_type"] {
		if _, err := d.db.Exec("ALTER TABLE rss_tasks ADD COLUMN schedule_type TEXT NOT NULL DEFAULT 'interval'"); err != nil {
			return err
		}
	}
	if !columns["schedule_time"] {
		if _, err := d.db.Exec("ALTER TABLE rss_tasks ADD COLUMN schedule_time TEXT NOT NULL DEFAULT '03:00'"); err != nil {
			return err
		}
	}
	settings := defaultSettings()
	javsp := defaultJavspConfig()
	anime := defaultAnimeConfig()
	maintenance := defaultMaintenanceConfig()
	intake := defaultIntakeConfig()
	defaults := map[string]any{
		"preview_mode": settings.PreviewMode, "qb_host": settings.QBHost,
		"qb_port": settings.QBPort, "qb_username": settings.QBUsername,
		"qb_password": "", "qb_verify_certificate": settings.QBVerifyCertificate,
		"feed_use_proxy": settings.FeedUseProxy, "global_proxy_url": settings.GlobalProxyURL,
		"request_timeout": settings.RequestTimeout, "run_retention_count": settings.RunRetentionCount,
		"javsp_enabled": javsp.Enabled, "javsp_schedule_type": javsp.ScheduleType,
		"javsp_schedule_minutes": javsp.ScheduleMinutes, "javsp_schedule_time": javsp.ScheduleTime,
		"javsp_input_directory":       javsp.InputDirectory,
		"javsp_output_directory":      javsp.OutputDirectory,
		"javsp_output_folder_pattern": javsp.OutputFolderPattern,
		"javsp_proxy_server":          javsp.ProxyServer, "javsp_minimum_size": javsp.MinimumSize,
		"javsp_move_files": javsp.MoveFiles, "javsp_extra_fanarts": javsp.ExtraFanarts,
		"javsp_translator_engine": javsp.TranslatorEngine,
		"javsp_timeout_seconds":   javsp.TimeoutSeconds,
		"javsp_last_run_at":       nil, "javsp_next_run_at": nil,
		"anime_directory":                       anime.Directory,
		"maintenance_root_directory":            maintenance.RootDirectory,
		"maintenance_cleanup_enabled":           maintenance.CleanupEnabled,
		"maintenance_duplicate_cleanup_enabled": maintenance.DuplicateCleanupEnabled,
		"maintenance_orphan_cleanup_enabled":    maintenance.OrphanCleanupEnabled,
		"maintenance_schedule_type":             maintenance.ScheduleType,
		"maintenance_schedule_minutes":          maintenance.ScheduleMinutes,
		"maintenance_schedule_time":             maintenance.ScheduleTime,
		"maintenance_minimum_age_hours":         maintenance.MinimumAgeHours,
		"maintenance_last_run_at":               nil,
		"maintenance_next_run_at":               nil,
		"intake_enabled":                        intake.Enabled,
		"intake_source_directory":               intake.SourceDirectory,
		"intake_destination_directory":          intake.DestinationDirectory,
		"intake_schedule_minutes":               intake.ScheduleMinutes,
		"intake_run_javsp":                      intake.RunJavsp,
		"intake_last_run_at":                    nil,
		"intake_next_run_at":                    nil,
	}
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range defaults {
		if _, err := tx.Exec("INSERT OR IGNORE INTO settings(key,value,updated_at) VALUES(?,?,?)", key, encodeJSON(value), nowUTC()); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE settings SET value=?,updated_at=?
WHERE key='javsp_nfo_basename_pattern' AND value=?`, encodeJSON("{num}"), nowUTC(), encodeJSON("movie")); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE javsp_runs SET status='failed', finished_at=?, error='应用服务重启，运行状态已中断' WHERE status='running'", nowUTC()); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE maintenance_runs SET status='failed', finished_at=?, error='应用服务重启，运行状态已中断' WHERE status='running'", nowUTC()); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE task_runs SET status='failed', finished_at=?, error='应用服务重启，运行状态已中断' WHERE status='running'", nowUTC()); err != nil {
		return err
	}
	var retentionRaw []byte
	if err := tx.QueryRow("SELECT value FROM settings WHERE key='run_retention_count'").Scan(&retentionRaw); err != nil {
		return err
	}
	retention := settings.RunRetentionCount
	if json.Unmarshal(retentionRaw, &retention) != nil || retention < 1 || retention > 100 {
		retention = settings.RunRetentionCount
		if _, err := tx.Exec("UPDATE settings SET value=?,updated_at=? WHERE key='run_retention_count'", encodeJSON(retention), nowUTC()); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM task_runs
WHERE status!='running' AND id NOT IN (
  SELECT id FROM task_runs WHERE status!='running' ORDER BY id DESC LIMIT ?
)`, retention); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *Database) AnimeConfig() (AnimeConfig, error) {
	values, err := d.allSettings()
	if err != nil {
		return AnimeConfig{}, err
	}
	defaults := defaultAnimeConfig()
	return AnimeConfig{Directory: decodeSetting(values, "anime_directory", defaults.Directory)}, nil
}

func (d *Database) UpdateAnimeConfig(config AnimeConfig) (AnimeConfig, error) {
	directory := strings.TrimSpace(config.Directory)
	if _, err := canonicalDirectory(directory); err != nil {
		return AnimeConfig{}, fmt.Errorf("番剧扫描目录不可访问")
	}
	if _, err := d.db.Exec(`INSERT INTO settings(key,value,updated_at) VALUES('anime_directory',?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, encodeJSON(directory), nowUTC()); err != nil {
		return AnimeConfig{}, err
	}
	return AnimeConfig{Directory: directory}, nil
}

func (d *Database) MaintenanceConfig() (MaintenanceConfig, error) {
	values, err := d.allSettings()
	if err != nil {
		return MaintenanceConfig{}, err
	}
	defaults := defaultMaintenanceConfig()
	return MaintenanceConfig{
		RootDirectory:           decodeSetting(values, "maintenance_root_directory", defaults.RootDirectory),
		CleanupEnabled:          decodeSetting(values, "maintenance_cleanup_enabled", defaults.CleanupEnabled),
		DuplicateCleanupEnabled: decodeSetting(values, "maintenance_duplicate_cleanup_enabled", defaults.DuplicateCleanupEnabled),
		OrphanCleanupEnabled:    decodeSetting(values, "maintenance_orphan_cleanup_enabled", defaults.OrphanCleanupEnabled),
		ScheduleType:            decodeSetting(values, "maintenance_schedule_type", defaults.ScheduleType),
		ScheduleMinutes:         decodeSetting(values, "maintenance_schedule_minutes", defaults.ScheduleMinutes),
		ScheduleTime:            decodeSetting(values, "maintenance_schedule_time", defaults.ScheduleTime),
		MinimumAgeHours:         decodeSetting(values, "maintenance_minimum_age_hours", defaults.MinimumAgeHours),
		LastRunAt:               decodeSetting(values, "maintenance_last_run_at", defaults.LastRunAt),
		NextRunAt:               decodeSetting(values, "maintenance_next_run_at", defaults.NextRunAt),
	}, nil
}

func (d *Database) IntakeConfig() (IntakeConfig, error) {
	values, err := d.allSettings()
	if err != nil {
		return IntakeConfig{}, err
	}
	return intakeConfigFromSettings(values), nil
}

func intakeConfigFromSettings(values map[string]json.RawMessage) IntakeConfig {
	defaults := defaultIntakeConfig()
	return IntakeConfig{
		Revision:             decodeSetting(values, "intake_revision", int64(0)),
		Enabled:              decodeSetting(values, "intake_enabled", defaults.Enabled),
		SourceDirectory:      decodeSetting(values, "intake_source_directory", defaults.SourceDirectory),
		DestinationDirectory: decodeSetting(values, "intake_destination_directory", defaults.DestinationDirectory),
		ScheduleMinutes:      decodeSetting(values, "intake_schedule_minutes", defaults.ScheduleMinutes),
		RunJavsp:             decodeSetting(values, "intake_run_javsp", defaults.RunJavsp),
		LastRunAt:            decodeSetting(values, "intake_last_run_at", defaults.LastRunAt),
		NextRunAt:            decodeSetting(values, "intake_next_run_at", defaults.NextRunAt),
	}
}

var errIntakeConfigConflict = errors.New("下载入库配置已更新，请重新读取后再保存")

func (d *Database) UpdateIntakeConfig(config IntakeConfig) (IntakeConfig, error) {
	return d.updateIntakeConfig(context.Background(), config, nil)
}

func (d *Database) UpdateIntakeConfigIfRevision(ctx context.Context, config IntakeConfig) (IntakeConfig, error) {
	return d.updateIntakeConfig(ctx, config, &config.Revision)
}

func (d *Database) updateIntakeConfig(ctx context.Context, config IntakeConfig, revision *int64) (IntakeConfig, error) {
	validated, err := validateIntakeConfig(config)
	if err != nil {
		return IntakeConfig{}, err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return IntakeConfig{}, err
	}
	defer tx.Rollback()
	stored, err := readSettings(ctx, tx)
	if err != nil {
		return IntakeConfig{}, err
	}
	current := intakeConfigFromSettings(stored)
	if revision != nil && *revision != current.Revision {
		return IntakeConfig{}, errIntakeConfigConflict
	}
	validated.LastRunAt = current.LastRunAt
	validated.NextRunAt = current.NextRunAt
	if !validated.Enabled {
		validated.NextRunAt = nil
	} else if !current.Enabled || current.NextRunAt == nil || validated.ScheduleMinutes != current.ScheduleMinutes {
		next := time.Now().UTC().Add(time.Duration(validated.ScheduleMinutes) * time.Minute).Format(time.RFC3339)
		validated.NextRunAt = &next
	}
	values := map[string]any{
		"intake_revision":              current.Revision + 1,
		"intake_enabled":               validated.Enabled,
		"intake_source_directory":      validated.SourceDirectory,
		"intake_destination_directory": validated.DestinationDirectory,
		"intake_schedule_minutes":      validated.ScheduleMinutes,
		"intake_run_javsp":             validated.RunJavsp,
		"intake_next_run_at":           validated.NextRunAt,
	}
	for key, value := range values {
		stored[key] = json.RawMessage(encodeJSON(value))
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, encodeJSON(value), nowUTC()); err != nil {
			return IntakeConfig{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return IntakeConfig{}, err
	}
	return intakeConfigFromSettings(stored), nil
}

func (d *Database) IntakeDue() (bool, error) {
	config, err := d.IntakeConfig()
	return err == nil && config.Enabled && config.NextRunAt != nil && *config.NextRunAt <= nowUTC(), err
}

func (d *Database) MarkIntakeStarted(_ IntakeConfig) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stored, err := readSettings(context.Background(), tx)
	if err != nil {
		return err
	}
	config := intakeConfigFromSettings(stored)
	started := nowUTC()
	var next *string
	if config.Enabled {
		value := time.Now().UTC().Add(time.Duration(config.ScheduleMinutes) * time.Minute).Format(time.RFC3339)
		next = &value
	}
	for key, value := range map[string]any{"intake_last_run_at": started, "intake_next_run_at": next} {
		if _, err := tx.Exec(`INSERT INTO settings(key,value,updated_at) VALUES(?,?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, encodeJSON(value), started); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *Database) UpdateMaintenanceConfig(config MaintenanceConfig) (MaintenanceConfig, error) {
	validated, err := validateMaintenanceConfig(config)
	if err != nil {
		return MaintenanceConfig{}, err
	}
	current, err := d.MaintenanceConfig()
	if err != nil {
		return MaintenanceConfig{}, err
	}
	validated.LastRunAt = current.LastRunAt
	if validated.CleanupEnabled || validated.DuplicateCleanupEnabled || validated.OrphanCleanupEnabled {
		next, nextErr := calculateNextRun(validated.ScheduleType, validated.ScheduleMinutes, validated.ScheduleTime, time.Now())
		if nextErr != nil {
			return MaintenanceConfig{}, nextErr
		}
		validated.NextRunAt = &next
	} else {
		validated.NextRunAt = nil
	}
	values := map[string]any{
		"maintenance_root_directory":            validated.RootDirectory,
		"maintenance_cleanup_enabled":           validated.CleanupEnabled,
		"maintenance_duplicate_cleanup_enabled": validated.DuplicateCleanupEnabled,
		"maintenance_orphan_cleanup_enabled":    validated.OrphanCleanupEnabled,
		"maintenance_schedule_type":             validated.ScheduleType,
		"maintenance_schedule_minutes":          validated.ScheduleMinutes,
		"maintenance_schedule_time":             validated.ScheduleTime,
		"maintenance_minimum_age_hours":         validated.MinimumAgeHours,
		"maintenance_next_run_at":               validated.NextRunAt,
	}
	tx, err := d.db.Begin()
	if err != nil {
		return MaintenanceConfig{}, err
	}
	defer tx.Rollback()
	for key, value := range values {
		if _, err := tx.Exec(`INSERT INTO settings(key,value,updated_at) VALUES(?,?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, encodeJSON(value), nowUTC()); err != nil {
			return MaintenanceConfig{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return MaintenanceConfig{}, err
	}
	return d.MaintenanceConfig()
}

func (d *Database) MaintenanceDue() (bool, error) {
	config, err := d.MaintenanceConfig()
	return err == nil && (config.CleanupEnabled || config.DuplicateCleanupEnabled || config.OrphanCleanupEnabled) && config.NextRunAt != nil && *config.NextRunAt <= nowUTC(), err
}

func (d *Database) MarkMaintenanceStarted(config MaintenanceConfig) error {
	started := nowUTC()
	var next *string
	if config.CleanupEnabled || config.DuplicateCleanupEnabled || config.OrphanCleanupEnabled {
		value, err := calculateNextRun(config.ScheduleType, config.ScheduleMinutes, config.ScheduleTime, time.Now())
		if err != nil {
			return err
		}
		next = &value
	}
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range map[string]any{"maintenance_last_run_at": started, "maintenance_next_run_at": next} {
		if _, err := tx.Exec(`INSERT INTO settings(key,value,updated_at) VALUES(?,?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, encodeJSON(value), started); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *Database) StartMaintenanceRun(kind, trigger string) (int64, error) {
	result, err := d.db.Exec("INSERT INTO maintenance_runs(kind,trigger_type,status,started_at) VALUES(?,?,'running',?)", kind, trigger, nowUTC())
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (d *Database) FinishMaintenanceRun(id int64, status string, scanned, found, action int, details any, message string) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var kind string
	if err := tx.QueryRow("SELECT kind FROM maintenance_runs WHERE id=?", id).Scan(&kind); err != nil {
		return fmt.Errorf("运行记录不存在：%w", err)
	}
	result, err := tx.Exec(`UPDATE maintenance_runs SET status=?,finished_at=?,scanned_count=?,found_count=?,action_count=?,details=?,error=? WHERE id=?`,
		status, nowUTC(), scanned, found, action, encodeJSON(details), message, id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return fmt.Errorf("运行记录 %d 未完成更新", id)
	}
	if _, err := tx.Exec(`DELETE FROM maintenance_runs
WHERE kind=? AND status<>'running' AND id NOT IN (
	SELECT id FROM maintenance_runs WHERE kind=? AND status<>'running' ORDER BY id DESC LIMIT 100
)`, kind, kind); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *Database) ListMaintenanceRuns(limit int) ([]map[string]any, error) {
	return d.listMaintenanceRuns(limit, nil)
}

func (d *Database) ListMaintenanceRunsByKinds(limit int, kinds ...string) ([]map[string]any, error) {
	if len(kinds) == 0 {
		return []map[string]any{}, nil
	}
	return d.listMaintenanceRuns(limit, kinds)
}

func (d *Database) listMaintenanceRuns(limit int, kinds []string) ([]map[string]any, error) {
	limit = clamp(limit, 1, 1000)
	query := `SELECT id,kind,trigger_type,status,started_at,finished_at,scanned_count,found_count,action_count,details,error FROM maintenance_runs`
	args := make([]any, 0, len(kinds)+1)
	if len(kinds) > 0 {
		placeholders := make([]string, len(kinds))
		for index, kind := range kinds {
			placeholders[index] = "?"
			args = append(args, kind)
		}
		query += " WHERE kind IN (" + strings.Join(placeholders, ",") + ")"
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var kind, trigger, status, started, details, runError string
		var finished sql.NullString
		var scanned, found, action int
		if err := rows.Scan(&id, &kind, &trigger, &status, &started, &finished, &scanned, &found, &action, &details, &runError); err != nil {
			return nil, err
		}
		var parsed any = []any{}
		_ = json.Unmarshal([]byte(details), &parsed)
		item := map[string]any{
			"id": id, "kind": kind, "trigger_type": trigger, "status": status, "started_at": started,
			"finished_at": nil, "scanned_count": scanned, "found_count": found, "action_count": action,
			"details": parsed, "error": runError,
		}
		if finished.Valid {
			item["finished_at"] = finished.String
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (d *Database) tableColumns(table string) (map[string]bool, error) {
	rows, err := d.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		result[name] = true
	}
	return result, rows.Err()
}

func (d *Database) allSettings() (map[string]json.RawMessage, error) {
	return readSettings(context.Background(), d.db)
}

func readSettings(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) (map[string]json.RawMessage, error) {
	rows, err := query.QueryContext(ctx, "SELECT key,value FROM settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]json.RawMessage{}
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result, rows.Err()
}

func decodeSetting[T any](values map[string]json.RawMessage, key string, fallback T) T {
	raw, ok := values[key]
	if !ok || json.Unmarshal(raw, &fallback) != nil {
		return fallback
	}
	return fallback
}

func decodeStringMapSetting(values map[string]json.RawMessage, key string, fallback map[string]string) map[string]string {
	result := make(map[string]string, len(fallback))
	for itemKey, itemValue := range fallback {
		result[itemKey] = itemValue
	}
	raw, ok := values[key]
	if !ok {
		return result
	}
	decoded := map[string]string{}
	if json.Unmarshal(raw, &decoded) != nil {
		return result
	}
	return decoded
}

func (d *Database) Settings(includePassword bool) (Settings, error) {
	values, err := d.allSettings()
	if err != nil {
		return Settings{}, err
	}
	defaults := defaultSettings()
	settings := Settings{
		PreviewMode:         decodeSetting(values, "preview_mode", defaults.PreviewMode),
		QBHost:              decodeSetting(values, "qb_host", defaults.QBHost),
		QBPort:              decodeSetting(values, "qb_port", defaults.QBPort),
		QBUsername:          decodeSetting(values, "qb_username", defaults.QBUsername),
		QBPassword:          decodeSetting(values, "qb_password", ""),
		QBVerifyCertificate: decodeSetting(values, "qb_verify_certificate", defaults.QBVerifyCertificate),
		FeedUseProxy:        decodeSetting(values, "feed_use_proxy", defaults.FeedUseProxy),
		GlobalProxyURL:      decodeSetting(values, "global_proxy_url", defaults.GlobalProxyURL),
		RequestTimeout:      decodeSetting(values, "request_timeout", defaults.RequestTimeout),
		RunRetentionCount:   decodeSetting(values, "run_retention_count", defaults.RunRetentionCount),
	}
	if !includePassword {
		settings.QBPasswordSet = settings.QBPassword != ""
		settings.QBPassword = ""
	}
	return settings, nil
}

func (d *Database) UpdateSettings(values map[string]any) (Settings, error) {
	allowed := map[string]bool{
		"preview_mode": true, "qb_host": true, "qb_port": true, "qb_username": true,
		"qb_password": true, "qb_verify_certificate": true, "feed_use_proxy": true,
		"global_proxy_url": true, "request_timeout": true, "run_retention_count": true,
	}
	tx, err := d.db.Begin()
	if err != nil {
		return Settings{}, err
	}
	defer tx.Rollback()
	for key, value := range values {
		if !allowed[key] {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO settings(key,value,updated_at) VALUES(?,?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, encodeJSON(value), nowUTC()); err != nil {
			return Settings{}, err
		}
	}
	if value, ok := values["run_retention_count"]; ok {
		retention, valid := value.(int)
		if !valid || retention < 1 || retention > 100 {
			return Settings{}, fmt.Errorf("run_retention_count 超出有效范围")
		}
		if _, err := tx.Exec(`DELETE FROM task_runs
WHERE status!='running' AND id NOT IN (
  SELECT id FROM task_runs WHERE status!='running' ORDER BY id DESC LIMIT ?
)`, retention); err != nil {
			return Settings{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Settings{}, err
	}
	return d.Settings(false)
}

func (d *Database) JavspConfig() (JavspConfig, error) {
	return d.javspConfig(false)
}

func (d *Database) JavspConfigPrivate() (JavspConfig, error) {
	return d.javspConfig(true)
}

func (d *Database) javspConfig(includeSecret bool) (JavspConfig, error) {
	values, err := d.allSettings()
	if err != nil {
		return JavspConfig{}, err
	}
	return javspConfigFromSettings(values, includeSecret), nil
}

func javspConfigFromSettings(values map[string]json.RawMessage, includeSecret bool) JavspConfig {
	defaults := defaultJavspConfig()
	config := JavspConfig{
		Revision:                    decodeSetting(values, "javsp_revision", int64(0)),
		Enabled:                     decodeSetting(values, "javsp_enabled", defaults.Enabled),
		ScheduleType:                decodeSetting(values, "javsp_schedule_type", defaults.ScheduleType),
		ScheduleMinutes:             decodeSetting(values, "javsp_schedule_minutes", defaults.ScheduleMinutes),
		ScheduleTime:                decodeSetting(values, "javsp_schedule_time", defaults.ScheduleTime),
		InputDirectory:              decodeSetting(values, "javsp_input_directory", defaults.InputDirectory),
		OutputDirectory:             decodeSetting(values, "javsp_output_directory", defaults.OutputDirectory),
		OutputFolderPattern:         decodeSetting(values, "javsp_output_folder_pattern", defaults.OutputFolderPattern),
		ProxyServer:                 decodeSetting(values, "javsp_proxy_server", defaults.ProxyServer),
		MinimumSize:                 decodeSetting(values, "javsp_minimum_size", defaults.MinimumSize),
		MoveFiles:                   decodeSetting(values, "javsp_move_files", defaults.MoveFiles),
		ExtraFanarts:                decodeSetting(values, "javsp_extra_fanarts", defaults.ExtraFanarts),
		TranslatorEngine:            decodeSetting(values, "javsp_translator_engine", defaults.TranslatorEngine),
		TimeoutSeconds:              decodeSetting(values, "javsp_timeout_seconds", defaults.TimeoutSeconds),
		LastRunAt:                   decodeSetting(values, "javsp_last_run_at", defaults.LastRunAt),
		NextRunAt:                   decodeSetting(values, "javsp_next_run_at", defaults.NextRunAt),
		IgnoredIDPatterns:           decodeSetting(values, "javsp_ignored_id_patterns", defaults.IgnoredIDPatterns),
		FilenameExtensions:          decodeSetting(values, "javsp_filename_extensions", defaults.FilenameExtensions),
		IgnoredFolderPatterns:       decodeSetting(values, "javsp_ignored_folder_patterns", defaults.IgnoredFolderPatterns),
		SkipNFODir:                  decodeSetting(values, "javsp_skip_nfo_dir", defaults.SkipNFODir),
		ProxyFree:                   decodeStringMapSetting(values, "javsp_proxy_free", defaults.ProxyFree),
		NetworkRetry:                decodeSetting(values, "javsp_network_retry", defaults.NetworkRetry),
		NetworkTimeout:              decodeSetting(values, "javsp_network_timeout", defaults.NetworkTimeout),
		NormalCrawlers:              decodeSetting(values, "javsp_normal_crawlers", defaults.NormalCrawlers),
		FC2Crawlers:                 decodeSetting(values, "javsp_fc2_crawlers", defaults.FC2Crawlers),
		CIDCrawlers:                 decodeSetting(values, "javsp_cid_crawlers", defaults.CIDCrawlers),
		GetchuCrawlers:              decodeSetting(values, "javsp_getchu_crawlers", defaults.GetchuCrawlers),
		GyuttoCrawlers:              decodeSetting(values, "javsp_gyutto_crawlers", defaults.GyuttoCrawlers),
		RequiredKeys:                decodeSetting(values, "javsp_required_keys", defaults.RequiredKeys),
		MovieConcurrency:            decodeSetting(values, "javsp_movie_concurrency", defaults.MovieConcurrency),
		Hardworking:                 decodeSetting(values, "javsp_hardworking", defaults.Hardworking),
		RespectSiteAVID:             decodeSetting(values, "javsp_respect_site_avid", defaults.RespectSiteAVID),
		FC2FanLocalPath:             decodeSetting(values, "javsp_fc2fan_local_path", defaults.FC2FanLocalPath),
		SleepAfterScraping:          decodeSetting(values, "javsp_sleep_after_scraping", defaults.SleepAfterScraping),
		UseJavDBCover:               decodeSetting(values, "javsp_use_javdb_cover", defaults.UseJavDBCover),
		NormalizeActressName:        decodeSetting(values, "javsp_normalize_actress_name", defaults.NormalizeActressName),
		PathBasenamePattern:         decodeSetting(values, "javsp_path_basename_pattern", defaults.PathBasenamePattern),
		PathLengthMaximum:           decodeSetting(values, "javsp_path_length_maximum", defaults.PathLengthMaximum),
		PathLengthByByte:            decodeSetting(values, "javsp_path_length_by_byte", defaults.PathLengthByByte),
		PathMaxActressCount:         decodeSetting(values, "javsp_path_max_actress_count", defaults.PathMaxActressCount),
		PathHardLink:                decodeSetting(values, "javsp_path_hard_link", defaults.PathHardLink),
		RemoveTrailingActorName:     decodeSetting(values, "javsp_remove_trailing_actor_name", defaults.RemoveTrailingActorName),
		DefaultTitle:                decodeSetting(values, "javsp_default_title", defaults.DefaultTitle),
		DefaultActress:              decodeSetting(values, "javsp_default_actress", defaults.DefaultActress),
		DefaultSeries:               decodeSetting(values, "javsp_default_series", defaults.DefaultSeries),
		DefaultDirector:             decodeSetting(values, "javsp_default_director", defaults.DefaultDirector),
		DefaultProducer:             decodeSetting(values, "javsp_default_producer", defaults.DefaultProducer),
		DefaultPublisher:            decodeSetting(values, "javsp_default_publisher", defaults.DefaultPublisher),
		NFOBasenamePattern:          decodeSetting(values, "javsp_nfo_basename_pattern", defaults.NFOBasenamePattern),
		NFOTitlePattern:             decodeSetting(values, "javsp_nfo_title_pattern", defaults.NFOTitlePattern),
		NFOCustomGenresFields:       decodeSetting(values, "javsp_nfo_custom_genres_fields", defaults.NFOCustomGenresFields),
		NFOCustomTagsFields:         decodeSetting(values, "javsp_nfo_custom_tags_fields", defaults.NFOCustomTagsFields),
		CensorOptionsRepresentation: decodeSetting(values, "javsp_censor_options_representation", defaults.CensorOptionsRepresentation),
		CoverBasenamePattern:        decodeSetting(values, "javsp_cover_basename_pattern", defaults.CoverBasenamePattern),
		CoverHighRes:                decodeSetting(values, "javsp_cover_highres", defaults.CoverHighRes),
		CoverAddLabel:               decodeSetting(values, "javsp_cover_add_label", defaults.CoverAddLabel),
		CoverCropOnIDPatterns:       decodeSetting(values, "javsp_cover_crop_on_id_patterns", defaults.CoverCropOnIDPatterns),
		CoverCropEngine:             decodeSetting(values, "javsp_cover_crop_engine", defaults.CoverCropEngine),
		FanartBasenamePattern:       decodeSetting(values, "javsp_fanart_basename_pattern", defaults.FanartBasenamePattern),
		ExtraFanartScrapInterval:    decodeSetting(values, "javsp_extra_fanart_scrap_interval", defaults.ExtraFanartScrapInterval),
		TranslatorURL:               decodeSetting(values, "javsp_translator_url", defaults.TranslatorURL),
		TranslatorModel:             decodeSetting(values, "javsp_translator_model", defaults.TranslatorModel),
		TranslatorAppID:             decodeSetting(values, "javsp_translator_app_id", defaults.TranslatorAppID),
		TranslatorAPIKey:            decodeSetting(values, "javsp_translator_api_key", defaults.TranslatorAPIKey),
		TranslateTitle:              decodeSetting(values, "javsp_translate_title", defaults.TranslateTitle),
		TranslatePlot:               decodeSetting(values, "javsp_translate_plot", defaults.TranslatePlot),
	}
	if !includeSecret {
		config.TranslatorAPIKeySet = config.TranslatorAPIKey != ""
		config.TranslatorAPIKey = ""
	}
	return config
}

var errJavspConfigConflict = errors.New("JAVSP 配置已更新，请重新读取后再保存")

func (d *Database) UpdateJavspConfig(config JavspConfig) (JavspConfig, error) {
	return d.updateJavspConfig(context.Background(), config, nil)
}

func (d *Database) UpdateJavspConfigIfRevision(ctx context.Context, config JavspConfig) (JavspConfig, error) {
	return d.updateJavspConfig(ctx, config, &config.Revision)
}

func (d *Database) updateJavspConfig(ctx context.Context, config JavspConfig, revision *int64) (JavspConfig, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return JavspConfig{}, err
	}
	defer tx.Rollback()
	stored, err := readSettings(ctx, tx)
	if err != nil {
		return JavspConfig{}, err
	}
	current := javspConfigFromSettings(stored, true)
	if revision != nil && *revision != current.Revision {
		return JavspConfig{}, errJavspConfigConflict
	}
	// Runtime timestamps are not editable settings. In particular, saving a
	// stale browser form must not erase a run or postpone an unchanged timer.
	config.NextRunAt = current.NextRunAt
	if !config.Enabled {
		config.NextRunAt = nil
	} else if !current.Enabled || current.NextRunAt == nil ||
		config.ScheduleType != current.ScheduleType || config.ScheduleMinutes != current.ScheduleMinutes || config.ScheduleTime != current.ScheduleTime {
		next, err := calculateNextRun(config.ScheduleType, config.ScheduleMinutes, config.ScheduleTime, time.Now())
		if err != nil {
			return JavspConfig{}, err
		}
		config.NextRunAt = &next
	}
	values := javspSettingsValues(config)
	values["next_run_at"] = config.NextRunAt
	values["revision"] = current.Revision + 1
	for key, value := range values {
		stored["javsp_"+key] = json.RawMessage(encodeJSON(value))
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, "javsp_"+key, encodeJSON(value), nowUTC()); err != nil {
			return JavspConfig{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return JavspConfig{}, err
	}
	return javspConfigFromSettings(stored, false), nil
}

func javspSettingsValues(config JavspConfig) map[string]any {
	return map[string]any{
		"enabled": config.Enabled, "schedule_type": config.ScheduleType,
		"schedule_minutes": config.ScheduleMinutes, "schedule_time": config.ScheduleTime,
		"input_directory": config.InputDirectory, "output_folder_pattern": config.OutputFolderPattern,
		"output_directory": config.OutputDirectory,
		"proxy_server":     config.ProxyServer, "minimum_size": config.MinimumSize,
		"move_files": config.MoveFiles, "extra_fanarts": config.ExtraFanarts,
		"translator_engine": config.TranslatorEngine, "timeout_seconds": config.TimeoutSeconds,
		"ignored_id_patterns": config.IgnoredIDPatterns, "filename_extensions": config.FilenameExtensions,
		"ignored_folder_patterns": config.IgnoredFolderPatterns, "skip_nfo_dir": config.SkipNFODir,
		"proxy_free": config.ProxyFree, "network_retry": config.NetworkRetry, "network_timeout": config.NetworkTimeout,
		"normal_crawlers": config.NormalCrawlers, "fc2_crawlers": config.FC2Crawlers,
		"cid_crawlers": config.CIDCrawlers, "getchu_crawlers": config.GetchuCrawlers,
		"gyutto_crawlers": config.GyuttoCrawlers, "required_keys": config.RequiredKeys,
		"movie_concurrency": config.MovieConcurrency, "hardworking": config.Hardworking,
		"respect_site_avid": config.RespectSiteAVID, "fc2fan_local_path": config.FC2FanLocalPath,
		"sleep_after_scraping": config.SleepAfterScraping, "use_javdb_cover": config.UseJavDBCover,
		"normalize_actress_name": config.NormalizeActressName, "path_basename_pattern": config.PathBasenamePattern,
		"path_length_maximum": config.PathLengthMaximum, "path_length_by_byte": config.PathLengthByByte,
		"path_max_actress_count": config.PathMaxActressCount, "path_hard_link": config.PathHardLink,
		"remove_trailing_actor_name": config.RemoveTrailingActorName, "default_title": config.DefaultTitle,
		"default_actress": config.DefaultActress, "default_series": config.DefaultSeries,
		"default_director": config.DefaultDirector, "default_producer": config.DefaultProducer,
		"default_publisher": config.DefaultPublisher, "nfo_basename_pattern": config.NFOBasenamePattern,
		"nfo_title_pattern": config.NFOTitlePattern, "nfo_custom_genres_fields": config.NFOCustomGenresFields,
		"nfo_custom_tags_fields": config.NFOCustomTagsFields, "censor_options_representation": config.CensorOptionsRepresentation,
		"cover_basename_pattern": config.CoverBasenamePattern, "cover_highres": config.CoverHighRes,
		"cover_add_label": config.CoverAddLabel, "cover_crop_on_id_patterns": config.CoverCropOnIDPatterns,
		"cover_crop_engine": config.CoverCropEngine, "fanart_basename_pattern": config.FanartBasenamePattern,
		"extra_fanart_scrap_interval": config.ExtraFanartScrapInterval,
		"translator_url":              config.TranslatorURL, "translator_model": config.TranslatorModel,
		"translator_app_id": config.TranslatorAppID, "translator_api_key": config.TranslatorAPIKey,
		"translate_title": config.TranslateTitle, "translate_plot": config.TranslatePlot,
	}
}

func (d *Database) MigrateJavspAdvancedConfig(config JavspConfig) error {
	if config.NFOBasenamePattern == "movie" {
		config.NFOBasenamePattern = "{num}"
	}
	values := javspSettingsValues(config)
	for _, key := range []string{
		"enabled", "schedule_type", "schedule_minutes", "schedule_time", "input_directory",
		"output_folder_pattern", "proxy_server", "minimum_size", "move_files", "extra_fanarts",
		"translator_engine", "timeout_seconds",
	} {
		delete(values, key)
	}
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range values {
		if _, err := tx.Exec("INSERT OR IGNORE INTO settings(key,value,updated_at) VALUES(?,?,?)", "javsp_"+key, encodeJSON(value), nowUTC()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *Database) JavspDue() (bool, error) {
	config, err := d.JavspConfig()
	return err == nil && config.Enabled && config.NextRunAt != nil && *config.NextRunAt <= nowUTC(), err
}

func (d *Database) MarkJavspStarted() error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A scanner can hold old settings while the user changes its schedule.
	// Read scheduling policy inside the same transaction as the start stamp.
	values, err := readSettings(context.Background(), tx)
	if err != nil {
		return err
	}
	config := javspConfigFromSettings(values, false)
	started := nowUTC()
	var next *string
	if config.Enabled {
		value, err := calculateNextRun(config.ScheduleType, config.ScheduleMinutes, config.ScheduleTime, time.Now())
		if err != nil {
			return err
		}
		next = &value
	}
	for key, value := range map[string]any{"javsp_last_run_at": started, "javsp_next_run_at": next} {
		if _, err := tx.Exec(`INSERT INTO settings(key,value,updated_at) VALUES(?,?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, encodeJSON(value), started); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func scanTask(scanner interface{ Scan(...any) error }) (Task, error) {
	var task Task
	var enabled int
	var includeAll, includeAny, excludeAny string
	var lastRun, nextRun sql.NullString
	err := scanner.Scan(&task.ID, &task.Name, &task.FeedURL, &enabled, &task.ScheduleType,
		&task.ScheduleMinutes, &task.ScheduleTime, &includeAll, &includeAny, &excludeAny,
		&task.Extractor, &task.ProxyURL, &task.QBCategory, &task.SavePath, &lastRun, &nextRun,
		&task.CreatedAt, &task.UpdatedAt)
	if err != nil {
		return Task{}, err
	}
	task.Enabled = enabled != 0
	_ = json.Unmarshal([]byte(includeAll), &task.IncludeAll)
	_ = json.Unmarshal([]byte(includeAny), &task.IncludeAny)
	_ = json.Unmarshal([]byte(excludeAny), &task.ExcludeAny)
	if task.IncludeAll == nil {
		task.IncludeAll = []string{}
	}
	if task.IncludeAny == nil {
		task.IncludeAny = []string{}
	}
	if task.ExcludeAny == nil {
		task.ExcludeAny = []string{}
	}
	if lastRun.Valid {
		task.LastRunAt = &lastRun.String
	}
	if nextRun.Valid {
		task.NextRunAt = &nextRun.String
	}
	return task, nil
}

const taskColumns = `id,name,feed_url,enabled,schedule_type,schedule_minutes,schedule_time,
include_all,include_any,exclude_any,extractor,proxy_url,qb_category,save_path,last_run_at,next_run_at,created_at,updated_at`

func (d *Database) Tasks() ([]Task, error) {
	rows, err := d.db.Query("SELECT " + taskColumns + " FROM rss_tasks ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Task{}
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, task)
	}
	return items, rows.Err()
}

func (d *Database) Task(id int64) (Task, error) {
	task, err := scanTask(d.db.QueryRow("SELECT "+taskColumns+" FROM rss_tasks WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	return task, err
}

func (d *Database) CreateTask(task Task) (Task, error) {
	now := nowUTC()
	next, err := calculateNextRun(task.ScheduleType, task.ScheduleMinutes, task.ScheduleTime, time.Now())
	if err != nil {
		return Task{}, err
	}
	result, err := d.db.Exec(`INSERT INTO rss_tasks(name,feed_url,enabled,schedule_type,schedule_minutes,
schedule_time,include_all,include_any,exclude_any,extractor,proxy_url,qb_category,save_path,next_run_at,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, task.Name, task.FeedURL, boolInt(task.Enabled), task.ScheduleType,
		task.ScheduleMinutes, task.ScheduleTime, encodeJSON(task.IncludeAll), encodeJSON(task.IncludeAny),
		encodeJSON(task.ExcludeAny), task.Extractor, task.ProxyURL, task.QBCategory, task.SavePath, next, now, now)
	if err != nil {
		return Task{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Task{}, err
	}
	return d.Task(id)
}

func (d *Database) UpdateTask(id int64, task Task) (Task, error) {
	if _, err := d.Task(id); err != nil {
		return Task{}, err
	}
	next, err := calculateNextRun(task.ScheduleType, task.ScheduleMinutes, task.ScheduleTime, time.Now())
	if err != nil {
		return Task{}, err
	}
	_, err = d.db.Exec(`UPDATE rss_tasks SET name=?,feed_url=?,enabled=?,schedule_type=?,schedule_minutes=?,
schedule_time=?,include_all=?,include_any=?,exclude_any=?,extractor=?,proxy_url=?,qb_category=?,save_path=?,next_run_at=?,updated_at=? WHERE id=?`,
		task.Name, task.FeedURL, boolInt(task.Enabled), task.ScheduleType, task.ScheduleMinutes, task.ScheduleTime,
		encodeJSON(task.IncludeAll), encodeJSON(task.IncludeAny), encodeJSON(task.ExcludeAny), task.Extractor,
		task.ProxyURL, task.QBCategory, task.SavePath, next, nowUTC(), id)
	if err != nil {
		return Task{}, err
	}
	return d.Task(id)
}

func (d *Database) DeleteTask(id int64) (bool, error) {
	result, err := d.db.Exec("DELETE FROM rss_tasks WHERE id=?", id)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

func (d *Database) DueTaskIDs() ([]int64, error) {
	rows, err := d.db.Query("SELECT id FROM rss_tasks WHERE enabled=1 AND (next_run_at IS NULL OR next_run_at<=?) ORDER BY id LIMIT 100", nowUTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (d *Database) MarkTaskScheduled(id int64, task Task) error {
	now := nowUTC()
	next, err := calculateNextRun(task.ScheduleType, task.ScheduleMinutes, task.ScheduleTime, time.Now())
	if err != nil {
		return err
	}
	_, err = d.db.Exec("UPDATE rss_tasks SET last_run_at=?,next_run_at=?,updated_at=? WHERE id=?", now, next, now, id)
	return err
}

func (d *Database) StartRun(taskID int64, trigger string, dryRun bool) (int64, error) {
	result, err := d.db.Exec("INSERT INTO task_runs(task_id,trigger_type,status,dry_run,started_at) VALUES(?,?,'running',?,?)", taskID, trigger, boolInt(dryRun), nowUTC())
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (d *Database) FinishRun(id int64, summary RunSummary, status, message string) error {
	settings, err := d.Settings(false)
	if err != nil {
		return err
	}
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE task_runs SET status=?,finished_at=?,entries_count=?,matched_count=?,submitted_count=?,
skipped_count=?,failed_count=?,details=?,error=? WHERE id=?`, status, nowUTC(), summary.Entries, summary.Matched,
		summary.Submitted, summary.Skipped, summary.Failed, encodeJSON(summary.Details), message, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM task_runs
WHERE status!='running' AND id NOT IN (
  SELECT id FROM task_runs WHERE status!='running' ORDER BY id DESC LIMIT ?
)`, settings.RunRetentionCount); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *Database) HasSeen(taskID int64, fp string) (bool, error) {
	var found int
	err := d.db.QueryRow("SELECT 1 FROM seen_items WHERE task_id=? AND fingerprint=?", taskID, fp).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (d *Database) seenStatus(taskID int64, fp string) (string, bool, error) {
	var status string
	err := d.db.QueryRow("SELECT status FROM seen_items WHERE task_id=? AND fingerprint=?", taskID, fp).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return status, err == nil, err
}

func (d *Database) MarkSeen(taskID int64, entry FeedEntry, magnet, status string) error {
	identity := magnet
	if identity == "" {
		identity = entry.EntryID
	}
	if identity == "" {
		identity = entry.SourceURL
	}
	if identity == "" {
		identity = entry.Title
	}
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT OR REPLACE INTO seen_items(task_id,fingerprint,title,source_url,magnet,status,created_at)
VALUES(?,?,?,?,?,?,?)`, taskID, fingerprint(identity), boundedText(entry.Title, 256), "", "", status, nowUTC()); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM seen_items WHERE task_id=? AND rowid NOT IN (
SELECT rowid FROM seen_items WHERE task_id=? ORDER BY created_at DESC LIMIT 10000)`, taskID, taskID); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *Database) UnmarkSeen(taskID int64, itemFingerprint string) error {
	_, err := d.db.Exec("DELETE FROM seen_items WHERE task_id=? AND fingerprint=?", taskID, itemFingerprint)
	return err
}

func (d *Database) StartJavspRun(trigger string) (int64, error) {
	result, err := d.db.Exec("INSERT INTO javsp_runs(trigger_type,status,started_at) VALUES(?,'running',?)", trigger, nowUTC())
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (d *Database) FinishJavspRun(id int64, status string, exitCode *int, output, message string) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE javsp_runs SET status=?,finished_at=?,exit_code=?,output=?,error=? WHERE id=?", status, nowUTC(), exitCode, output, message, id); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM javsp_runs WHERE id NOT IN (SELECT id FROM javsp_runs ORDER BY id DESC LIMIT 100)"); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *Database) ListRuns(limit int) ([]map[string]any, error) {
	limit = clamp(limit, 1, 100)
	rows, err := d.db.Query(`SELECT r.id,r.task_id,r.trigger_type,r.status,r.dry_run,r.started_at,r.finished_at,
r.entries_count,r.matched_count,r.submitted_count,r.skipped_count,r.failed_count,r.details,r.error,t.name
FROM task_runs r JOIN rss_tasks t ON t.id=r.task_id ORDER BY r.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, taskID int64
		var trigger, status, started, details, runError, taskName string
		var dry, entries, matched, submitted, skipped, failed int
		var finished sql.NullString
		if err := rows.Scan(&id, &taskID, &trigger, &status, &dry, &started, &finished, &entries, &matched, &submitted, &skipped, &failed, &details, &runError, &taskName); err != nil {
			return nil, err
		}
		var parsed any = []any{}
		_ = json.Unmarshal([]byte(details), &parsed)
		item := map[string]any{"id": id, "task_id": taskID, "trigger_type": trigger, "status": status, "dry_run": dry != 0,
			"started_at": started, "finished_at": nil, "entries_count": entries, "matched_count": matched,
			"submitted_count": submitted, "skipped_count": skipped, "failed_count": failed, "details": parsed, "error": runError, "task_name": taskName}
		if finished.Valid {
			item["finished_at"] = finished.String
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (d *Database) ListJavspRuns(limit int) ([]map[string]any, error) {
	limit = clamp(limit, 1, 200)
	rows, err := d.db.Query("SELECT id,trigger_type,status,started_at,finished_at,exit_code,output,error FROM javsp_runs ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var trigger, status, started, output, runError string
		var finished sql.NullString
		var exit sql.NullInt64
		if err := rows.Scan(&id, &trigger, &status, &started, &finished, &exit, &output, &runError); err != nil {
			return nil, err
		}
		item := map[string]any{"id": id, "trigger_type": trigger, "status": status, "started_at": started, "finished_at": nil, "exit_code": nil, "output": output, "error": runError}
		if finished.Valid {
			item["finished_at"] = finished.String
		}
		if exit.Valid {
			item["exit_code"] = exit.Int64
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (d *Database) Dashboard() (map[string]any, error) {
	tasks, err := d.Tasks()
	if err != nil {
		return nil, err
	}
	runs, err := d.ListRuns(8)
	if err != nil {
		return nil, err
	}
	today := time.Now().UTC().Format("2006-01-02")
	var count, matched, submitted, failed int
	err = d.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(matched_count),0),COALESCE(SUM(submitted_count),0),
COALESCE(SUM(failed_count),0) FROM task_runs WHERE substr(started_at,1,10)=?`, today).Scan(&count, &matched, &submitted, &failed)
	if err != nil {
		return nil, err
	}
	return map[string]any{"tasks": tasks, "recent_runs": runs, "today": map[string]int{"runs": count, "matched": matched, "submitted": submitted, "failed": failed}}, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func clamp(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

var ErrNotFound = fmt.Errorf("not found")
