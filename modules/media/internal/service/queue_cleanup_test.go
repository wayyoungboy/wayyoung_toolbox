package service

import (
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
