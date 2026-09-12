package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestJavspExplicitOutputDirectoryPersistsAndFeedsEngine(t *testing.T) {
	s := testService(t)
	bundle, data := javspTestRuntime(t)
	s.javsp = NewJavspRunner(bundle, data)
	cfg := defaultJavspConfig()
	cfg.InputDirectory = t.TempDir()
	cfg.OutputDirectory = t.TempDir()
	cfg.OutputFolderPattern = "{year}/{num}"
	validated, err := validateJavspConfig(cfg)
	if err != nil {
		t.Fatalf("separate output directory rejected: %v", err)
	}
	w := javspConfigRequest(t, s, context.Background(), validated)
	if w.Code != 200 {
		t.Fatalf("save output directory: %d %s", w.Code, w.Body.String())
	}
	stored := readJavspHTTPConfig(t, s)
	if stored.OutputDirectory != validated.OutputDirectory || stored.OutputFolderPattern != cfg.OutputFolderPattern {
		t.Fatal("explicit output directory was not persisted")
	}
	raw, err := s.javsp.prepareConfigDataContext(context.Background(), stored, false)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Summarizer struct {
			Path struct {
				OutputFolderPattern string `yaml:"output_folder_pattern"`
			} `yaml:"path"`
		} `yaml:"summarizer"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Summarizer.Path.OutputFolderPattern != filepath.Join(stored.OutputDirectory, cfg.OutputFolderPattern) {
		t.Fatalf("engine still outputs under scan root: %q", doc.Summarizer.Path.OutputFolderPattern)
	}
	for _, resolve := range []func(JavspConfig) (string, bool, error){plannedJavspOutputRoot, staticJavspOutputRoot} {
		root, available, err := resolve(stored)
		if err != nil || !available || root != stored.OutputDirectory {
			t.Fatalf("wrong completed/discovery boundary: %q %t %v", root, available, err)
		}
	}
	writeIntakeFixture(t, stored.OutputDirectory, map[string]string{"2024/TEST-001/TEST-001.mp4": "complete"})
	index, err := buildCompletedMediaIndex(stored)
	if err != nil || !index.available {
		t.Fatalf("completed directory cannot be indexed: %v", err)
	}
	if _, found := index.ids["TEST-001"]; !found {
		t.Fatal("completed index ignored the explicit output directory")
	}
}

func TestJavspExplicitOutputDirectoryRejectsUnsafeBoundaries(t *testing.T) {
	parent := t.TempDir()
	input, output := filepath.Join(parent, "pending"), filepath.Join(parent, "completed")
	for _, dir := range []string{input, output} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(output, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct{ name, root, pattern string }{
		{"equal to input", input, "{num}"},
		{"contains input", parent, "{num}"},
		{"filesystem root", "/", "{num}"},
		{"relative root", "completed", "{num}"},
		{"missing root", filepath.Join(parent, "missing"), "{num}"},
		{"parent traversal", output + "/../completed", "{num}"},
		{"escaping template", output, "../{num}"},
		{"absolute template", output, filepath.Join(outside, "{num}")},
		{"symlink template", output, "escape/{num}"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			cfg := defaultJavspConfig()
			cfg.InputDirectory, cfg.OutputDirectory, cfg.OutputFolderPattern = input, scenario.root, scenario.pattern
			if _, err := validateJavspConfig(cfg); err == nil {
				t.Fatal("unsafe output boundary accepted")
			}
		})
	}
}

func TestJavspExplicitNestedOutputIsExcludedBeforeDiscovery(t *testing.T) {
	s := testService(t)
	bundle, data := javspTestRuntime(t)
	s.javsp = NewJavspRunner(bundle, data)
	cfg := defaultJavspConfig()
	cfg.InputDirectory, cfg.MinimumSize = t.TempDir(), "0B"
	cfg.OutputDirectory = filepath.Join(cfg.InputDirectory, "complete")
	cfg.OutputFolderPattern, cfg.IgnoredFolderPatterns = "{num}", nil
	writeIntakeFixture(t, cfg.InputDirectory, map[string]string{
		"TEST-001.mp4": "pending", "complete/TEST-002.mp4": "already complete",
	})
	validated, err := validateJavspConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.UpdateJavspConfig(validated); err != nil {
		t.Fatal(err)
	}
	if _, err := s.startJavsp(context.Background(), "manual"); err != nil {
		t.Fatal(err)
	}
	s.wait.Wait()
	jobs, err := s.db.ListMediaJobs("", 10)
	if err != nil || len(jobs) != 1 || jobs[0].MediaID != "TEST-001" {
		t.Fatalf("explicit completed root entered discovery: %+v %v", jobs, err)
	}
}

func TestJavspSeparateOutputDiscovery(t *testing.T) {
	for _, entry := range []string{"manual", "intake"} {
		t.Run(entry, func(t *testing.T) {
			s := testService(t)
			bundle, data := javspTestRuntime(t)
			s.javsp = NewJavspRunner(bundle, data)
			cfg := defaultJavspConfig()
			cfg.InputDirectory, cfg.OutputDirectory = t.TempDir(), t.TempDir()
			cfg.OutputFolderPattern, cfg.MinimumSize = "{num}", "0B"
			writeIntakeFixture(t, cfg.InputDirectory, map[string]string{"TEST-001.mp4": "pending", "TEST-002.mp4": "other pending"})
			writeIntakeFixture(t, cfg.OutputDirectory, map[string]string{"TEST-003.mp4": "complete"})
			cfg, err := validateJavspConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.UpdateJavspConfig(cfg); err != nil {
				t.Fatal(err)
			}
			var jobs []MediaJob
			want := 2
			if entry == "manual" {
				if _, err := s.startJavsp(context.Background(), "manual"); err != nil {
					t.Fatal(err)
				}
				s.wait.Wait()
				state := s.movieDiscoveryStatus()
				if state.Status != "success" {
					t.Fatalf("separate output broke manual discovery: %+v", state)
				}
				jobs, err = s.db.ListMediaJobs("", 10)
			} else {
				want = 1
				jobs, err = s.discoverIntakeMovies(context.Background(), MediaJob{ID: 12, IntakeTargetRoot: cfg.InputDirectory, PlannedPaths: []string{filepath.Join(cfg.InputDirectory, "TEST-001.mp4")}})
			}
			if err != nil || len(jobs) != want {
				t.Fatalf("separate output broke %s discovery: %+v %v", entry, jobs, err)
			}
			for _, job := range jobs {
				if job.MovieSourceRoot != cfg.InputDirectory || job.MediaID == "TEST-003" || entry == "intake" && (job.MediaID != "TEST-001" || job.ParentID != 12) {
					t.Fatalf("wrong discovery boundary: %+v", job)
				}
			}
			assertIntakeFixture(t, cfg.InputDirectory, map[string]string{"TEST-001.mp4": "pending", "TEST-002.mp4": "other pending"})
			assertIntakeFixture(t, cfg.OutputDirectory, map[string]string{"TEST-003.mp4": "complete"})
		})
	}
}
