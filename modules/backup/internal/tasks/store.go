package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"fnos-backup-center/internal/backup"
	"golang.org/x/sys/unix"
)

func load(dir string) (State, error) {
	s := State{Schema: 1, Retain: 100, Tasks: []Task{}, Runs: []Run{}}
	p := filepath.Join(dir, "state.json")
	st, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if !st.Mode().IsRegular() || st.Size() > 32<<20 {
		return s, errors.New("invalid state file")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("state parse: %w", err)
	}
	if s.Schema != 1 || s.Retain < 1 || s.Retain > 10000 || len(s.Tasks) > 100 {
		return s, errors.New("unsupported or invalid state")
	}
	ids := map[string]bool{}
	writers := map[string]bool{}
	for _, t := range s.Tasks {
		if t.ID == "" || ids[t.ID] {
			return s, errors.New("invalid task identity")
		}
		ids[t.ID] = true
		if err := t.Selection.Validate(!t.ReadOnly); err != nil {
			return s, err
		}
		if !t.ReadOnly {
			if writers[t.Selection.RepoID] {
				return s, errors.New("duplicate repository writer in persisted state")
			}
			writers[t.Selection.RepoID] = true
		}
		if _, err := NextRun(t.Schedule, time.Now()); err != nil {
			return s, err
		}
	}
	for i := range s.Runs {
		if active(s.Runs[i].Status) {
			s.Runs[i].Status = "interrupted"
			s.Runs[i].Ended = time.Now().UTC()
			s.Runs[i].Error = "应用重启，原任务未确认完成；请检查备份快照。"
		}
	}
	prune(&s)
	return s, nil
}

func persist(dir string, s State) (err error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".state-"+backup.NewID()+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, filepath.Join(dir, "state.json")); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func acquire(dir string) (*os.File, error) {
	fd, err := unix.Open(filepath.Join(dir, "service.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "service.lock")
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another backup service is already running")
	}
	return f, nil
}
