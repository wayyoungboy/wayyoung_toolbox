package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func separateMovieOutput(t *testing.T, f *movieQueueFixture) JavspConfig {
	t.Helper()
	output, err := canonicalDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.target = filepath.Join(output, "TEST-001", "TEST-001.mp4")
	cfg, err := f.s.db.JavspConfigPrivate()
	if err != nil {
		t.Fatal(err)
	}
	cfg.OutputDirectory, cfg.OutputFolderPattern = output, "{num}"
	if _, err := f.s.db.UpdateJavspConfig(cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestMovieQueueSeparateOutputMovesAndResumesWithStoredRoots(t *testing.T) {
	for _, hardLink := range []bool{false, true} {
		t.Run(map[bool]string{false: "move", true: "hardlink"}[hardLink], func(t *testing.T) {
			f := newMovieQueueFixture(t)
			cfg := separateMovieOutput(t, f)
			f.hardLink, f.afterMoveError = hardLink, context.DeadlineExceeded
			before, err := os.Lstat(f.source)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.s.drainPendingMovies(context.Background()); err != nil {
				t.Fatal(err)
			}
			job, err := f.s.db.MediaJob(f.job.ID)
			if err != nil || job.Status != "timed_out" || job.MoviePlan == nil || !job.MoviePlan.MetadataReady || job.MoviePlan.Root != cfg.OutputDirectory {
				t.Fatalf("separate-root move did not checkpoint: %+v %v", job, err)
			}
			after, err := os.Lstat(f.target)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("target is not the queued file: %v", err)
			}
			_, err = os.Stat(f.source)
			if hardLink && err != nil || !hardLink && !os.IsNotExist(err) {
				t.Fatalf("wrong source disposition (hardlink=%t): %v", hardLink, err)
			}
			// A saved plan must not be reinterpreted using newly selected roots.
			cfg.InputDirectory, cfg.OutputDirectory = t.TempDir(), t.TempDir()
			if _, err := f.s.db.UpdateJavspConfig(cfg); err != nil {
				t.Fatal(err)
			}
			job, err = f.s.db.RetryMediaJob(job.ID, job.Version)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.db.ClaimMediaJob(job.ID, job.Version); err != nil {
				t.Fatal(err)
			}
			var seq int
			var schema, path string
			if err := f.s.db.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &schema, &path); err != nil {
				t.Fatal(err)
			}
			if err := f.s.db.Close(); err != nil {
				t.Fatal(err)
			}
			f.s.db, err = OpenDatabase(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = f.s.db.Close() })
			if err := f.s.drainPendingMovies(context.Background()); err != nil {
				t.Fatal(err)
			}
			job, err = f.s.db.MediaJob(job.ID)
			if err != nil || job.Status != "success" || f.runs != 1 {
				t.Fatalf("recovery lost original roots or recrawled: %+v runs=%d %v", job, f.runs, err)
			}
			assertIntakeFixture(t, filepath.Dir(f.target), map[string]string{"TEST-001.nfo": f.nfo, "TEST-001.mp4": "video"})
		})
	}
}

func TestMovieQueueSeparateOutputRejectsCollisionsAndEscapes(t *testing.T) {
	for _, scenario := range []string{"collision", "symlink", "outside output", "outside source"} {
		t.Run(scenario, func(t *testing.T) {
			f := newMovieQueueFixture(t)
			cfg := separateMovieOutput(t, f)
			outside := t.TempDir()
			switch scenario {
			case "collision":
				writeIntakeFixture(t, filepath.Dir(f.target), map[string]string{"TEST-001.mp4": "unrelated"})
			case "symlink":
				if err := os.Symlink(outside, filepath.Dir(f.target)); err != nil {
					t.Fatal(err)
				}
			case "outside output":
				f.target = filepath.Join(outside, "TEST-001.mp4")
			case "outside source":
				cfg.InputDirectory = outside
				if _, err := f.s.db.UpdateJavspConfig(cfg); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.s.drainPendingMovies(context.Background()); err != nil {
				t.Fatal(err)
			}
			job, err := f.s.db.MediaJob(f.job.ID)
			if err != nil || job.Status != "needs_attention" {
				t.Fatalf("unsafe plan accepted: %+v %v", job, err)
			}
			assertIntakeFixture(t, filepath.Dir(f.source), map[string]string{"TEST-001.mp4": "video"})
			if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
				t.Fatalf("wrote outside selected output: %v %v", entries, err)
			}
			if scenario == "collision" {
				assertIntakeFixture(t, filepath.Dir(f.target), map[string]string{"TEST-001.mp4": "unrelated"})
			}
		})
	}
}

func TestDiscoveredMovieKeepsSourceRootAfterConfigChanges(t *testing.T) {
	for _, stage := range []string{"pending", "intention", "legacy intention"} {
		t.Run(stage, func(t *testing.T) {
			f := newMovieQueueFixture(t)
			cfg := separateMovieOutput(t, f)
			candidate, err := movieDiscoveryJob(context.Background(), f.root, "TEST-001", []string{f.source}, 1)
			if err != nil || candidate.MovieSourceRoot != f.root {
				t.Fatalf("discovery did not bind source root: %+v %v", candidate, err)
			}
			job, err := f.s.db.changeMediaJob(f.job.ID, f.job.Version, "discovered", func(job *MediaJob) error {
				job.SourcePaths, job.SourceIdentities = candidate.SourcePaths, candidate.SourceIdentities
				job.MovieSourceRoot = candidate.MovieSourceRoot
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if stage != "pending" {
				f.afterPlanError = context.DeadlineExceeded
				if err := f.s.drainPendingMovies(context.Background()); err != nil {
					t.Fatal(err)
				}
				job, err = f.s.db.MediaJob(job.ID)
				if err != nil || job.MoviePlan == nil || job.MoviePlan.MetadataReady || job.Status != "timed_out" {
					t.Fatalf("missing intention: %+v %v", job, err)
				}
				if stage == "legacy intention" {
					job, err = f.s.db.changeMediaJob(job.ID, job.Version, "legacy_fixture", func(job *MediaJob) error {
						job.MovieSourceRoot = ""
						return nil
					})
					if err != nil {
						t.Fatal(err)
					}
				}
				if _, err := f.s.db.RetryMediaJob(job.ID, job.Version); err != nil {
					t.Fatal(err)
				}
				f.afterPlanError = nil
			}
			cfg.InputDirectory = t.TempDir()
			if _, err := f.s.db.UpdateJavspConfig(cfg); err != nil {
				t.Fatal(err)
			}
			if err := f.s.drainPendingMovies(context.Background()); err != nil {
				t.Fatal(err)
			}
			job, err = f.s.db.MediaJob(job.ID)
			if err != nil || job.Status != "success" || job.MoviePlan == nil || job.MoviePlan.SourceRoot != f.root {
				t.Fatalf("config change stranded queued input: %+v %v", job, err)
			}
			current, err := f.s.db.JavspConfigPrivate()
			if err != nil || current.InputDirectory != cfg.InputDirectory {
				t.Fatal("running old job reverted current settings")
			}
		})
	}
}
