package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fnos-backup-center/internal/backup"
)

type Executor interface {
	Backup(context.Context, backup.Selection, func(backup.Progress)) (backup.Result, error)
	Preview(context.Context, backup.Selection) (backup.Preview, error)
	Restore(context.Context, backup.Selection, string, string, func(backup.Progress)) (backup.Result, error)
}
type job struct {
	cancel context.CancelFunc
	runID  string
}
type Manager struct {
	mu       sync.Mutex
	dir      string
	executor Executor
	state    State
	busy     map[string]job
	idle     chan struct{}
	closed   bool
	lock     *os.File
}

func Open(dir string, executor Executor) (*Manager, error) {
	if !filepath.IsAbs(dir) || executor == nil {
		return nil, errors.New("absolute data directory and executor required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("invalid data directory")
	}
	lock, err := acquire(dir)
	if err != nil {
		return nil, err
	}
	s, err := load(dir)
	if err != nil {
		lock.Close()
		return nil, err
	}
	if err = persist(dir, s); err != nil {
		lock.Close()
		return nil, err
	}
	idle := make(chan struct{})
	close(idle)
	return &Manager{dir: dir, executor: executor, state: s, busy: map[string]job{}, idle: idle, lock: lock}, nil
}

func clone(s State) State {
	b, _ := json.Marshal(s)
	var out State
	_ = json.Unmarshal(b, &out)
	return out
}
func (m *Manager) State() State { m.mu.Lock(); defer m.mu.Unlock(); return clone(m.state) }
func (m *Manager) commit(s State) error {
	if err := persist(m.dir, s); err != nil {
		return fmt.Errorf("persist task state: %w", err)
	}
	m.state = s
	return nil
}

func (m *Manager) SaveTask(t Task) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return t, errors.New("service is closing")
	}
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" || len(t.Name) > 200 || t.Retries < 0 || t.Retries > 3 {
		return t, errors.New("invalid name or retry count")
	}
	s := clone(m.state)
	index := -1
	if t.ID != "" {
		for i, item := range s.Tasks {
			if item.ID == t.ID {
				index = i
				break
			}
		}
		if index < 0 {
			return t, errors.New("task not found")
		}
	}
	if _, ok := m.busy[t.ID]; ok {
		return t, errors.New("任务正在运行，不能修改配置")
	}
	if index < 0 {
		if len(s.Tasks) >= 100 {
			return t, errors.New("最多支持 100 个任务")
		}
		t.ID = backup.NewID()
		t.Created = time.Now().UTC()
		if t.Selection.RepoID == "" {
			t.Selection.RepoID = backup.NewID()
		}
	} else {
		old := s.Tasks[index]
		t.Created = old.Created
		oldSelection, newSelection := old.Selection, t.Selection
		oldSelection.Mode, newSelection.Mode = "", ""
		if oldSelection != newSelection || t.ReadOnly != old.ReadOnly ||
			(old.Selection.Mode != t.Selection.Mode && (t.Selection.Mode != backup.ModeDirect || old.Selection.Mode == backup.ModeDirect)) {
			return t, errors.New("目录或仓库身份不可直接改写，请新建任务；旧备份仍保留")
		}
	}
	if t.Schedule.Kind == "" {
		t.Schedule.Kind = "daily"
	}
	if t.Schedule.At == "" {
		t.Schedule.At = "02:00"
	}
	if t.Schedule.Zone == "" {
		t.Schedule.Zone = "Asia/Shanghai"
	}
	if t.ReadOnly {
		t.Schedule.Enabled = false
	}
	if err := t.Selection.Validate(!t.ReadOnly); err != nil {
		return t, err
	}
	if !t.ReadOnly {
		for _, other := range s.Tasks {
			if other.ID != t.ID && !other.ReadOnly && other.Selection.RepoID == t.Selection.RepoID {
				return t, errors.New("该备份仓库已有写入任务，不能重复接管")
			}
		}
	}
	var err error
	t.Next, err = NextRun(t.Schedule, time.Now().UTC())
	if err != nil {
		return t, err
	}
	if index < 0 {
		s.Tasks = append(s.Tasks, t)
	} else {
		s.Tasks[index] = t
	}
	return t, m.commit(s)
}

func (m *Manager) DeleteTask(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("service is closing")
	}
	if _, ok := m.busy[id]; ok {
		return errors.New("运行中的任务不能删除")
	}
	s := clone(m.state)
	for i, t := range s.Tasks {
		if t.ID == id {
			s.Tasks = append(s.Tasks[:i], s.Tasks[i+1:]...)
			return m.commit(s)
		}
	}
	return errors.New("task not found")
}

// Rebind is called only after the API has verified the existing repository marker
// and snapshots at the new target. It never changes source or repository identity.
func (m *Manager) Rebind(id string, selection backup.Selection) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Task{}, errors.New("service is closing")
	}
	if _, busy := m.busy[id]; busy {
		return Task{}, errors.New("运行中的任务不能重新选择挂载")
	}
	s := clone(m.state)
	for i := range s.Tasks {
		if s.Tasks[i].ID == id {
			t := s.Tasks[i]
			if selection.RepoID != t.Selection.RepoID || selection.Source != t.Selection.Source {
				return Task{}, errors.New("不能更改源目录或仓库身份")
			}
			if err := selection.Validate(!t.ReadOnly); err != nil {
				return Task{}, err
			}
			t.Selection = selection
			t.Schedule.Enabled = false
			t.Next = time.Time{}
			s.Tasks[i] = t
			return t, m.commit(s)
		}
	}
	return Task{}, errors.New("task not found")
}
func (m *Manager) SetRetention(count int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("service is closing")
	}
	if count < 1 || count > 10000 {
		return errors.New("记录保留量应为 1–10000")
	}
	s := clone(m.state)
	s.Retain = count
	prune(&s)
	return m.commit(s)
}

