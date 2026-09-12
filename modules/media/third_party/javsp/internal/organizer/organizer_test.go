package organizer

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/yuukiy/javsp-go/internal/model"
)

func TestMoveFilesContextHonorsCancellationBeforeMutation(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source", "ABC-123.mp4")
	destinationDir := filepath.Join(root, "destination")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destinationDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	movie := &model.Movie{Files: []string{source}, SaveDir: destinationDir, BaseName: "ABC-123"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := MoveFilesContext(ctx, movie, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("MoveFilesContext error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source changed after cancellation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destinationDir, "ABC-123.mp4")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination created after cancellation: %v", err)
	}
}

func TestGenerateNamesUsesTemplatesAndAttrSuffix(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "ABC-123-UC.mp4")
	if err := os.WriteFile(source, []byte("movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	uncensored := true
	movie := &model.Movie{
		DVDID: "ABC-123",
		Files: []string{source},
		Info: &model.MovieInfo{
			DVDID:       "ABC-123",
			Title:       "Title",
			Actress:     []string{"Actor A", "Actor B", "Actor C"},
			Genre:       []string{"Drama"},
			Score:       "8",
			Serial:      "Series",
			Director:    "Director",
			Producer:    "Producer",
			Publisher:   "Publisher",
			PublishDate: "2024-01-02",
			Uncensored:  &uncensored,
		},
	}
	err := GenerateNames(movie, Options{
		MoveFiles:             true,
		OutputFolderPattern:   filepath.Join(root, "out", "{actress}", "[{num}] {title}"),
		BasenamePattern:       "{num}-{year}-{label}-{genre}",
		NFOBasenamePattern:    "movie",
		NFOTitlePattern:       "{num} {title}",
		FanartBasenamePattern: "fanart",
		PosterBasenamePattern: "poster",
		MaxActressCount:       2,
		DefaultActress:        "#Unknown Actress",
		DefaultTitle:          "#Unknown Title",
		CensorText:            []string{"无码", "有码", "打码情况未知"},
	})
	if err != nil {
		t.Fatalf("GenerateNames returned error: %v", err)
	}
	if !strings.Contains(movie.SaveDir, "Actor A,Actor B,…") {
		t.Fatalf("SaveDir = %q, want truncated actress list", movie.SaveDir)
	}
	if movie.BaseName != "ABC-123-UC-2024-ABC-Drama" {
		t.Fatalf("BaseName = %q", movie.BaseName)
	}
	if movie.Info.NFOTitle != "ABC-123 Title" {
		t.Fatalf("NFOTitle = %q", movie.Info.NFOTitle)
	}
	if filepath.Base(movie.NFOFile) != "movie.nfo" {
		t.Fatalf("NFOFile = %q", movie.NFOFile)
	}
	if filepath.Base(movie.FanartFile) != "fanart.jpg" || filepath.Base(movie.PosterFile) != "poster.jpg" {
		t.Fatalf("fanart/poster = %q / %q", movie.FanartFile, movie.PosterFile)
	}
}

func TestGenerateNamesPreservesScannedIDAgainstTruncatedMetadata(t *testing.T) {
	cases := []struct {
		scanned   string
		truncated string
	}{
		{scanned: "JUR-822", truncated: "-822"},
		{scanned: "ROYD-347", truncated: "-347"},
		{scanned: "JUR-832", truncated: "-832"},
		{scanned: "ABF-382", truncated: "-382"},
	}
	for _, test := range cases {
		t.Run(test.scanned, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, test.scanned+".mp4")
			if err := os.WriteFile(source, []byte("movie"), 0o644); err != nil {
				t.Fatal(err)
			}
			movie := &model.Movie{
				DVDID: test.scanned,
				Files: []string{source},
				Info:  &model.MovieInfo{DVDID: test.truncated, Title: "Title"},
			}
			err := GenerateNames(movie, Options{
				MoveFiles:             true,
				OutputFolderPattern:   filepath.Join(root, "out", "[{num}]"),
				BasenamePattern:       "{num}",
				NFOBasenamePattern:    "movie",
				NFOTitlePattern:       "{num} {title}",
				FanartBasenamePattern: "fanart",
				PosterBasenamePattern: "poster",
			})
			if err != nil {
				t.Fatalf("GenerateNames returned error: %v", err)
			}
			if movie.Info.DVDID != test.scanned {
				t.Fatalf("DVDID = %q, want %q", movie.Info.DVDID, test.scanned)
			}
			if filepath.Base(movie.SaveDir) != "["+test.scanned+"]" || movie.BaseName != test.scanned {
				t.Fatalf("generated path used truncated ID: SaveDir=%q BaseName=%q", movie.SaveDir, movie.BaseName)
			}
			if movie.Info.NFOTitle != test.scanned+" Title" {
				t.Fatalf("NFO title = %q", movie.Info.NFOTitle)
			}
		})
	}
}

