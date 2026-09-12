package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestIntakeConfigRevisionProtectsPathsAndPreservesSchedule(t *testing.T) {
	s := testService(t)
	cfg := defaultIntakeConfig()
	cfg.SourceDirectory, cfg.DestinationDirectory = t.TempDir(), t.TempDir()
	cfg.Enabled = true
	saved, err := s.db.UpdateIntakeConfigIfRevision(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 || saved.NextRunAt == nil {
		t.Fatalf("missing first revision/schedule: %+v", saved)
	}
	old := saved
	saved.SourceDirectory = t.TempDir()
	updated, err := s.db.UpdateIntakeConfigIfRevision(context.Background(), saved)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || !reflect.DeepEqual(updated.NextRunAt, old.NextRunAt) {
		t.Fatal("path save postponed timer or missed revision")
	}
	if _, err := s.db.UpdateIntakeConfigIfRevision(context.Background(), old); !errors.Is(err, errIntakeConfigConflict) {
		t.Fatalf("stale save accepted: %v", err)
	}
	after, err := s.db.IntakeConfig()
	if err != nil || !reflect.DeepEqual(updated, after) {
		t.Fatal("stale save changed configuration")
	}
}

func TestIntakeConfigStartUsesCurrentSchedule(t *testing.T) {
	for _, disable := range []bool{true, false} {
		t.Run(map[bool]string{true: "disabled", false: "interval changed"}[disable], func(t *testing.T) {
			s := testService(t)
			cfg := defaultIntakeConfig()
			cfg.SourceDirectory, cfg.DestinationDirectory, cfg.Enabled = t.TempDir(), t.TempDir(), true
			old, err := s.db.UpdateIntakeConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Enabled, cfg.ScheduleMinutes = !disable, 180
			saved, err := s.db.UpdateIntakeConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.db.MarkIntakeStarted(old); err != nil {
				t.Fatal(err)
			}
			after, err := s.db.IntakeConfig()
			if err != nil {
				t.Fatal(err)
			}
			if after.LastRunAt == nil || after.Revision != saved.Revision {
				t.Fatal("bookkeeping changed revision or lost timestamp")
			}
			if disable {
				if after.NextRunAt != nil {
					t.Fatal("stale run restored disabled schedule")
				}
			} else {
				if after.NextRunAt == nil {
					t.Fatal("missing due time")
				}
				next, err := time.Parse(time.RFC3339, *after.NextRunAt)
				if err != nil || time.Until(next) < 179*time.Minute {
					t.Fatal("stale run restored old interval")
				}
			}
		})
	}
}

func TestIntakeConfigCanceledDatabaseWaitHasNoWrite(t *testing.T) {
	s := testService(t)
	cfg := defaultIntakeConfig()
	cfg.SourceDirectory, cfg.DestinationDirectory = t.TempDir(), t.TempDir()
	before, err := s.db.UpdateIntakeConfig(cfg)
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
	go func() { _, err := s.db.UpdateIntakeConfigIfRevision(ctx, before); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(2 * time.Second):
		_ = conn.Close()
		<-done
		t.Fatal("canceled save kept waiting")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := s.db.IntakeConfig()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("canceled save changed configuration")
	}
}
