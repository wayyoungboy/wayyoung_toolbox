package converter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNCMEngineConvertsAndForwardsArtworkPolicy(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()
	file := &fakeNCMFile{output: filepath.Join(staging, "song.flac"), dumpOK: true, metadataOK: true}
	engine := newNCMEngine(func(source string) (ncmFile, error) {
		if source != "/music/song.ncm" {
			t.Fatalf("source = %q", source)
		}
		return file, nil
	})

	output, err := engine.Convert(context.Background(), "/music/song.ncm", staging, true)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if output != file.output {
		t.Fatalf("output = %q, want %q", output, file.output)
	}
	if !file.fetchRemoteArt {
		t.Fatal("remote artwork policy was not forwarded")
	}
}

func TestNCMEnginePreservesM4AWithoutUnsupportedMetadataWriter(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()
	file := &fakeNCMFile{
		output: filepath.Join(staging, "song.m4a"), dumpOK: true,
		metadataErr: errors.New("M4A metadata writer must not be called"),
	}
	engine := newNCMEngine(func(string) (ncmFile, error) { return file, nil })
	output, err := engine.Convert(context.Background(), "/music/song.ncm", staging, true)
	if err != nil {
		t.Fatalf("convert M4A: %v", err)
	}
	if output != file.output || file.metadataCalls != 0 {
		t.Fatalf("output = %q, metadata calls = %d", output, file.metadataCalls)
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "audio" {
		t.Fatalf("M4A output was altered: %q, %v", data, err)
	}
}

func TestNCMEngineReturnsUsefulStageErrors(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		factory ncmFactory
		want    string
	}{
		{
			name: "open",
			factory: func(string) (ncmFile, error) {
				return nil, errors.New("bad header")
			},
			want: "读取 NCM 文件失败",
		},
		{
			name: "dump",
			factory: func(string) (ncmFile, error) {
				return &fakeNCMFile{dumpErr: errors.New("disk full")}, nil
			},
			want: "解析音频失败",
		},
		{
			name: "metadata",
			factory: func(string) (ncmFile, error) {
				return &fakeNCMFile{dumpOK: true, metadataErr: errors.New("invalid tag")}, nil
			},
			want: "写入音乐元数据失败",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			staging := t.TempDir()
			_, err := newNCMEngine(test.factory).Convert(context.Background(), "/music/song.ncm", staging, false)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want text %q", err, test.want)
			}
		})
	}
}

func TestNCMEngineRejectsCanceledContextAndEscapedOutput(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	engine := newNCMEngine(func(string) (ncmFile, error) {
		called = true
		return &fakeNCMFile{}, nil
	})
	if _, err := engine.Convert(ctx, "/music/song.ncm", t.TempDir(), false); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if called {
		t.Fatal("factory should not run after cancellation")
	}

	staging := t.TempDir()
	escaped := filepath.Join(filepath.Dir(staging), "escaped.mp3")
	file := &fakeNCMFile{output: escaped, dumpOK: true, metadataOK: true}
	if _, err := newNCMEngine(func(string) (ncmFile, error) { return file, nil }).Convert(context.Background(), "/music/song.ncm", staging, false); err == nil {
		t.Fatal("expected escaped output to be rejected")
	}
}

type fakeNCMFile struct {
	output         string
	dumpOK         bool
	dumpErr        error
	metadataOK     bool
	metadataErr    error
	fetchRemoteArt bool
	metadataCalls  int
}

func (file *fakeNCMFile) Dump(stagingDir string) (bool, error) {
	if file.dumpErr != nil {
		return false, file.dumpErr
	}
	if file.output == "" {
		file.output = filepath.Join(stagingDir, "song.mp3")
	}
	if err := os.WriteFile(file.output, []byte("audio"), 0o600); err != nil {
		return false, err
	}
	return file.dumpOK, nil
}

func (file *fakeNCMFile) FixMetadata(fetchRemoteArt bool) (bool, error) {
	file.metadataCalls++
	file.fetchRemoteArt = fetchRemoteArt
	if file.metadataErr != nil {
		return false, file.metadataErr
	}
	return file.metadataOK, nil
}

func (file *fakeNCMFile) GetDumpFilePath() string {
	return file.output
}
