package service

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMovieQueuePreservesMultipartSelectionAndRejectsUnselectedInput(t *testing.T) {
	for _, unrelated := range []bool{false, true} {
		t.Run(map[bool]string{false: "multipart", true: "unselected"}[unrelated], func(t *testing.T) {
			f := newMovieQueueFixture(t)
			second := filepath.Join(filepath.Dir(f.source), "TEST-001.2.mp4")
			other := filepath.Join(filepath.Dir(f.source), "OTHER-002.mp4")
			writeIntakeFixture(t, filepath.Dir(f.source), map[string]string{filepath.Base(second): "second video", filepath.Base(other): "other video"})
			if _, err := f.s.db.changeMediaJob(f.job.ID, f.job.Version, "test_multipart", func(job *MediaJob) error {
				job.SourcePaths = []string{f.source, second}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			bundle, data := javspTestRuntime(t)
			f.s.javsp = newJavspRunnerWithEngine(bundle, data, selectedJavspFixture(func(ctx context.Context, options embeddedJavspOptions) (embeddedJavspResult, error) {
				if !reflect.DeepEqual(options.InputFiles, []string{f.source, second}) {
					t.Errorf("lost selected parts: %+v", options.InputFiles)
				}
				selectedSecond := second
				if unrelated {
					selectedSecond = other
				}
				plan := JavspMoviePlan{MediaID: "TEST-001", MoveFiles: true, NFOPath: filepath.Join(filepath.Dir(f.target), "movie.nfo"), Files: []JavspFileMove{
					{Source: f.source, Destination: f.target}, {Source: selectedSecond, Destination: filepath.Join(filepath.Dir(f.target), "TEST-001.2.mp4")},
				}}
				if err := options.Hooks.BeforeOrganize(ctx, plan); err != nil {
					return embeddedJavspResult{}, err
				}
				writeIntakeFixture(t, filepath.Dir(f.target), map[string]string{"movie.nfo": f.nfo})
				if _, err := options.Hooks.MoveFiles(ctx, plan); err != nil {
					return embeddedJavspResult{}, err
				}
				return embeddedJavspResult{Total: 1, Processed: 1}, nil
			}))
			if err := f.s.drainPendingMovies(context.Background()); err != nil {
				t.Fatal(err)
			}
			job, err := f.s.db.MediaJob(f.job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if unrelated {
				if job.Status != "needs_attention" {
					t.Fatalf("unselected input was accepted: %+v", job)
				}
				if _, err := os.Stat(f.target); !os.IsNotExist(err) {
					t.Fatal("conflicting plan wrote output")
				}
			} else {
				if job.Status != "success" || len(job.OutputPaths) != 2 {
					t.Fatalf("multipart film did not finish together: %+v", job)
				}
				for _, name := range []string{"TEST-001.nfo", "TEST-001.2.nfo"} {
					if _, err := os.Stat(filepath.Join(filepath.Dir(f.target), name)); err != nil {
						t.Fatal(err)
					}
				}
			}
			assertIntakeFixture(t, filepath.Dir(other), map[string]string{filepath.Base(other): "other video"})
		})
	}
}
