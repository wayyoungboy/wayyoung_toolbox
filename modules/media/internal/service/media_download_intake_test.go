package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func registeredRSSDownload(t *testing.T, f *intakeQueueFixture) MediaJob {
	t.Helper()
	hash := "0123456789abcdef0123456789abcdef01234567"
	f.torrentHash.Store(&hash)
	job, err := f.s.db.EnqueueRSSAcquisition(Task{ID: 41, Name: "fixture"}, FeedEntry{EntryID: "entry", Title: "TEST-001"})
	if err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.ClaimMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.ReserveRSSDownload(job.ID, job.Version, "magnet:?xt=urn:btih:"+hash)
	if err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.ObserveRSSDownload(job.ID, job.Version, QBTorrent{Hash: hash, AddedOn: f.addedOn.Load(), ContentPath: f.source, Progress: .5, AmountLeft: 100, State: "downloading"})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestIntakeContinuesRegisteredDownloadFromOldRootAfterSettingsChange(t *testing.T) {
	f := newIntakeQueueFixture(t)
	config, err := f.s.db.IntakeConfig()
	if err != nil {
		t.Fatal(err)
	}
	job := registeredRSSDownload(t, f)
	if job.IntakeSourceRoot != config.SourceDirectory {
		t.Fatalf("RSS discovery did not retain intake source boundary: %+v", job)
	}
	writeIntakeFixture(t, filepath.Dir(f.source), map[string]string{"UNSELECTED-002.mp4": "leave alone"})
	config.SourceDirectory, config.DestinationDirectory = t.TempDir(), t.TempDir()
	saved, err := f.s.db.UpdateIntakeConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	restartIntakeQueueFixture(t, f)
	f.incomplete.Store(true)
	preview, err := f.s.previewIntake(context.Background())
	if err != nil || len(preview.Candidates) != 0 {
		t.Fatalf("unfinished download accepted: %+v %v", preview, err)
	}
	f.incomplete.Store(false)
	preview, err = f.s.previewIntake(context.Background())
	if err != nil || len(preview.Candidates) != 1 || preview.Candidates[0].JobID != job.ID {
		t.Fatalf("old download missing from preview: %+v %v", preview, err)
	}
	if f.stops.Load() != 0 || f.deletes.Load() != 0 {
		t.Fatal("preview performed a qB action")
	}
	result, err := f.s.runIntake(context.Background(), "manual")
	if err != nil || result.Moved != 1 || result.TasksDeleted != 1 {
		t.Fatalf("old download did not continue: %+v %v", result, err)
	}
	current, err := f.s.db.MediaJob(job.ID)
	if err != nil || current.Status != "success" || current.TargetPath != filepath.Join(saved.DestinationDirectory, "TEST-001.mp4") || current.IntakeSourceRoot != job.IntakeSourceRoot {
		t.Fatalf("lost original queue identity/boundary: %+v %v", current, err)
	}
	if _, err := os.Stat(f.source); !os.IsNotExist(err) {
		t.Fatal("source still present", err)
	}
	assertIntakeFixture(t, filepath.Dir(f.source), map[string]string{"UNSELECTED-002.mp4": "leave alone"})
}

func TestOldDownloadBoundaryCannotAuthorizeOtherOrCanceledTorrent(t *testing.T) {
	for _, scenario := range []string{"canceled", "replaced", "outside"} {
		t.Run(scenario, func(t *testing.T) {
			f := newIntakeQueueFixture(t)
			job := registeredRSSDownload(t, f)
			config, err := f.s.db.IntakeConfig()
			if err != nil {
				t.Fatal(err)
			}
			config.SourceDirectory = t.TempDir()
			if _, err := f.s.db.UpdateIntakeConfig(config); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "canceled":
				if _, err := f.s.db.CancelMediaJob(job.ID, job.Version); err != nil {
					t.Fatal(err)
				}
			case "replaced":
				f.addedOn.Add(1)
			case "outside":
				outside := filepath.Join(t.TempDir(), "OTHER-002.mp4")
				f.reportedPath.Store(&outside)
			}
			result, err := f.s.runIntake(context.Background(), "manual")
			if err != nil || len(result.Candidates) != 0 || f.stops.Load() != 0 || f.deletes.Load() != 0 {
				t.Fatalf("wrong torrent authorized: %+v %v", result, err)
			}
			assertIntakeFixture(t, filepath.Dir(f.source), map[string]string{"TEST-001.mp4": "video"})
		})
	}
}

func TestIntakeOldRootContinuesWhenNewSourceDisappears(t *testing.T) {
	f := newIntakeQueueFixture(t)
	job := registeredRSSDownload(t, f)
	config, err := f.s.db.IntakeConfig()
	if err != nil {
		t.Fatal(err)
	}
	config.SourceDirectory = t.TempDir()
	if _, err := f.s.db.UpdateIntakeConfig(config); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(config.SourceDirectory); err != nil {
		t.Fatal(err)
	}
	preview, err := f.s.previewIntake(context.Background())
	if err != nil || len(preview.Candidates) != 1 || preview.Candidates[0].JobID != job.ID || len(preview.Errors) != 1 {
		t.Fatalf("unavailable new root blocked healthy tracked root or hid failure: %+v %v", preview, err)
	}
	if f.stops.Load() != 0 || f.deletes.Load() != 0 {
		t.Fatal("preview changed qB")
	}
	result, err := f.s.runIntake(context.Background(), "manual")
	if err != nil || result.Moved != 1 || result.TasksDeleted != 1 || result.Failed != 1 || len(result.Errors) != 1 {
		t.Fatalf("independent root did not complete with explicit partial result: %+v %v", result, err)
	}
	current, err := f.s.db.MediaJob(job.ID)
	if err != nil || current.Status != "success" {
		t.Fatalf("tracked download did not complete: %+v %v", current, err)
	}
}

func TestRetryDownloadResumesScheduledIntakeWithoutResubmitting(t *testing.T) {
	f := newIntakeQueueFixture(t)
	job := registeredRSSDownload(t, f)
	canceled, err := f.s.db.CancelMediaJob(job.ID, job.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.runIntake(context.Background(), "schedule"); err != nil {
		t.Fatal(err)
	}
	if f.stops.Load() != 0 || f.deletes.Load() != 0 {
		t.Fatal("canceled download moved")
	}
	if _, err := f.s.db.RetryMediaJob(canceled.ID, canceled.Version); err != nil {
		t.Fatal(err)
	}
	result, err := f.s.runIntake(context.Background(), "schedule")
	if err != nil || result.Moved != 1 || result.TasksDeleted != 1 {
		t.Fatalf("retry did not resume intake: %+v %v", result, err)
	}
	current, err := f.s.db.MediaJob(job.ID)
	if err != nil || current.Status != "success" {
		t.Fatalf("original download did not continue: %+v %v", current, err)
	}
	if f.adds.Load() != 0 {
		t.Fatal("resuming intake resubmitted a torrent")
	}
}
