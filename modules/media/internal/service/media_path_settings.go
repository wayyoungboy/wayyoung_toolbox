package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// MediaPathSettings is a view of existing settings, not a second source of
// truth. Both revisions protect edits made through the advanced module pages.
type MediaPathSettings struct {
	IntakeRevision        int64  `json:"intake_revision"`
	JavspRevision         int64  `json:"javsp_revision"`
	DownloadDirectory     string `json:"download_directory"`
	PendingDirectory      string `json:"pending_directory"`
	CompletedDirectory    string `json:"completed_directory"`
	MoveFiles             bool   `json:"move_files"`
	OutputFolderPattern   string `json:"output_folder_pattern"`
	PathBasenamePattern   string `json:"path_basename_pattern"`
	NFOBasenamePattern    string `json:"nfo_basename_pattern"`
	NFOTitlePattern       string `json:"nfo_title_pattern"`
	CoverBasenamePattern  string `json:"cover_basename_pattern"`
	FanartBasenamePattern string `json:"fanart_basename_pattern"`
}

var (
	errMediaPathSettingsConflict = errors.New("目录或相关模块配置已更新，请重新预览后再保存")
	errMediaPathSettingsInvalid  = errors.New("媒体目录配置无效")
)

func mediaPathSettingsFromConfig(intake IntakeConfig, javsp JavspConfig) MediaPathSettings {
	return MediaPathSettings{
		IntakeRevision: intake.Revision, JavspRevision: javsp.Revision,
		DownloadDirectory: intake.SourceDirectory, PendingDirectory: intake.DestinationDirectory,
		CompletedDirectory: javsp.OutputDirectory, MoveFiles: javsp.MoveFiles,
		OutputFolderPattern: javsp.OutputFolderPattern, PathBasenamePattern: javsp.PathBasenamePattern,
		NFOBasenamePattern: javsp.NFOBasenamePattern, NFOTitlePattern: javsp.NFOTitlePattern,
		CoverBasenamePattern: javsp.CoverBasenamePattern, FanartBasenamePattern: javsp.FanartBasenamePattern,
	}
}

func (d *Database) MediaPathSettings(ctx context.Context) (MediaPathSettings, error) {
	stored, err := readSettings(ctx, d.db)
	if err != nil {
		return MediaPathSettings{}, err
	}
	return mediaPathSettingsFromConfig(intakeConfigFromSettings(stored), javspConfigFromSettings(stored, false)), nil
}

// Apply only this resource's fields. Private keys, network settings, filters,
// timers and runtime timestamps remain owned by their existing settings.
func applyMediaPathSettings(paths MediaPathSettings, intake IntakeConfig, javsp JavspConfig) (IntakeConfig, JavspConfig, error) {
	if paths.IntakeRevision != intake.Revision || paths.JavspRevision != javsp.Revision {
		return IntakeConfig{}, JavspConfig{}, errMediaPathSettingsConflict
	}
	intake.SourceDirectory, intake.DestinationDirectory = paths.DownloadDirectory, paths.PendingDirectory
	intake, err := validateIntakeConfig(intake)
	if err != nil {
		return IntakeConfig{}, JavspConfig{}, fmt.Errorf("%w：%v", errMediaPathSettingsInvalid, err)
	}
	javsp.InputDirectory, javsp.OutputDirectory = intake.DestinationDirectory, paths.CompletedDirectory
	javsp.MoveFiles = paths.MoveFiles
	javsp.OutputFolderPattern, javsp.PathBasenamePattern = paths.OutputFolderPattern, paths.PathBasenamePattern
	javsp.NFOBasenamePattern, javsp.NFOTitlePattern = paths.NFOBasenamePattern, paths.NFOTitlePattern
	javsp.CoverBasenamePattern, javsp.FanartBasenamePattern = paths.CoverBasenamePattern, paths.FanartBasenamePattern
	// Only naming metadata is previewed. Do not require credentials to validate
	// paths; this copy is not written as the translator configuration.
	javsp.TranslatorEngine, javsp.TranslatorAPIKey = "none", ""
	javsp.TranslatorAPIKeySet, javsp.TranslateTitle, javsp.TranslatePlot = false, false, false
	javsp.TranslatorURL, javsp.TranslatorModel, javsp.TranslatorAppID = "", "", ""
	javsp, err = validateJavspConfig(javsp)
	if err != nil {
		return IntakeConfig{}, JavspConfig{}, fmt.Errorf("%w：%v", errMediaPathSettingsInvalid, err)
	}
	if javsp.MoveFiles {
		root, available, err := plannedJavspOutputRoot(javsp)
		if err != nil || !available {
			return IntakeConfig{}, JavspConfig{}, fmt.Errorf("%w：请指定明确的整理完成目录", errMediaPathSettingsInvalid)
		}
		if configuredPathWithin(intake.SourceDirectory, root) || configuredPathWithin(root, intake.SourceDirectory) {
			return IntakeConfig{}, JavspConfig{}, fmt.Errorf("%w：下载目录与整理完成目录不能重叠", errMediaPathSettingsInvalid)
		}
	}
	return intake, javsp, nil
}

func (d *Database) UpdateMediaPathSettings(ctx context.Context, paths MediaPathSettings) (MediaPathSettings, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return MediaPathSettings{}, err
	}
	defer tx.Rollback()
	stored, err := readSettings(ctx, tx)
	if err != nil {
		return MediaPathSettings{}, err
	}
	intake, javsp, err := applyMediaPathSettings(paths, intakeConfigFromSettings(stored), javspConfigFromSettings(stored, false))
	if err != nil {
		return MediaPathSettings{}, err
	}
	values := map[string]any{
		"intake_source_directory": intake.SourceDirectory, "intake_destination_directory": intake.DestinationDirectory,
		"intake_revision": intake.Revision + 1, "javsp_revision": javsp.Revision + 1,
		"javsp_input_directory": javsp.InputDirectory, "javsp_output_directory": javsp.OutputDirectory,
		"javsp_move_files": javsp.MoveFiles, "javsp_output_folder_pattern": javsp.OutputFolderPattern,
		"javsp_path_basename_pattern": javsp.PathBasenamePattern,
		"javsp_nfo_basename_pattern":  javsp.NFOBasenamePattern, "javsp_nfo_title_pattern": javsp.NFOTitlePattern,
		"javsp_cover_basename_pattern": javsp.CoverBasenamePattern, "javsp_fanart_basename_pattern": javsp.FanartBasenamePattern,
	}
	for key, value := range values {
		stored[key] = json.RawMessage(encodeJSON(value))
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, encodeJSON(value), nowUTC()); err != nil {
			return MediaPathSettings{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return MediaPathSettings{}, err
	}
	return mediaPathSettingsFromConfig(intakeConfigFromSettings(stored), javspConfigFromSettings(stored, false)), nil
}
