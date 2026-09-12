package service

import (
	"context"
	"encoding/json"
	"testing"
)

func submittedRSSPath(t *testing.T, f *rssExecutionFixture) string {
	t.Helper()
	select {
	case path := <-f.savePaths:
		return path
	default:
		t.Fatal("no download submission was captured")
		return ""
	}
}

func setRSSDownloadDirectory(t *testing.T, d *Database, path string) {
	t.Helper()
	if _, err := d.db.Exec(`UPDATE settings SET value=? WHERE key='intake_source_directory'`, encodeJSON(path)); err != nil {
		t.Fatal(err)
	}
}

func TestRSSDownloadPathSnapshotSurvivesChangesRestartAndRetry(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "shared-default", true: "subscription-override"}[explicit], func(t *testing.T) {
			f := newRSSExecutionFixture(t)
			setRSSDownloadDirectory(t, f.s.db, "/media/downloads")
			want := "/media/downloads"
			if explicit {
				want = "/other/subscription"
				f.task.SavePath = want
			}
			var err error
			f.task, err = f.s.db.UpdateTask(f.task.ID, f.task)
			if err != nil {
				t.Fatal(err)
			}
			entry := f.job.Discovery.Entry
			entry.EntryID = "path-snapshot-entry"
			f.job, err = f.s.db.EnqueueRSSAcquisition(f.task, entry)
			if err != nil {
				t.Fatal(err)
			}
			// Inspect the durable payload rather than requiring a new Go field.
			var payload string
			if err := f.s.db.db.QueryRow(`SELECT payload FROM media_jobs WHERE id=?`, f.job.ID).Scan(&payload); err != nil {
				t.Fatal(err)
			}
			var saved struct {
				Discovery struct {
					DownloadPath *string `json:"download_path"`
				} `json:"discovery"`
			}
			if err := json.Unmarshal([]byte(payload), &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Discovery.DownloadPath == nil || *saved.Discovery.DownloadPath != want {
				t.Fatalf("download destination not captured with queue job: %s", payload)
			}
			f.task.SavePath = "/changed/subscription"
			if _, err := f.s.db.UpdateTask(f.task.ID, f.task); err != nil {
				t.Fatal(err)
			}
			setRSSDownloadDirectory(t, f.s.db, "/changed/default")
			// Rediscovering the same RSS entry must not replace its snapshot.
			if _, err := f.s.db.EnqueueRSSAcquisition(f.task, entry); err != nil {
				t.Fatal(err)
			}
			f.reopen(t)
			f.reject.Store(true)
			job, _ := f.run(t)
			if job.Status != "failed" || submittedRSSPath(t, f) != want {
				t.Fatalf("first attempt lost path: %+v", job)
			}
			if _, err := f.s.db.RetryMediaJob(job.ID, job.Version); err != nil {
				t.Fatal(err)
			}
			f.reject.Store(false)
			f.reopen(t)
			job, _ = f.run(t)
			if job.Phase != "download" || submittedRSSPath(t, f) != want || f.adds.Load() != 2 {
				t.Fatalf("retry changed path: %+v", job)
			}
		})
	}
}

func TestRSSLegacyJobDoesNotAdoptNewSharedDirectory(t *testing.T) {
	for _, explicit := range []string{"", "/legacy/subscription"} {
		t.Run("path="+explicit, func(t *testing.T) {
			f := newRSSExecutionFixture(t)
			// Simulate a pre-snapshot queue payload without changing its phase/claim.
			if _, err := f.s.db.db.Exec(`UPDATE media_jobs SET payload=json_remove(payload,'$.discovery.download_path') WHERE id=?`, f.job.ID); err != nil {
				t.Fatal(err)
			}
			setRSSDownloadDirectory(t, f.s.db, "/new/shared")
			f.task.SavePath = explicit
			if _, err := f.s.db.UpdateTask(f.task.ID, f.task); err != nil {
				t.Fatal(err)
			}
			f.reopen(t)
			job, _ := f.run(t)
			if job.Phase != "download" || submittedRSSPath(t, f) != explicit {
				t.Fatalf("legacy default path changed: %+v", job)
			}
		})
	}
}

func TestRSSEmptyDownloadSnapshotDoesNotAdoptLaterDirectory(t *testing.T) {
	f := newRSSExecutionFixture(t)
	setRSSDownloadDirectory(t, f.s.db, "")
	entry := f.job.Discovery.Entry
	entry.EntryID = "empty-snapshot"
	var err error
	f.job, err = f.s.db.EnqueueRSSAcquisition(f.task, entry)
	if err != nil {
		t.Fatal(err)
	}
	if f.job.Discovery.DownloadPath == nil || *f.job.Discovery.DownloadPath != "" {
		t.Fatal("empty snapshot was lost")
	}
	setRSSDownloadDirectory(t, f.s.db, "/later/shared")
	f.task.SavePath = "/later/subscription"
	if _, err := f.s.db.UpdateTask(f.task.ID, f.task); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	job, _ := f.run(t)
	if job.Phase != "download" || submittedRSSPath(t, f) != "" {
		t.Fatalf("empty default reinterpreted: %+v", job)
	}
}

func TestRSSDiscoveryUsesCommittedSharedDirectoryOnlyForNewEntries(t *testing.T) {
	s, paths := setupMediaPathSettings(t)
	task, err := s.db.CreateTask(Task{Name: "Shared directory", FeedURL: "https://example.test/feed", ScheduleType: "interval", ScheduleMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	entry := FeedEntry{EntryID: "first", Title: "TEST-001"}
	first, err := s.db.EnqueueRSSAcquisition(task, entry)
	if err != nil {
		t.Fatal(err)
	}
	paths.DownloadDirectory = t.TempDir()
	saved, err := s.db.UpdateMediaPathSettings(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.db.EnqueueRSSAcquisition(task, entry)
	if err != nil {
		t.Fatal(err)
	}
	entry.EntryID = "second"
	second, err := s.db.EnqueueRSSAcquisition(task, entry)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != again.ID || *first.Discovery.DownloadPath != *again.Discovery.DownloadPath || *second.Discovery.DownloadPath != saved.DownloadDirectory || *first.Discovery.DownloadPath == *second.Discovery.DownloadPath {
		t.Fatal("shared save rewrote old job or did not affect new discovery")
	}
	stored, err := s.db.Task(task.ID)
	if err != nil || stored.SavePath != "" {
		t.Fatal("shared default rewrote subscription override", err)
	}
}

func TestPipelinePathsIncludesSharedDefaultAndPreservesOverrides(t *testing.T) {
	intake := IntakeConfig{SourceDirectory: "/media/downloads", DestinationDirectory: "/media/pending"}
	javsp := JavspConfig{InputDirectory: "/media/pending"}
	paths := buildPipelinePaths([]Task{{Name: "default"}}, intake, javsp)
	if len(paths.RSSDownloadPaths) != 1 || paths.RSSDownloadPaths[0] != intake.SourceDirectory || len(paths.Warnings) != 0 || !paths.Consistent {
		t.Fatalf("default RSS path not represented: %+v", paths)
	}
	paths = buildPipelinePaths([]Task{{Name: "default"}, {Name: "override", SavePath: "/other/downloads"}}, intake, javsp)
	if len(paths.RSSDownloadPaths) != 2 || paths.Consistent || len(paths.Issues) != 1 {
		t.Fatalf("override mismatch hidden: %+v", paths)
	}
}
