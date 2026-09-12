package service

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestJavspConfigCanceledWhileWaitingForDatabase(t *testing.T) {
	s := testService(t)
	cfg := defaultJavspConfig()
	cfg.InputDirectory = t.TempDir()
	before, err := s.db.UpdateJavspConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := s.db.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		candidate := before
		candidate.NetworkRetry = 9
		_, err := s.db.UpdateJavspConfigIfRevision(ctx, candidate)
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waiting save returned %v, want deadline exceeded", err)
		}
	case <-time.After(2 * time.Second):
		_ = connection.Close()
		<-result
		t.Fatal("canceled save continued waiting for the database connection")
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := s.db.JavspConfig()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("canceled save changed configuration or revision: %v", err)
	}
}

func TestJavspStartCannotRestoreStaleSchedule(t *testing.T) {
	for _, change := range []string{"disable", "new_interval"} {
		t.Run(change, func(t *testing.T) {
			s := testService(t)
			cfg := defaultJavspConfig()
			cfg.InputDirectory, cfg.Enabled = t.TempDir(), true
			cfg.ScheduleType, cfg.ScheduleMinutes = "interval", 60
			old, err := s.db.UpdateJavspConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			changed := old
			if change == "disable" {
				changed.Enabled = false
			} else {
				changed.ScheduleMinutes = 180
			}
			saved, err := s.db.UpdateJavspConfig(changed)
			if err != nil {
				t.Fatal(err)
			}
			// The already accepted scan holds old settings while a user saves a
			// new schedule before that scan records its start time.
			if err := s.db.MarkJavspStarted(); err != nil {
				t.Fatal(err)
			}
			after, err := s.db.JavspConfig()
			if err != nil {
				t.Fatal(err)
			}
			if after.LastRunAt == nil || after.Revision != saved.Revision {
				t.Fatal("run bookkeeping changed config revision or lost start time")
			}
			if change == "disable" {
				if after.NextRunAt != nil {
					t.Fatal("old scan restored a disabled schedule's due time")
				}
			} else {
				if after.NextRunAt == nil {
					t.Fatal("new schedule lost due time")
				}
				next, err := time.Parse(time.RFC3339, *after.NextRunAt)
				if err != nil || time.Until(next) < 179*time.Minute || time.Until(next) > 181*time.Minute {
					t.Fatal("old scan overwrote the new schedule interval")
				}
			}
		})
	}
}

func TestJavspConfigRevisionSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := OpenDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg, err := db.JavspConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.InputDirectory = t.TempDir()
	saved, err := db.UpdateJavspConfigIfRevision(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := db.JavspConfig()
	if err != nil || !reflect.DeepEqual(saved, after) {
		t.Fatalf("reopen changed configuration revision: %v", err)
	}
	if _, err := db.UpdateJavspConfigIfRevision(context.Background(), cfg); !errors.Is(err, errJavspConfigConflict) {
		t.Fatalf("reopen allowed a stale save: %v", err)
	}
}
