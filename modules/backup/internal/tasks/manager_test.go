package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"fnos-backup-center/internal/backup"
)

type fakeExecutor struct {
	calls    atomic.Int32
	release  chan struct{}
	failures int32
}

func (e *fakeExecutor) Backup(ctx context.Context, s backup.Selection, p func(backup.Progress)) (backup.Result, error) {
	n := e.calls.Add(1)
	if p != nil {
		p(backup.Progress{Phase: "copying", Total: 2, Files: 1})
	}
	if e.release != nil {
		select {
		case <-e.release:
		case <-ctx.Done():
			return backup.Result{State: "canceled"}, ctx.Err()
		}
	}
	if n <= e.failures {
		return backup.Result{State: "failed"}, errors.New("transient fixture error")
	}
	return backup.Result{State: "pending", SnapshotID: backup.NewID(), Copied: 2, Notice: backup.CloudNotice}, nil
}
func (e *fakeExecutor) Preview(context.Context, backup.Selection) (backup.Preview, error) {
	return backup.Preview{New: 2, Files: 2}, nil
}
func (e *fakeExecutor) Restore(ctx context.Context, s backup.Selection, id, dest string, p func(backup.Progress)) (backup.Result, error) {
	return backup.Result{State: "restored", SnapshotID: id}, nil
}

func newManager(t *testing.T, e Executor) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	m, err := Open(dir, e)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ctx, c := context.WithTimeout(context.Background(), time.Second); defer c(); m.Close(ctx) })
	return m, dir
}
func sampleTask() Task {
	return Task{Name: "fixture", Selection: backup.Selection{Source: "/data/source", Target: "/mnt/cloud", RepoID: backup.NewID(), Mount: backup.Mount{Point: "/mnt/cloud", Source: "test:remote", Type: "fuse.rclone", Root: "/"}}, Schedule: Schedule{Enabled: false, Kind: "interval", Minutes: 60, Zone: "UTC"}, Retries: 1}
}

func TestRepositoryCannotHaveTwoWritableTasks(t *testing.T) {
	m, _ := newManager(t, &fakeExecutor{})
	first, err := m.SaveTask(sampleTask())
	if err != nil {
		t.Fatal(err)
	}
	duplicate := first
	duplicate.ID = ""
	duplicate.Name = "second writer"
	if _, err := m.SaveTask(duplicate); err == nil {
		t.Fatal("duplicate repository writer accepted")
	}
	duplicate.ReadOnly = true
	if _, err := m.SaveTask(duplicate); err != nil {
		t.Fatal("read-only import rejected", err)
	}
}

func TestExistingTaskCanSwitchToDirectCopyWithoutChangingItsPaths(t *testing.T) {
	m, _ := newManager(t, &fakeExecutor{})
	legacy, err := m.SaveTask(sampleTask())
	if err != nil {
		t.Fatal(err)
	}
	legacy.Selection.Mode = "direct"
	updated, err := m.SaveTask(legacy)
	if err != nil || updated.Selection.Mode != "direct" {
		t.Fatalf("switch to direct copy: %+v %v", updated, err)
	}
	if got := m.State().Tasks[0]; got.Selection.Target != legacy.Selection.Target || got.Selection.Source != legacy.Selection.Source {
		t.Fatal("switch changed task paths")
	}
}

func TestLoadRejectsDuplicateRepositoryWriters(t *testing.T) {
	dir := t.TempDir()
	first := sampleTask()
	first.ID = backup.NewID()
	other := first
	other.ID = backup.NewID()
	s := State{Schema: 1, Retain: 100, Tasks: []Task{first, other}}
	if err := persist(dir, s); err != nil {
		t.Fatal(err)
	}
	if _, err := load(dir); err == nil {
		t.Fatal("persisted duplicate writers accepted")
	}
	s.Tasks[1].ReadOnly = true
	if err := persist(dir, s); err != nil {
		t.Fatal(err)
	}
	if _, err := load(dir); err != nil {
		t.Fatal(err)
	}
}

type ambiguousExecutor struct{ fakeExecutor }

