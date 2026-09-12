package projectpath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveConfigExplicitPathWins(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "cwd")
	executable := filepath.Join(root, "bin", "javsp")
	explicit := filepath.Join(root, "custom.yml")
	writeConfig(t, filepath.Join(cwd, "config.yml"))
	writeConfig(t, explicit)

	got, err := ResolveConfig(explicit, cwd, executable)
	if err != nil {
		t.Fatalf("ResolveConfig returned error: %v", err)
	}
	if got != explicit {
		t.Fatalf("ResolveConfig = %q, want %q", got, explicit)
	}
}

func TestResolveConfigDiscoversDefaultInOrder(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name    string
		configs []string
		want    string
	}{
		{
			name: "current working directory",
			configs: []string{
				filepath.Join(root, "cwd", "config.yml"),
				filepath.Join(root, "bin", "config.yml"),
				filepath.Join(root, "config.yml"),
			},
			want: filepath.Join(root, "cwd", "config.yml"),
		},
		{
			name: "executable directory",
			configs: []string{
				filepath.Join(root, "bin", "config.yml"),
				filepath.Join(root, "config.yml"),
			},
			want: filepath.Join(root, "bin", "config.yml"),
		},
		{
			name: "executable parent directory",
			configs: []string{
				filepath.Join(root, "config.yml"),
			},
			want: filepath.Join(root, "config.yml"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caseRoot := t.TempDir()
			caseExecutable := filepath.Join(caseRoot, "bin", "javsp")
			caseCWD := filepath.Join(caseRoot, "cwd")
			for _, configPath := range tc.configs {
				rel, err := filepath.Rel(root, configPath)
				if err != nil {
					t.Fatal(err)
				}
				writeConfig(t, filepath.Join(caseRoot, rel))
			}
			wantRel, err := filepath.Rel(root, tc.want)
			if err != nil {
				t.Fatal(err)
			}

			got, err := ResolveConfig("", caseCWD, caseExecutable)
			if err != nil {
				t.Fatalf("ResolveConfig returned error: %v", err)
			}
			if want := filepath.Join(caseRoot, wantRel); got != want {
				t.Fatalf("ResolveConfig = %q, want %q", got, want)
			}
		})
	}
}

func TestResolveConfigMissingReportsAbsoluteCandidates(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "cwd")
	executable := filepath.Join(root, "bin", "javsp")

	_, err := ResolveConfig("", cwd, executable)
	if err == nil {
		t.Fatal("ResolveConfig returned nil error")
	}
	for _, want := range []string{
		filepath.Join(cwd, "config.yml"),
		filepath.Join(root, "bin", "config.yml"),
		filepath.Join(root, "config.yml"),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not report %q", err, want)
		}
	}
}

func TestResolveConfigRejectsNonRegularFile(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "config.yml")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveConfig(directory, root, filepath.Join(root, "javsp"))
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("ResolveConfig error = %v, want non-regular file error", err)
	}
}

func TestResourceRootReturnsAbsoluteConfigDirectory(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config", "config.yml")
	writeConfig(t, configPath)

	got, err := ResourceRoot(configPath)
	if err != nil {
		t.Fatalf("ResourceRoot returned error: %v", err)
	}
	if want := filepath.Dir(configPath); got != want {
		t.Fatalf("ResourceRoot = %q, want %q", got, want)
	}
}

func writeConfig(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("scanner: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
