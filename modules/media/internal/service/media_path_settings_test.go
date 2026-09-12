package service

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func setupMediaPathSettings(t *testing.T) (*Service, MediaPathSettings) {
	t.Helper()
	s := testService(t)
	intake := defaultIntakeConfig()
	intake.SourceDirectory, intake.DestinationDirectory = t.TempDir(), t.TempDir()
	intake.Enabled = true
	savedIntake, err := s.db.UpdateIntakeConfig(intake)
	if err != nil {
		t.Fatal(err)
	}
	javsp := defaultJavspConfig()
	javsp.InputDirectory, javsp.OutputDirectory = savedIntake.DestinationDirectory, t.TempDir()
	javsp.OutputFolderPattern, javsp.Enabled = "{num}", true
	javsp.TranslatorEngine, javsp.TranslatorAPIKey = "deepl", "stored-synthetic-secret"
	if _, err := s.db.UpdateJavspConfig(javsp); err != nil {
		t.Fatal(err)
	}
	paths, err := s.db.MediaPathSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s, paths
}

func TestMediaPathSettingsAtomicSavePreservesOtherConfiguration(t *testing.T) {
	s, paths := setupMediaPathSettings(t)
	before, err := s.db.allSettings()
	if err != nil {
		t.Fatal(err)
	}
	paths.DownloadDirectory, paths.PendingDirectory, paths.CompletedDirectory = t.TempDir(), t.TempDir(), t.TempDir()
	paths.OutputFolderPattern, paths.PathBasenamePattern = "{year}/{num}", "{num} {title}"
	saved, err := s.db.UpdateMediaPathSettings(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	intake, err := s.db.IntakeConfig()
	if err != nil {
		t.Fatal(err)
	}
	javsp, err := s.db.JavspConfigPrivate()
	if err != nil {
		t.Fatal(err)
	}
	if intake.SourceDirectory != saved.DownloadDirectory || intake.DestinationDirectory != saved.PendingDirectory || javsp.InputDirectory != saved.PendingDirectory || javsp.OutputDirectory != saved.CompletedDirectory || javsp.OutputFolderPattern != paths.OutputFolderPattern || javsp.PathBasenamePattern != paths.PathBasenamePattern {
		t.Fatal("cross-module directory or naming save was incomplete")
	}
	if saved.IntakeRevision != paths.IntakeRevision+1 || saved.JavspRevision != paths.JavspRevision+1 {
		t.Fatal("both revisions must advance")
	}
	after, err := s.db.allSettings()
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range before {
		switch key {
		case "intake_source_directory", "intake_destination_directory", "intake_revision", "javsp_input_directory", "javsp_output_directory", "javsp_output_folder_pattern", "javsp_path_basename_pattern", "javsp_revision":
			continue
		}
		if !reflect.DeepEqual(value, after[key]) {
			t.Errorf("unrelated setting changed: %s", key)
		}
	}
	if _, err := s.db.UpdateMediaPathSettings(context.Background(), paths); !errors.Is(err, errMediaPathSettingsConflict) {
		t.Fatalf("stale shared save accepted: %v", err)
	}
}

func TestMediaPathSettingsDetectsEitherModuleChanging(t *testing.T) {
	for _, module := range []string{"intake", "javsp"} {
		t.Run(module, func(t *testing.T) {
			s, paths := setupMediaPathSettings(t)
			if module == "intake" {
				cfg, _ := s.db.IntakeConfig()
				cfg.RunJavsp = false
				if _, err := s.db.UpdateIntakeConfig(cfg); err != nil {
					t.Fatal(err)
				}
			} else {
				cfg, _ := s.db.JavspConfigPrivate()
				cfg.NetworkRetry = 9
				if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := s.db.allSettings()
			paths.DownloadDirectory = t.TempDir()
			if _, err := s.db.UpdateMediaPathSettings(context.Background(), paths); !errors.Is(err, errMediaPathSettingsConflict) {
				t.Fatalf("stale %s accepted: %v", module, err)
			}
			after, _ := s.db.allSettings()
			if !reflect.DeepEqual(before, after) {
				t.Fatal("conflicting save modified storage")
			}
		})
	}
}

func TestMediaPathSettingsRollbackAndUnsafePaths(t *testing.T) {
	for _, kind := range []string{"storage", "overlap", "output covers pending", "template escape", "missing output"} {
		t.Run(kind, func(t *testing.T) {
			s, paths := setupMediaPathSettings(t)
			before, _ := s.db.allSettings()
			paths.DownloadDirectory, paths.PendingDirectory, paths.CompletedDirectory = t.TempDir(), t.TempDir(), t.TempDir()
			switch kind {
			case "storage":
				if _, err := s.db.db.Exec(`CREATE TRIGGER fail_path_write BEFORE UPDATE ON settings WHEN NEW.key='javsp_input_directory' BEGIN SELECT RAISE(ABORT, 'injected'); END;`); err != nil {
					t.Fatal(err)
				}
			case "overlap":
				paths.CompletedDirectory = paths.DownloadDirectory
			case "output covers pending":
				paths.PendingDirectory = paths.CompletedDirectory
			case "template escape":
				paths.OutputFolderPattern = "../escape/{num}"
			case "missing output":
				paths.CompletedDirectory = filepath.Join(t.TempDir(), "not-created")
			}
			if _, err := s.db.UpdateMediaPathSettings(context.Background(), paths); err == nil {
				t.Fatal("invalid or partially failed shared save accepted")
			}
			after, _ := s.db.allSettings()
			if !reflect.DeepEqual(before, after) {
				t.Fatal("failed shared save left partial settings")
			}
		})
	}
}