func (e *ambiguousExecutor) Backup(context.Context, backup.Selection, func(backup.Progress)) (backup.Result, error) {
	e.calls.Add(1)
	return backup.Result{State: "publication_unknown", SnapshotID: backup.NewID()}, nil
}
func TestUnknownPublicationIsNotRetried(t *testing.T) {
	e := &ambiguousExecutor{}
	m, _ := newManager(t, e)
	task := sampleTask()
	task.Retries = 3
	saved, err := m.SaveTask(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(saved.ID, "backup", "manual", "", ""); err != nil {
		t.Fatal(err)
	}
	wait(t, m)
	if e.calls.Load() != 1 || m.State().Runs[0].Status != "publication_unknown" {
		t.Fatal("ambiguous commit retried or mislabeled")
	}
}
func wait(t *testing.T, m *Manager) {
	t.Helper()
	ctx, c := context.WithTimeout(context.Background(), 3*time.Second)
	defer c()
	if err := m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCreateDefaultsToDisabledScheduleAndPreservesRestart(t *testing.T) {
	m, dir := newManager(t, &fakeExecutor{})
	task, err := m.SaveTask(sampleTask())
	if err != nil {
		t.Fatal(err)
	}
	if task.Schedule.Enabled || !task.Next.IsZero() {
		t.Fatal("new task automatically scheduled")
	}
	if _, err = m.Start(task.ID, "backup", "manual", "", ""); err != nil {
		t.Fatal(err)
	}
	wait(t, m)
	s := m.State()
	if len(s.Runs) != 1 || s.Runs[0].Status != "pending" {
		t.Fatalf("%+v", s)
	}
	ctx, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	if err = m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, &fakeExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(ctx)
	loaded := reopened.State()
	if len(loaded.Tasks) != 1 || len(loaded.Runs) != 1 || loaded.Tasks[0].ID != task.ID {
		t.Fatal("state lost")
	}
}

func TestCreatePreservesExplicitlyEnabledSchedule(t *testing.T) {
	m, _ := newManager(t, &fakeExecutor{})
	task := sampleTask()
	task.Schedule.Enabled = true
	saved, err := m.SaveTask(task)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Schedule.Enabled || saved.Next.IsZero() {
		t.Fatalf("explicit schedule was discarded: %+v", saved)
	}
}

func TestCancelAndSameTaskExclusion(t *testing.T) {
	e := &fakeExecutor{release: make(chan struct{})}
	m, _ := newManager(t, e)
	task, err := m.SaveTask(sampleTask())
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.Start(task.ID, "backup", "manual", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Start(task.ID, "backup", "manual", "", ""); err == nil {
		t.Fatal("overlap allowed")
	}
	if _, err = m.SaveTask(task); err == nil {
		t.Fatal("edited active task")
	}
	if err = m.DeleteTask(task.ID); err == nil {
		t.Fatal("deleted active task")
	}
	if err = m.Cancel(r.ID); err != nil {
		t.Fatal(err)
	}
	wait(t, m)
	if got := m.State().Runs[0].Status; got != "canceled" {
		t.Fatal(got)
	}
}

func TestHistoryRetentionAndPreview(t *testing.T) {
	m, _ := newManager(t, &fakeExecutor{})
	task, err := m.SaveTask(sampleTask())
	if err != nil {
		t.Fatal(err)
	}
	if err = m.SetRetention(2); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err = m.Start(task.ID, "preview", "manual", "", ""); err != nil {
			t.Fatal(err)
		}
		wait(t, m)
	}
	s := m.State()
	if len(s.Runs) != 2 || s.Runs[0].Preview == nil || s.Runs[0].Status != "previewed" {
		t.Fatal("preview/retention failed")
	}
	if err = m.SetRetention(0); err == nil {
		t.Fatal("invalid retention accepted")
	}
}

func TestInterruptedRecoveryAndExclusiveProcessLock(t *testing.T) {
	dir := t.TempDir()
	state := State{Schema: 1, Retain: 100, Tasks: []Task{}, Runs: []Run{{ID: backup.NewID(), Status: "running"}, {ID: backup.NewID(), Status: "cancel_requested"}}}
	b, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(dir, &fakeExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	for _, r := range m.State().Runs {
		if r.Status != "interrupted" {
			t.Fatal(r)
		}
	}
	if other, err := Open(dir, &fakeExecutor{}); err == nil {
		other.Close(context.Background())
		t.Fatal("second process accepted")
	}
}

func TestScheduleCoalescesAndReadOnlyImports(t *testing.T) {
	e := &fakeExecutor{}
	m, _ := newManager(t, e)
	task, err := m.SaveTask(sampleTask())
	if err != nil {
		t.Fatal(err)
	}
	task.Schedule.Enabled = true
	task, err = m.SaveTask(task)
	if err != nil {
		t.Fatal(err)
	}
	if task.Next.IsZero() {
		t.Fatal("missing next run")
	}
	due := task.Next.Add(24 * time.Hour)
	if err = m.Tick(due); err != nil {
		t.Fatal(err)
	}
	wait(t, m)
	if err = m.Tick(due); err != nil {
		t.Fatal(err)
	}
	wait(t, m)
	if e.calls.Load() != 1 {
		t.Fatal("overdue schedule replayed")
	}
	imported := sampleTask()
	imported.Selection.Source = ""
	imported.ReadOnly = true
	imported, err = m.SaveTask(imported)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Start(imported.ID, "backup", "manual", "", ""); err == nil {
		t.Fatal("read-only import allowed backup")
	}
	if _, err = m.Start(imported.ID, "restore", "manual", backup.NewID(), "/data/new-restore"); err != nil {
		t.Fatal(err)
	}
	wait(t, m)
}

func TestPersistenceFailureDoesNotStartWork(t *testing.T) {
	e := &fakeExecutor{}
	m, dir := newManager(t, e)
	task, err := m.SaveTask(sampleTask())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "state.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "state.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Start(task.ID, "backup", "manual", "", ""); err == nil {
		t.Fatal("work started without durable record")
	}
	if e.calls.Load() != 0 {
		t.Fatal("executor ran after persistence failure")
	}
}
