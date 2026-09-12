package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestMediaPathSettingsCompetingSavesHaveOneWinner(t *testing.T) {
	for _, competitor := range []string{"shared", "intake", "javsp"} {
		t.Run(competitor, func(t *testing.T) {
			s, paths := setupMediaPathSettings(t)
			intake, err := s.db.IntakeConfig()
			if err != nil {
				t.Fatal(err)
			}
			javsp, err := s.db.JavspConfigPrivate()
			if err != nil {
				t.Fatal(err)
			}
			first, second := paths, paths
			first.PendingDirectory, second.PendingDirectory = t.TempDir(), t.TempDir()
			intake.RunJavsp = !intake.RunJavsp
			javsp.NetworkRetry++
			start := make(chan struct{})
			results := make(chan error, 2)
			go func() { <-start; _, err := s.db.UpdateMediaPathSettings(context.Background(), first); results <- err }()
			go func() {
				<-start
				var err error
				switch competitor {
				case "shared":
					_, err = s.db.UpdateMediaPathSettings(context.Background(), second)
				case "intake":
					_, err = s.db.UpdateIntakeConfigIfRevision(context.Background(), intake)
				case "javsp":
					_, err = s.db.UpdateJavspConfigIfRevision(context.Background(), javsp)
				}
				results <- err
			}()
			close(start)
			successes, conflicts := 0, 0
			for i := 0; i < 2; i++ {
				err := <-results
				switch {
				case err == nil:
					successes++
				case errors.Is(err, errMediaPathSettingsConflict), errors.Is(err, errIntakeConfigConflict), errors.Is(err, errJavspConfigConflict):
					conflicts++
				default:
					t.Fatalf("unexpected concurrent save error: %v", err)
				}
			}
			if successes != 1 || conflicts != 1 {
				t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
			}
			storedIntake, err := s.db.IntakeConfig()
			if err != nil {
				t.Fatal(err)
			}
			storedJavsp, err := s.db.JavspConfig()
			if err != nil || storedIntake.DestinationDirectory != storedJavsp.InputDirectory {
				t.Fatalf("concurrent save split shared directories: intake=%q javsp=%q error=%v", storedIntake.DestinationDirectory, storedJavsp.InputDirectory, err)
			}
		})
	}
}

func TestMediaPathSettingsCanceledWaitPreservesBothModules(t *testing.T) {
	s, paths := setupMediaPathSettings(t)
	before, err := s.db.allSettings()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := s.db.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.db.UpdateMediaPathSettings(ctx, paths); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(2 * time.Second):
		_ = conn.Close()
		<-done
		t.Fatal("shared save did not stop waiting after cancellation")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := s.db.allSettings()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("canceled wait changed shared settings")
	}
}

func TestMediaPathSettingsSavePreservesNewRuntimeTimestamps(t *testing.T) {
	s, paths := setupMediaPathSettings(t)
	intake, err := s.db.IntakeConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.MarkIntakeStarted(intake); err != nil {
		t.Fatal(err)
	}
	if err := s.db.MarkJavspStarted(); err != nil {
		t.Fatal(err)
	}
	before, err := s.db.allSettings()
	if err != nil {
		t.Fatal(err)
	}
	paths.PendingDirectory = t.TempDir()
	if _, err := s.db.UpdateMediaPathSettings(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	after, err := s.db.allSettings()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"intake_last_run_at", "intake_next_run_at", "javsp_last_run_at", "javsp_next_run_at"} {
		if string(before[key]) == "null" || !reflect.DeepEqual(before[key], after[key]) {
			t.Errorf("lost runtime bookkeeping: %s", key)
		}
	}
}

func TestIntakeConfigLegacyRevisionStartsAtZeroAfterReopen(t *testing.T) {
	db, path := newQueueTestDB(t)
	cfg := defaultIntakeConfig()
	cfg.SourceDirectory, cfg.DestinationDirectory = t.TempDir(), t.TempDir()
	if _, err := db.UpdateIntakeConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`DELETE FROM settings WHERE key='intake_revision'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := OpenDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	legacy, err := db.IntakeConfig()
	if err != nil || legacy.Revision != 0 {
		t.Fatalf("legacy revision: %+v %v", legacy, err)
	}
	saved, err := db.UpdateIntakeConfigIfRevision(context.Background(), legacy)
	if err != nil || saved.Revision != 1 || saved.SourceDirectory != legacy.SourceDirectory || saved.DestinationDirectory != legacy.DestinationDirectory {
		t.Fatalf("legacy CAS upgrade: %+v %v", saved, err)
	}
}
