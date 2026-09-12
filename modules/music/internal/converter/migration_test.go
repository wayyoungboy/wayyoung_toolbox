package converter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestScanIncludesMP3OnlyWhenEnabledAndExcludesOutputTree(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	output := filepath.Join(source, "done")
	mustWrite(t, filepath.Join(source, "song.ncm"), "ncm")
	mustWrite(t, filepath.Join(source, "ready.MP3"), "ID3 original")
	mustWrite(t, filepath.Join(source, "album", "nested.mp3"), "ID3 nested")
	mustWrite(t, filepath.Join(output, "already.mp3"), "ID3 done")
	if err := os.Symlink(filepath.Join(source, "ready.MP3"), filepath.Join(source, "link.mp3")); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		options ScanOptions
		want    []string
	}{
		{"disabled", ScanOptions{SourceDir: source, Recursive: true}, []string{"song.ncm"}},
		{"enabled", ScanOptions{SourceDir: source, OutputDir: output, Recursive: true, MigrateMP3: true}, []string{"album/nested.mp3", "ready.MP3", "song.ncm"}},
		{"flat", ScanOptions{SourceDir: source, OutputDir: output, MigrateMP3: true}, []string{"ready.MP3", "song.ncm"}},
		{"same root", ScanOptions{SourceDir: source, OutputDir: source, MigrateMP3: true}, []string{"ready.MP3", "song.ncm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			items, err := Scan(context.Background(), test.options)
			if err != nil {
				t.Fatal(err)
			}
			assertRelatives(t, items, test.want)
		})
	}
}

func TestMP3MigrationPreservesBytesAndDoesNotCallConverter(t *testing.T) {
	t.Parallel()
	source, output := t.TempDir(), t.TempDir()
	input := filepath.Join(source, "album", "ready.MP3")
	mustWrite(t, input, "ID3 original tags and audio")
	options := RunOptions{SourceDir: source, OutputDir: output, PreserveTree: true, MigrateMP3: true, Conflict: ConflictSkip, Concurrency: 1}
	result := NewRunner(errorEngine{}).Run(context.Background(), options, []Item{{Source: input, Relative: "album/ready.MP3"}})[0]
	if result.Status != StatusCompleted || result.Format != "mp3" {
		t.Fatalf("result = %+v", result)
	}
	got, err := os.ReadFile(filepath.Join(output, "album", "ready.MP3"))
	if err != nil || string(got) != "ID3 original tags and audio" {
		t.Fatalf("output = %q, %v", got, err)
	}
	if _, err := os.Stat(input); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source should be moved: %v", err)
	}
}

func TestMP3MigrationConflictPoliciesAndSameFile(t *testing.T) {
	t.Parallel()
	for _, policy := range []ConflictPolicy{ConflictSkip, ConflictRename, ConflictOverwrite} {
		for _, sameFile := range []bool{false, true} {
			t.Run(string(policy)+map[bool]string{true: " same file", false: " conflict"}[sameFile], func(t *testing.T) {
				source, output := t.TempDir(), t.TempDir()
				input := filepath.Join(source, "ready.mp3")
				mustWrite(t, input, "ID3 original")
				if sameFile {
					output = source
				} else {
					mustWrite(t, filepath.Join(output, "ready.mp3"), "existing")
				}
				options := RunOptions{SourceDir: source, OutputDir: output, MigrateMP3: true, Conflict: policy, Concurrency: 1}
				result := NewRunner(errorEngine{}).Run(context.Background(), options, []Item{{Source: input, Relative: "ready.mp3"}})[0]
				if sameFile || policy == ConflictSkip {
					if result.Status != StatusSkipped {
						t.Fatalf("result = %+v", result)
					}
					got, err := os.ReadFile(input)
					if err != nil || string(got) != "ID3 original" {
						t.Fatalf("source changed: %q %v", got, err)
					}
				} else {
					if result.Status != StatusCompleted {
						t.Fatalf("result = %+v", result)
					}
					got, err := os.ReadFile(result.Output)
					if err != nil || string(got) != "ID3 original" {
						t.Fatalf("output = %q %v", got, err)
					}
					if policy == ConflictRename {
						got, _ := os.ReadFile(filepath.Join(output, "ready.mp3"))
						if string(got) != "existing" {
							t.Fatal("rename overwrote existing audio")
						}
					}
				}
			})
		}
	}
}

