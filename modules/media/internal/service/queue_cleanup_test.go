package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestQueueCleanupArchivesOnlyConfirmedInactiveVersions(t *testing.T) {
	s := testService(t)
	failed := enqueueQueueTestJob(t, s.db, "old-failed")
	if _, err := s.db.db.Exec("UPDATE media_jobs SET status='failed' WHERE id=?", failed.ID); err != nil {
		t.Fatal(err)
	}
	active := enqueueQueueTestJob(t, s.db, "active")
	body := map[string]any{"jobs": []map[string]int64{{"id": failed.ID, "version": failed.Version}, {"id": active.ID, "version": active.Version}}}
	if w := catalogRequest(t, s, "POST", "/api/queue/cleanup", body); w.Code != 409 {
		t.Fatalf("active cleanup: %d %s", w.Code, w.Body.String())
	}
	if page, err := s.db.MediaQueuePage("failed", 0, 10); err != nil || page.Total != 1 {
		t.Fatalf("non-atomic cleanup: %+v %v", page, err)
	}
	body = map[string]any{"jobs": []map[string]int64{{"id": failed.ID, "version": failed.Version}}}
	if w := catalogRequest(t, s, "POST", "/api/queue/cleanup", body); w.Code != 200 {
		t.Fatalf("cleanup: %d %s", w.Code, w.Body.String())
	}
	if page, err := s.db.MediaQueuePage("failed", 0, 10); err != nil || page.Total != 0 {
		t.Fatalf("attention still includes archive: %+v %v", page, err)
	}
	if page, err := s.db.MediaQueuePage("archived", 0, 10); err != nil || page.Total != 1 {
		t.Fatalf("archive missing: %+v %v", page, err)
	}
	preserved, err := s.db.MediaJob(failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.RetryMediaJob(preserved.ID, preserved.Version); err == nil {
		t.Fatal("archived task restarted")
	}
	same, err := s.db.EnqueueMediaJob(MediaJob{Key: failed.Key, Kind: failed.Kind, Phase: failed.Phase, SourcePath: failed.SourcePath})
	if err != nil || same.ID != failed.ID {
		t.Fatalf("dedup lost: %+v %v", same, err)
	}
	if w := catalogRequest(t, s, "POST", "/api/queue/cleanup", body); w.Code != 409 {
		t.Fatalf("stale cleanup replay: %d", w.Code)
	}
	events, err := s.db.MediaJobEvents(failed.ID)
	if err != nil || len(events) < 2 || events[len(events)-1].Reason != "archived" {
		t.Fatalf("archive not auditable: %+v %v", events, err)
	}
}

func TestQueueCleanupStopsAutomaticRSSReconciliation(t *testing.T) {
	f := newRSSExecutionFixture(t)
	f.failLookupAfterAdd.Store(true)
	job, _ := f.run(t)
	if job.Status != "needs_attention" {
		t.Fatalf("fixture: %+v", job)
	}
	payload := map[string]any{"jobs": []map[string]int64{{"id": job.ID, "version": job.Version}}}
	if w := catalogRequest(t, f.s, "POST", "/api/queue/cleanup", payload); w.Code != 200 {
		t.Fatalf("cleanup: %d", w.Code)
	}
	before := f.queries.Load()
	if err := f.s.reconcileRSSSubmissions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if before != f.queries.Load() {
		t.Fatal("archived task still makes qB requests")
	}
}

func TestQueueCleanupCandidatesAreAgeBoundedAndExcludeHeldAndActive(t *testing.T) {
	s := testService(t)
	for _, scenario := range []string{"old", "recent", "held", "active"} {
		job := enqueueQueueTestJob(t, s.db, scenario)
		if scenario != "active" {
			if _, err := s.db.db.Exec(`UPDATE media_jobs SET status='failed' WHERE id=?`, job.ID); err != nil {
				t.Fatal(err)
			}
		}
		if scenario != "recent" {
			if _, err := s.db.db.Exec(`UPDATE media_jobs SET updated_at='2020-01-01T00:00:00Z' WHERE id=?`, job.ID); err != nil {
				t.Fatal(err)
			}
		}
		if scenario == "held" {
			if _, err := s.db.db.Exec(`UPDATE media_jobs SET payload=json_set(payload,'$.hold_files',json('true')) WHERE id=?`, job.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	w := catalogRequest(t, s, "GET", "/api/queue/cleanup?older_days=30", nil)
	var page MediaQueuePage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || page.Total != 1 || len(page.Jobs) != 1 || page.Jobs[0].Key != "old" {
		t.Fatalf("candidates: %d %+v", w.Code, page)
	}
}

func TestQueueCleanupRejectsHeldFilesAndUnvalidatedRequests(t *testing.T) {
	s := testService(t)
	job := enqueueQueueTestJob(t, s.db, "held")
	if _, err := s.db.db.Exec(`UPDATE media_jobs SET status='failed',payload=json_set(payload,'$.hold_files',json('true')) WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"jobs": []map[string]int64{{"id": job.ID, "version": job.Version}}}
	if w := catalogRequest(t, s, "POST", "/api/queue/cleanup", payload); w.Code != 409 {
		t.Fatalf("held file cleanup: %d", w.Code)
	}
	raw, _ := json.Marshal(payload)
	if w := queueAPIRequest(s, "POST", "/api/queue/cleanup", string(raw), true); w.Code != 403 {
		t.Fatalf("csrf: %d", w.Code)
	}
	for _, raw := range []string{`{"jobs":[]}`, fmt.Sprintf(`{"jobs":[{"id":%d,"version":0}]}`, job.ID)} {
		var input any
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			t.Fatal(err)
		}
		if w := catalogRequest(t, s, "POST", "/api/queue/cleanup", input); w.Code != 400 {
			t.Fatalf("validation: %d %s", w.Code, w.Body.String())
		}
	}
}