func TestGenerateNamesWithoutMovingKeepsSourceBasename(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "ABC-123 custom.mp4")
	if err := os.WriteFile(source, []byte("movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	movie := &model.Movie{
		DVDID: "ABC-123",
		Files: []string{source},
		Info:  &model.MovieInfo{DVDID: "ABC-123", Title: "Title"},
	}
	err := GenerateNames(movie, Options{
		MoveFiles:             false,
		OutputFolderPattern:   filepath.Join(root, "out", "{num}"),
		BasenamePattern:       "{num}",
		NFOBasenamePattern:    "movie",
		NFOTitlePattern:       "{num} {title}",
		FanartBasenamePattern: "fanart",
		PosterBasenamePattern: "poster",
		DefaultActress:        "#Unknown Actress",
		DefaultTitle:          "#Unknown Title",
		CensorText:            []string{"无码", "有码", "打码情况未知"},
	})
	if err != nil {
		t.Fatalf("GenerateNames returned error: %v", err)
	}
	if movie.SaveDir != root {
		t.Fatalf("SaveDir = %q, want source dir", movie.SaveDir)
	}
	if movie.BaseName != "ABC-123 custom" {
		t.Fatalf("BaseName = %q, want source basename", movie.BaseName)
	}
}

func TestGenerateNamesTruncatesLongTitleToLengthMaximum(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "ABC-123.mp4")
	if err := os.WriteFile(source, []byte("movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	longTitle := strings.Repeat("LongTitle", 20)
	movie := &model.Movie{
		DVDID: "ABC-123",
		Files: []string{source},
		Info:  &model.MovieInfo{DVDID: "ABC-123", Title: longTitle},
	}
	maxLen := len(filepath.Join(root, "out")) + 80
	err := GenerateNames(movie, Options{
		MoveFiles:             true,
		OutputFolderPattern:   filepath.Join(root, "out", "{title}"),
		BasenamePattern:       "{num}-{title}",
		NFOBasenamePattern:    "movie",
		NFOTitlePattern:       "{num} {title}",
		FanartBasenamePattern: "fanart",
		PosterBasenamePattern: "poster",
		LengthMaximum:         maxLen,
		LengthByByte:          true,
		DefaultActress:        "#Unknown Actress",
		DefaultTitle:          "#Unknown Title",
	})
	if err != nil {
		t.Fatalf("GenerateNames returned error: %v", err)
	}
	if movie.Info.Title != longTitle {
		t.Fatalf("metadata Title = %q, want original full title", movie.Info.Title)
	}
	if movie.Info.NFOTitle != "ABC-123 "+longTitle {
		t.Fatalf("NFO title = %q, want original full title", movie.Info.NFOTitle)
	}
	destination, err := filepath.Abs(filepath.Join(movie.SaveDir, movie.BaseName+filepath.Ext(source)))
	if err != nil {
		t.Fatal(err)
	}
	if len(destination) > maxLen {
		t.Fatalf("video path length = %d, want <= %d: %s", len(destination), maxLen, destination)
	}
	if movie.BaseName == "ABC-123-"+longTitle {
		t.Fatal("path basename was not truncated")
	}
}

func TestGenerateNamesChecksVideoBasenameAgainstLengthLimit(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "ABC-123.mp4")
	if err := os.WriteFile(source, []byte("movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	title := strings.Repeat("长标题", 30)
	movie := &model.Movie{
		DVDID: "ABC-123",
		Files: []string{source},
		Info:  &model.MovieInfo{DVDID: "ABC-123", Title: title},
	}
	maxLen := len(filepath.Join(root, "out")) + 50
	err := GenerateNames(movie, Options{
		MoveFiles:             true,
		OutputFolderPattern:   filepath.Join(root, "out"),
		BasenamePattern:       "{title}",
		NFOBasenamePattern:    "movie",
		NFOTitlePattern:       "{num} {title}",
		FanartBasenamePattern: "fanart",
		PosterBasenamePattern: "poster",
		LengthMaximum:         maxLen,
		LengthByByte:          true,
		DefaultActress:        "#Unknown Actress",
		DefaultTitle:          "#Unknown Title",
	})
	if err != nil {
		t.Fatalf("GenerateNames returned error: %v", err)
	}
	destination := filepath.Join(movie.SaveDir, movie.BaseName+filepath.Ext(source))
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(absDestination) > maxLen {
		t.Fatalf("video path length = %d, want <= %d: %s", len(absDestination), maxLen, absDestination)
	}
	if movie.Info.Title != title || movie.Info.NFOTitle != "ABC-123 "+title {
		t.Fatalf("metadata was truncated: Title=%q NFOTitle=%q", movie.Info.Title, movie.Info.NFOTitle)
	}
}

func TestMoveFilesRenamesAndUpdatesNewPaths(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src", "ABC-123.mp4")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	saveDir := filepath.Join(root, "dst")
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	movie := &model.Movie{
		Files:    []string{source},
		SaveDir:  saveDir,
		BaseName: "ABC-123",
	}
	if err := MoveFiles(movie, false); err != nil {
		t.Fatalf("MoveFiles returned error: %v", err)
	}
	want := filepath.Join(saveDir, "ABC-123.mp4")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("moved file missing: %v", err)
	}
	if len(movie.NewPaths) != 1 || movie.NewPaths[0] != want {
		t.Fatalf("NewPaths = %#v, want %q", movie.NewPaths, want)
	}
	if _, err := os.Stat(filepath.Dir(source)); !os.IsNotExist(err) {
		t.Fatalf("source dir still exists or unexpected stat error: %v", err)
	}
}

func TestMoveFilesFallsBackToCopyAcrossFilesystems(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src", "ABC-123.mp4")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	wantContents := []byte("movie contents")
	if err := os.WriteFile(source, wantContents, 0o751); err != nil {
		t.Fatal(err)
	}
	saveDir := filepath.Join(root, "dst")
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	movie := &model.Movie{Files: []string{source}, SaveDir: saveDir, BaseName: "ABC-123"}

	originalRename := renameFile
	t.Cleanup(func() { renameFile = originalRename })
	renameFile = func(oldPath, newPath string) error {
		if oldPath != source || newPath != filepath.Join(saveDir, "ABC-123.mp4") {
			t.Fatalf("renameFile(%q, %q) called with unexpected paths", oldPath, newPath)
		}
		return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: syscall.EXDEV}
	}

	if err := MoveFiles(movie, false); err != nil {
		t.Fatalf("MoveFiles returned error: %v", err)
	}
	destination := filepath.Join(saveDir, "ABC-123.mp4")
	gotContents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(gotContents) != string(wantContents) {
		t.Fatalf("copied contents = %q, want %q", gotContents, wantContents)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o751 {
		t.Fatalf("copied mode = %o, want 751", got)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still exists or stat failed unexpectedly: %v", err)
	}
	if len(movie.NewPaths) != 1 || movie.NewPaths[0] != destination {
		t.Fatalf("NewPaths = %#v, want %q", movie.NewPaths, destination)
	}
}

func TestMoveFilesRejectsCollisionWithoutChangingPaths(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source", "ABC-123.mp4")
	destination := filepath.Join(root, "destination", "ABC-123.mp4")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	movie := &model.Movie{
		Files:    []string{source},
		SaveDir:  filepath.Dir(destination),
		BaseName: "ABC-123",
		NewPaths: []string{"unchanged"},
	}

	if err := MoveFiles(movie, false); err == nil || !strings.Contains(err.Error(), "file exists") {
		t.Fatalf("MoveFiles error = %v, want collision error", err)
	}
	if got, err := os.ReadFile(source); err != nil || string(got) != "source" {
		t.Fatalf("source changed: contents=%q err=%v", got, err)
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != "existing" {
		t.Fatalf("destination changed: contents=%q err=%v", got, err)
	}
	if len(movie.NewPaths) != 1 || movie.NewPaths[0] != "unchanged" {
		t.Fatalf("NewPaths = %#v, want unchanged", movie.NewPaths)
	}
}

type injectedSourceFile struct {
	*os.File
	closeErr   error
	closeCalls int
}

func (f *injectedSourceFile) Close() error {
	f.closeCalls++
	err := f.File.Close()
	return errors.Join(f.closeErr, err)
}

type injectedDestinationFile struct {
	*os.File
	chmodErr   error
	closeErr   error
	closeCalls int
}

func (f *injectedDestinationFile) Chmod(mode os.FileMode) error {
	if f.chmodErr != nil {
		return f.chmodErr
	}
	return f.File.Chmod(mode)
}

func (f *injectedDestinationFile) Close() error {
	f.closeCalls++
	err := f.File.Close()
	return errors.Join(f.closeErr, err)
}

func TestCopyAndRemoveCleansDestinationOnPrimaryFailures(t *testing.T) {
	tests := []struct {
		name      string
		stage     string
		wantErr   error
		configure func(*injectedSourceFile, *injectedDestinationFile, string, string)
	}{
		{
			name:    "copy",
			stage:   "copy",
			wantErr: errInjectedCopy,
			configure: func(_ *injectedSourceFile, _ *injectedDestinationFile, _, _ string) {
				copyFile = func(io.Writer, io.Reader) (int64, error) { return 0, errInjectedCopy }
			},
		},
		{
			name:    "chmod",
			stage:   "chmod destination",
			wantErr: errInjectedChmod,
			configure: func(_ *injectedSourceFile, destination *injectedDestinationFile, _, _ string) {
				destination.chmodErr = errInjectedChmod
			},
		},
		{
			name:    "destination close",
			stage:   "close destination",
			wantErr: errInjectedDestinationClose,
			configure: func(_ *injectedSourceFile, destination *injectedDestinationFile, _, _ string) {
				destination.closeErr = errInjectedDestinationClose
			},
		},
		{
			name:    "source close",
			stage:   "close source",
			wantErr: errInjectedSourceClose,
			configure: func(source *injectedSourceFile, _ *injectedDestinationFile, _, _ string) {
				source.closeErr = errInjectedSourceClose
			},
		},
		{
			name:    "source remove",
			stage:   "remove source",
			wantErr: errInjectedSourceRemove,
			configure: func(_ *injectedSourceFile, _ *injectedDestinationFile, source, _ string) {
				removeFile = func(path string) error {
					if path == source {
						return errInjectedSourceRemove
					}
					return os.Remove(path)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, destination, sourceFile, destinationFile := setupInjectedCopy(t)
			test.configure(sourceFile, destinationFile, source, destination)

			err := copyAndRemove(source, destination)
			if !errors.Is(err, test.wantErr) || !strings.Contains(err.Error(), test.stage) {
				t.Fatalf("copyAndRemove error = %v, want contextual %q error", err, test.stage)
			}
			if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("partial destination remains or stat failed: %v", err)
			}
			if _, err := os.Stat(source); err != nil {
				t.Fatalf("source not retained: %v", err)
			}
			if sourceFile.closeCalls != 1 {
				t.Fatalf("source close calls = %d, want 1", sourceFile.closeCalls)
			}
			if destinationFile.closeCalls != 1 {
				t.Fatalf("destination close calls = %d, want 1", destinationFile.closeCalls)
			}
		})
	}
}

var (
	errInjectedCopy             = errors.New("injected copy failure")
	errInjectedChmod            = errors.New("injected chmod failure")
	errInjectedDestinationClose = errors.New("injected destination close failure")
	errInjectedSourceClose      = errors.New("injected source close failure")
	errInjectedSourceRemove     = errors.New("injected source remove failure")
)

func TestCopyAndRemoveJoinsCleanupFailures(t *testing.T) {
	source, destination, sourceFile, destinationFile := setupInjectedCopy(t)
	cleanupRemoveErr := errors.New("injected cleanup remove failure")
	destinationFile.closeErr = errInjectedDestinationClose
	sourceFile.closeErr = errInjectedSourceClose
	copyFile = func(io.Writer, io.Reader) (int64, error) { return 0, errInjectedCopy }
	removeFile = func(path string) error {
		if path == destination {
			return cleanupRemoveErr
		}
		return os.Remove(path)
	}

	err := copyAndRemove(source, destination)
	for _, want := range []error{errInjectedCopy, errInjectedDestinationClose, errInjectedSourceClose, cleanupRemoveErr} {
		if !errors.Is(err, want) {
			t.Fatalf("copyAndRemove error = %v, want joined cause %v", err, want)
		}
	}
	for _, context := range []string{"copy", "close destination", "close source", "remove partial destination"} {
		if !strings.Contains(err.Error(), context) {
			t.Fatalf("copyAndRemove error = %v, want context %q", err, context)
		}
	}
	if sourceFile.closeCalls != 1 || destinationFile.closeCalls != 1 {
		t.Fatalf("close calls source/destination = %d/%d, want 1/1", sourceFile.closeCalls, destinationFile.closeCalls)
	}
	if _, statErr := os.Stat(source); statErr != nil {
		t.Fatalf("source not retained: %v", statErr)
	}
	if _, statErr := os.Stat(destination); statErr != nil {
		t.Fatalf("destination missing despite injected cleanup failure: %v", statErr)
	}
	if removeErr := os.Remove(destination); removeErr != nil {
		t.Fatalf("remove test destination: %v", removeErr)
	}
}

func setupInjectedCopy(t *testing.T) (string, string, *injectedSourceFile, *injectedDestinationFile) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source.mp4")
	destination := filepath.Join(root, "destination.mp4")
	if err := os.WriteFile(source, []byte("movie"), 0o751); err != nil {
		t.Fatal(err)
	}

	originalOpenSource := openSourceFile
	originalOpenDestination := openDestinationFile
	originalCopy := copyFile
	originalRemove := removeFile
	t.Cleanup(func() {
		openSourceFile = originalOpenSource
		openDestinationFile = originalOpenDestination
		copyFile = originalCopy
		removeFile = originalRemove
	})

	sourceProxy := &injectedSourceFile{}
	destinationProxy := &injectedDestinationFile{}
	openSourceFile = func(path string) (sourceMoveFile, error) {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		sourceProxy.File = file
		return sourceProxy, nil
	}
	openDestinationFile = func(path string, flag int, mode os.FileMode) (destinationMoveFile, error) {
		file, err := os.OpenFile(path, flag, mode)
		if err != nil {
			return nil, err
		}
		destinationProxy.File = file
		return destinationProxy, nil
	}
	return source, destination, sourceProxy, destinationProxy
}