func TestMP3MigrationDisabledOrCanceledKeepsSource(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{false, true} {
		source, output := t.TempDir(), t.TempDir()
		input := filepath.Join(source, "ready.mp3")
		mustWrite(t, input, "ID3 original")
		ctx, cancel := context.WithCancel(context.Background())
		if enabled {
			cancel()
		}
		options := RunOptions{SourceDir: source, OutputDir: output, MigrateMP3: enabled, Conflict: ConflictSkip, Concurrency: 1}
		result := NewRunner(errorEngine{}).Run(ctx, options, []Item{{Source: input, Relative: "ready.mp3"}})[0]
		cancel()
		if result.Status != StatusFailed && result.Status != StatusCanceled {
			t.Fatalf("result = %+v", result)
		}
		if _, err := os.Stat(input); err != nil {
			t.Fatalf("lost source: %v", err)
		}
		entries, _ := os.ReadDir(output)
		if len(entries) != 0 {
			t.Fatal("disabled/canceled migration left output")
		}
	}
}

func TestMP3MigrationParallelNameCollisions(t *testing.T) {
	t.Parallel()
	source, output := t.TempDir(), t.TempDir()
	var items []Item
	for _, name := range []string{"a", "b", "c"} {
		path := filepath.Join(source, name, "same.mp3")
		mustWrite(t, path, "ID3 "+name)
		items = append(items, Item{Source: path, Relative: filepath.Join(name, "same.mp3")})
	}
	results := NewRunner(errorEngine{}).Run(context.Background(), RunOptions{SourceDir: source, OutputDir: output, MigrateMP3: true, Conflict: ConflictRename, Concurrency: 3}, items)
	var bodies []string
	for _, result := range results {
		if result.Status != StatusCompleted {
			t.Fatalf("result = %+v", result)
		}
		body, err := os.ReadFile(result.Output)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, string(body))
	}
	sort.Strings(bodies)
	if strings.Join(bodies, ",") != "ID3 a,ID3 b,ID3 c" {
		t.Fatalf("lost/overwritten content: %v", bodies)
	}
}

func TestMP3MigrationFailuresKeepOriginal(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"canceled", "blocked directory", "target directory", "source directory readonly"} {
		t.Run(scenario, func(t *testing.T) {
			if scenario == "source directory readonly" && os.Geteuid() == 0 {
				t.Skip("root bypasses directory permissions")
			}
			source, output := t.TempDir(), t.TempDir()
			input := filepath.Join(source, "ready.mp3")
			mustWrite(t, input, "ID3 original")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "canceled" {
				cancel()
			}
			if scenario == "blocked directory" {
				mustWrite(t, filepath.Join(output, "blocked"), "x")
				output = filepath.Join(output, "blocked", "subdir")
			}
			if scenario == "target directory" {
				if err := os.Mkdir(filepath.Join(output, "ready.mp3"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "source directory readonly" {
				if err := os.Chmod(source, 0o500); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(source, 0o700)
			}
			result := NewRunner(errorEngine{}).migrateMP3(ctx, RunOptions{Conflict: ConflictOverwrite}, Item{Source: input}, output)
			if result.Status != StatusFailed && result.Status != StatusCanceled {
				t.Fatalf("result = %+v", result)
			}
			body, err := os.ReadFile(input)
			if err != nil || string(body) != "ID3 original" {
				t.Fatalf("source lost or changed: %q %v", body, err)
			}
			if scenario == "source directory readonly" {
				body, err := os.ReadFile(result.Output)
				if err != nil || string(body) != "ID3 original" {
					t.Fatal("published destination lost after deletion failure")
				}
			}
		})
	}
}

func TestMP3CopyRejectsChangedSourceAndIOErrors(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"missing", "replaced", "resized", "closed output"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "sample.mp3")
			mustWrite(t, source, "ID3 original")
			before, err := os.Stat(source)
			if err != nil {
				t.Fatal(err)
			}
			staged, err := os.CreateTemp(root, "staged-")
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing":
				os.Remove(source)
			case "replaced":
				os.Rename(source, source+".old")
				mustWrite(t, source, "ID3 replaced")
			case "resized":
				mustWrite(t, source, "ID3 original with new data")
			case "closed output":
				staged.Close()
			}
			if err := copyMP3(context.Background(), source, staged, before); err == nil {
				t.Fatal("expected copy failure")
			}
			if scenario != "missing" {
				if _, err := os.Stat(source); err != nil {
					t.Fatal("copy error removed source")
				}
			}
		})
	}
	root := t.TempDir()
	source := filepath.Join(root, "same.mp3")
	mustWrite(t, source, "before")
	before, _ := os.Stat(source)
	mustWrite(t, source, "after-longer")
	if err := unchangedSource(source, before); err == nil {
		t.Fatal("changed source accepted")
	}
	if err := syncDirectory(filepath.Join(root, "missing")); err == nil {
		t.Fatal("expected sync error")
	}
}
