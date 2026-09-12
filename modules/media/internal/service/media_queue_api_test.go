package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func queueAPIRequest(s *Service, method, path, body string, admin bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if admin {
		r.Header.Set("X-Trim-Isadmin", "true")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestMediaQueueAPIListDetailAndCursor(t *testing.T) {
	s := testService(t)
	first := enqueueQueueTestJob(t, s.db, "one")
	second := enqueueQueueTestJob(t, s.db, "two")
	third := enqueueQueueTestJob(t, s.db, "three")
	if w := queueAPIRequest(s, "GET", "/api/queue", "", false); w.Code != 403 {
		t.Fatalf("authorization=%d", w.Code)
	}
	w := queueAPIRequest(s, "GET", "/api/queue?limit=2", "", true)
	if w.Code != 200 {
		t.Fatalf("list=%d %s", w.Code, w.Body.String())
	}
	var page struct {
		Jobs       []MediaJob `json:"jobs"`
		NextCursor int64      `json:"next_cursor"`
		Total      int        `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Jobs) != 2 || page.Jobs[0].ID != third.ID || page.NextCursor != second.ID || page.Total != 3 {
		t.Fatalf("page=%+v", page)
	}
	w = queueAPIRequest(s, "GET", fmt.Sprintf("/api/queue?limit=2&before_id=%d", page.NextCursor), "", true)
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Jobs) != 1 || page.Jobs[0].ID != first.ID || page.NextCursor != 0 {
		t.Fatalf("next=%+v", page)
	}
	w = queueAPIRequest(s, "GET", fmt.Sprintf("/api/queue/%d", first.ID), "", true)
	var detail struct {
		Job    MediaJob        `json:"job"`
		Events []MediaJobEvent `json:"events"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || detail.Job.SourceURL != first.SourceURL || len(detail.Events) != 1 {
		t.Fatalf("detail=%d %+v", w.Code, detail)
	}
}

func TestMediaQueueAPIExplainsIntakeWaitAndClearsItAfterPublication(t *testing.T) {
	s := testService(t)
	movie, err := s.db.EnqueueMediaJob(MediaJob{Key: "waiting:movie", Kind: "movie", Phase: "organize", SourcePath: "/pending/TEST-001.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.db.EnqueueMediaJob(MediaJob{Key: "waiting:intake", Kind: "torrent", Phase: "qb_cleanup", TargetPath: movie.SourcePath, Organize: true})
	if err != nil {
		t.Fatal(err)
	}
	type queueItem struct {
		ID      int64
		Waiting []int64 `json:"waiting_for_intake"`
	}
	for _, ready := range []bool{false, true} {
		if ready {
			parent, err = s.db.ClaimMediaJob(parent.ID, parent.Version)
			if err != nil {
				t.Fatal(err)
			}
			movie.ParentID = parent.ID
			if _, _, err := s.db.saveIntakeMovies(context.Background(), parent.ID, parent.Version, []MediaJob{movie}); err != nil {
				t.Fatal(err)
			}
		}
		var detail struct{ Job queueItem }
		w := queueAPIRequest(s, "GET", fmt.Sprintf("/api/queue/%d", movie.ID), "", true)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
		var page struct{ Jobs []queueItem }
		w = queueAPIRequest(s, "GET", "/api/queue", "", true)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		items := []queueItem{detail.Job}
		for _, item := range page.Jobs {
			if item.ID == movie.ID {
				items = append(items, item)
			}
		}
		if len(items) != 2 {
			t.Fatalf("missing movie in list/detail: %+v", items)
		}
		for _, item := range items {
			if !ready && (len(item.Waiting) != 1 || item.Waiting[0] != parent.ID) {
				t.Fatalf("queue hides waiting reason: %+v", item)
			}
			if ready && len(item.Waiting) != 0 {
				t.Fatalf("stale wait after publication: %+v", item)
			}
		}
	}
}

func TestMediaQueueAPIActionsRequireFreshRevision(t *testing.T) {
	s := testService(t)
	job := enqueueQueueTestJob(t, s.db, "actions")
	url := fmt.Sprintf("/api/queue/%d", job.ID)
	if w := queueAPIRequest(s, "POST", url+"/cancel", `{}`, true); w.Code != 400 {
		t.Fatalf("missing version=%d", w.Code)
	}
	w := queueAPIRequest(s, "POST", url+"/cancel", fmt.Sprintf(`{"version":%d}`, job.Version), true)
	if w.Code != 200 {
		t.Fatalf("cancel=%d %s", w.Code, w.Body.String())
	}
	if w := queueAPIRequest(s, "POST", url+"/retry", fmt.Sprintf(`{"version":%d}`, job.Version), true); w.Code != 409 {
		t.Fatalf("stale retry=%d", w.Code)
	}
	current, err := s.db.MediaJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	w = queueAPIRequest(s, "POST", url+"/retry", fmt.Sprintf(`{"version":%d}`, current.Version), true)
	if w.Code != 200 {
		t.Fatalf("retry=%d %s", w.Code, w.Body.String())
	}
	current, err = s.db.MediaJob(job.ID)
	if err != nil || current.Status != "pending" || current.Phase != "transfer" {
		t.Fatalf("wrong retry: %+v %v", current, err)
	}
	for _, path := range []string{"/api/queue/nope", "/api/queue/99999"} {
		w := queueAPIRequest(s, "GET", path, "", true)
		if w.Code != 400 && w.Code != 404 {
			t.Fatalf("invalid id=%d", w.Code)
		}
	}
	if w := queueAPIRequest(s, "GET", "/api/queue?status=unknown", "", true); w.Code != 400 {
		t.Fatalf("invalid filter=%d", w.Code)
	}
}

func TestMediaQueueShutdownRemainsResumable(t *testing.T) {
	f := newIntakeQueueFixture(t)
	f.s.mediaFiles.Lock()
	_, err := f.s.runIntake(context.Background(), "manual")
	f.s.mediaFiles.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := f.s.db.ListMediaJobs("pending", 10)
	if err != nil || len(jobs) != 1 {
		t.Fatal(err)
	}
	job, err := f.s.db.ClaimMediaJob(jobs[0].ID, jobs[0].Version)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.s.runCtx = ctx
	job, err = f.s.failQueuedIntake(job, context.Canceled, false, &IntakeRunResult{})
	if err != nil || job.Status != "pending" {
		t.Fatalf("shutdown permanently canceled work: %+v %v", job, err)
	}
}

func TestMediaQueueAPICancelStopsRegisteredWorker(t *testing.T) {
	s := testService(t)
	job := enqueueQueueTestJob(t, s.db, "cancel-running")
	job, err := s.db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	ctx, finish := s.mediaJobContext(context.Background(), job)
	defer finish()
	w := queueAPIRequest(s, "POST", fmt.Sprintf("/api/queue/%d/cancel", job.ID), fmt.Sprintf(`{"version":%d}`, job.Version), true)
	if w.Code != 200 {
		t.Fatalf("cancel=%d %s", w.Code, w.Body.String())
	}
	if ctx.Err() != context.Canceled {
		t.Fatal("persisted cancel did not stop active worker")
	}
	late, cleanup := s.mediaJobContext(context.Background(), job)
	defer cleanup()
	if late.Err() != context.Canceled {
		t.Fatal("worker registered after cancel is still active")
	}
}

func TestMediaQueueCancelSettlesCompletedFileStep(t *testing.T) {
	for _, moved := range []bool{false, true} {
		t.Run(fmt.Sprint(moved), func(t *testing.T) {
			f := newIntakeQueueFixture(t)
			f.s.mediaFiles.Lock()
			_, err := f.s.runIntake(context.Background(), "manual")
			f.s.mediaFiles.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			jobs, err := f.s.db.ListMediaJobs("pending", 10)
			if err != nil || len(jobs) != 1 {
				t.Fatal(err)
			}
			job, err := f.s.db.ClaimMediaJob(jobs[0].ID, jobs[0].Version)
			if err != nil {
				t.Fatal(err)
			}
			_, finish := f.s.mediaJobContext(context.Background(), job)
			if moved {
				_, err := moveIntakeCandidate(IntakeCandidate{JobID: job.ID, Source: job.SourcePath, Destination: job.TargetPath}, job.IntakeSourceRoot, job.IntakeTargetRoot)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(job.SourcePath); !os.IsNotExist(err) {
					t.Fatal("move fixture failed")
				}
			}
			w := queueAPIRequest(f.s, "POST", fmt.Sprintf("/api/queue/%d/cancel", job.ID), fmt.Sprintf(`{"version":%d}`, job.Version), true)
			if w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			finish()
			current, err := f.s.db.MediaJob(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			phase := "transfer"
			if moved {
				phase = "qb_cleanup"
			}
			if current.Status != "canceled" || current.HoldFiles || current.Phase != phase {
				t.Fatalf("cancel did not settle durable work: %+v", current)
			}
			if blocked, err := f.s.db.MediaFilesBlocked(); err != nil || blocked {
				t.Fatalf("canceled task froze writers: %v %v", blocked, err)
			}
		})
	}
}

func TestMediaQueueLateCancellationDoesNotStopNewAttempt(t *testing.T) {
	s := testService(t)
	job := enqueueQueueTestJob(t, s.db, "cancel-retry")
	job, err := s.db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	oldVersion := job.Version
	job, err = s.db.CancelMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.db.RetryMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	ctx, finish := s.mediaJobContext(context.Background(), job)
	defer finish()
	s.cancelMediaJobAttempt(job.ID, oldVersion)
	if ctx.Err() != nil {
		t.Fatal("old cancel stopped the newer attempt")
	}
	s.cancelMediaJobAttempt(job.ID, job.Version)
	if ctx.Err() != context.Canceled {
		t.Fatal("current attempt did not stop")
	}
}

func TestMediaQueueCanceledWorkSettlesAfterCrashOrRepeatedActions(t *testing.T) {
	for _, mode := range []string{"restart", "restart_moved", "restart_captured", "cancel_retry_cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := newIntakeQueueFixture(t)
			f.s.mediaFiles.Lock()
			_, err := f.s.runIntake(context.Background(), "manual")
			f.s.mediaFiles.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			jobs, err := f.s.db.ListMediaJobs("pending", 10)
			if err != nil || len(jobs) != 1 {
				t.Fatal(err)
			}
			job, err := f.s.db.ClaimMediaJob(jobs[0].ID, jobs[0].Version)
			if err != nil {
				t.Fatal(err)
			}
			_, finish := f.s.mediaJobContext(context.Background(), job)
			if mode == "restart_moved" {
				if _, err := moveIntakeCandidate(IntakeCandidate{JobID: job.ID, Source: job.SourcePath, Destination: job.TargetPath}, job.IntakeSourceRoot, job.IntakeTargetRoot); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "restart_captured" {
				captureQueueFixture(t, job)
			}
			job, err = f.s.db.CancelMediaJob(job.ID, job.Version)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "cancel_retry_cancel" {
				job, err = f.s.db.RetryMediaJob(job.ID, job.Version)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.s.db.CancelMediaJob(job.ID, job.Version); err != nil {
					t.Fatal(err)
				}
				finish()
			} else {
				var seq int
				var schema, path string
				if err := f.s.db.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &schema, &path); err != nil {
					t.Fatal(err)
				}
				if err := f.s.db.Close(); err != nil {
					t.Fatal(err)
				}
				restarted, err := NewService(filepath.Dir(path), t.TempDir(), t.TempDir(), "", true)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = restarted.db.Close() })
				f.s = restarted
			}
			current, err := f.s.db.MediaJob(job.ID)
			wantHold := mode == "restart_captured"
			phase := "transfer"
			if mode == "restart_moved" {
				phase = "qb_cleanup"
			}
			if err != nil || current.Status != "canceled" || current.HoldFiles != wantHold || current.Phase != phase {
				t.Fatalf("unsafe/stuck canceled state: %+v %v", current, err)
			}
		})
	}
}