func (m *Manager) Start(id, kind, trigger, snapshotID, destination string) (Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startLocked(id, kind, trigger, snapshotID, destination, time.Now().UTC())
}
func (m *Manager) startLocked(id, kind, trigger, snapshotID, destination string, now time.Time) (Run, error) {
	var run Run
	if m.closed {
		return run, errors.New("service is closing")
	}
	if _, ok := m.busy[id]; ok {
		return run, errors.New("同一任务不能重复并发执行")
	}
	if len(m.busy) >= 2 {
		return run, errors.New("已有两个任务运行，请稍后重试")
	}
	if kind != "backup" && kind != "preview" && kind != "restore" {
		return run, errors.New("invalid operation")
	}
	s := clone(m.state)
	index := -1
	for i, t := range s.Tasks {
		if t.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return run, errors.New("task not found")
	}
	t := s.Tasks[index]
	if t.ReadOnly && kind != "restore" {
		return run, errors.New("导入的仓库只允许恢复")
	}
	if kind == "restore" && (snapshotID == "" || destination == "") {
		return run, errors.New("restore selection required")
	}
	if trigger == "schedule" {
		next, err := NextRun(t.Schedule, now)
		if err != nil {
			return run, err
		}
		s.Tasks[index].Next = next
	}
	run = Run{ID: backup.NewID(), TaskID: id, TaskName: t.Name, Kind: kind, Trigger: trigger, Status: "running", Started: now}
	s.Runs = append([]Run{run}, s.Runs...)
	prune(&s)
	if err := m.commit(s); err != nil {
		return Run{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	if len(m.busy) == 0 {
		m.idle = make(chan struct{})
	}
	m.busy[id] = job{cancel: cancel, runID: run.ID}
	go m.execute(ctx, t, run, snapshotID, destination)
	return run, nil
}

func (m *Manager) execute(ctx context.Context, t Task, run Run, snapshotID, destination string) {
	var result backup.Result
	var preview *backup.Preview
	var err error
	attempts := 0
	progress := func(p backup.Progress) {
		m.mu.Lock()
		defer m.mu.Unlock()
		for i := range m.state.Runs {
			if m.state.Runs[i].ID == run.ID {
				m.state.Runs[i].Progress = p
				m.state.Runs[i].Attempts = attempts
				break
			}
		}
	}
	for attempts = 1; attempts <= t.Retries+1; attempts++ {
		if err = ctx.Err(); err != nil {
			break
		}
		switch run.Kind {
		case "preview":
			var p backup.Preview
			p, err = m.executor.Preview(ctx, t.Selection)
			preview = &p
		case "backup":
			result, err = m.executor.Backup(ctx, t.Selection, progress)
		case "restore":
			result, err = m.executor.Restore(ctx, t.Selection, snapshotID, destination, progress)
		}
		// Restore targets are exclusive: automatic retry would reuse a partial dir.
		if err == nil || run.Kind != "backup" || errors.Is(err, context.Canceled) || errors.Is(err, backup.ErrMountLost) || attempts > t.Retries {
			break
		}
		timer := time.NewTimer(time.Duration(attempts) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			err = ctx.Err()
		case <-timer.C:
		}
		if ctx.Err() != nil {
			break
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := clone(m.state)
	for i := range s.Runs {
		if s.Runs[i].ID == run.ID {
			r := &s.Runs[i]
			r.Ended = time.Now().UTC()
			r.Attempts = attempts
			r.Result = result
			r.Preview = preview
			if err != nil {
				r.Status = "failed"
				r.Error = err.Error()
				if len(r.Error) > 2048 {
					r.Error = r.Error[:2048]
				}
				if errors.Is(err, context.Canceled) {
					r.Status = "canceled"
				}
			} else if run.Kind == "preview" {
				r.Status = "previewed"
			} else {
				r.Status = result.State
			}
			break
		}
	}
	prune(&s)
	if saveErr := m.commit(s); saveErr != nil {
		// Keep visible outcome, but do not claim the run record was durably saved.
		m.state = s
		for i := range m.state.Runs {
			if m.state.Runs[i].ID == run.ID {
				m.state.Runs[i].Status = "record_error"
				m.state.Runs[i].Error = "执行已结束，但运行记录保存失败；重启后需检查快照。"
			}
		}
	}
	m.busy[t.ID].cancel()
	delete(m.busy, t.ID)
	if len(m.busy) == 0 {
		close(m.idle)
	}
}

func (m *Manager) Cancel(runID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.busy {
		if j.runID == runID {
			s := clone(m.state)
			for i := range s.Runs {
				if s.Runs[i].ID == runID {
					s.Runs[i].Status = "cancel_requested"
				}
			}
			err := m.commit(s)
			j.cancel()
			return err
		}
	}
	return errors.New("任务已结束或不存在")
}

func (m *Manager) Tick(now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	// At most one overdue execution; advance from now, not the historic due time.
	for _, t := range append([]Task(nil), m.state.Tasks...) {
		if !t.Schedule.Enabled || t.ReadOnly || t.Next.IsZero() || t.Next.After(now) {
			continue
		}
		if _, busy := m.busy[t.ID]; busy || len(m.busy) >= 2 {
			continue
		}
		if _, err := m.startLocked(t.ID, "backup", "schedule", "", "", now); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) Wait(ctx context.Context) error {
	m.mu.Lock()
	idle := m.idle
	m.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	for _, j := range m.busy {
		j.cancel()
	}
	m.mu.Unlock()
	if err := m.Wait(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lock != nil {
		err := m.lock.Close()
		m.lock = nil
		return err
	}
	return nil
}
