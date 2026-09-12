package tasks

import (
	"fnos-backup-center/internal/backup"
	"time"
)

type Task struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Selection backup.Selection `json:"selection"`
	Schedule  Schedule         `json:"schedule"`
	Retries   int              `json:"retries"`
	ReadOnly  bool             `json:"read_only"`
	Created   time.Time        `json:"created"`
	Next      time.Time        `json:"next"`
}
type Run struct {
	ID       string          `json:"id"`
	TaskID   string          `json:"task_id"`
	TaskName string          `json:"task_name"`
	Kind     string          `json:"kind"`
	Trigger  string          `json:"trigger"`
	Status   string          `json:"status"`
	Started  time.Time       `json:"started"`
	Ended    time.Time       `json:"ended"`
	Attempts int             `json:"attempts"`
	Progress backup.Progress `json:"progress"`
	Result   backup.Result   `json:"result"`
	Preview  *backup.Preview `json:"preview,omitempty"`
	Error    string          `json:"error,omitempty"`
}
type State struct {
	Schema int    `json:"schema"`
	Retain int    `json:"retain"`
	Tasks  []Task `json:"tasks"`
	Runs   []Run  `json:"runs"`
}

func active(status string) bool { return status == "running" || status == "cancel_requested" }
func prune(s *State) {
	kept := make([]Run, 0, len(s.Runs))
	done := 0
	for _, r := range s.Runs {
		if active(r.Status) {
			kept = append(kept, r)
			continue
		}
		if done < s.Retain {
			kept = append(kept, r)
			done++
		}
	}
	s.Runs = kept
}
