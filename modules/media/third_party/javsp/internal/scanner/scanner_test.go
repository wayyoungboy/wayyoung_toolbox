package scanner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestScanContextStopsBeforeWalkingFiles(t *testing.T) {
	root := t.TempDir()
	makeFile(t, root, "ABC-123.mp4", defaultSize)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	movies, err := ScanContext(ctx, root, defaultOptions())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ScanContext error = %v, want context.Canceled", err)
	}
	if len(movies) != 0 {
		t.Fatalf("movies = %#v, want no results after cancellation", movies)
	}
}

const defaultSize int64 = 512 * 1024 * 1024

func makeFile(t *testing.T, root, name string, size int64) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Seek(size-1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
}

func defaultOptions() Options {
	return Options{
		FilenameExtensions:       []string{".mp4", ".mkv", ".avi"},
		MinimumSize:              defaultSize / 2,
		IgnoredIDPattern:         nil,
		IgnoredFolderNamePattern: []string{"^\\."},
	}
}

func TestScanSingleMovie(t *testing.T) {
	root := t.TempDir()
	makeFile(t, root, "ABC-123.mp4", defaultSize)
	movies, err := Scan(root, defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 1 {
		t.Fatalf("len = %d, want 1", len(movies))
	}
	if movies[0].DVDID != "ABC-123" {
		t.Fatalf("DVDID = %q, want ABC-123", movies[0].DVDID)
	}
	if filepath.Base(movies[0].Files[0]) != "ABC-123.mp4" {
		t.Fatalf("file = %s", movies[0].Files[0])
	}
}

func TestScanDoesNotSkipNFODirectoryByDefault(t *testing.T) {
	root := t.TempDir()
	makeFile(t, root, "with-nfo/ABC-123.mp4", defaultSize)
	if err := os.WriteFile(filepath.Join(root, "with-nfo", "movie.nfo"), []byte("<movie/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	movies, err := Scan(root, defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 1 || movies[0].DVDID != "ABC-123" {
		t.Fatalf("movies = %#v, want ABC-123 from directory containing NFO", movies)
	}

	opts := defaultOptions()
	opts.SkipNFODir = true
	movies, err = Scan(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 0 {
		t.Fatalf("movies = %#v, want NFO directory skipped when explicitly enabled", movies)
	}
}

func TestScanSlices123(t *testing.T) {
	root := t.TempDir()
	makeFile(t, root, "ABC-123.1.mp4", defaultSize)
	makeFile(t, root, "ABC-123. 2.mp4", defaultSize)
	makeFile(t, root, "ABC-123.3.mp4", defaultSize)
	movies, err := Scan(root, defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 1 || len(movies[0].Files) != 3 {
		t.Fatalf("movies = %#v", movies)
	}
	if filepath.Base(movies[0].Files[0]) != "ABC-123.1.mp4" {
		t.Fatalf("first slice = %s", movies[0].Files[0])
	}
}

func TestScanSlicesABC(t *testing.T) {
	root := t.TempDir()
	makeFile(t, root, "ABC-123-A.mp4", defaultSize)
	makeFile(t, root, "ABC-123-B.mp4", defaultSize)
	makeFile(t, root, "ABC-123- C .mp4", defaultSize)
	movies, err := Scan(root, defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 1 || len(movies[0].Files) != 3 {
		t.Fatalf("movies = %#v", movies)
	}
	if filepath.Base(movies[0].Files[2]) != "ABC-123- C .mp4" {
		t.Fatalf("third slice = %s", movies[0].Files[2])
	}
}

func TestInvalidSliceStructureSelectsLargestMP4(t *testing.T) {
	root := t.TempDir()
	makeFile(t, root, "ABC-123.CD1.mp4", defaultSize)
	makeFile(t, root, "sub/ABC-123.CD2.mp4", defaultSize+2)
	makeFile(t, root, "ABC-123.CD3.mp4", defaultSize+1)
	movies, err := Scan(root, defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 1 || len(movies[0].Files) != 1 {
		t.Fatalf("movies = %#v, want one selected duplicate", movies)
	}
	if filepath.Base(movies[0].Files[0]) != "ABC-123.CD2.mp4" {
		t.Fatalf("selected file = %q, want largest MP4", movies[0].Files[0])
	}
}

func TestDuplicateIDPrefersMP4BeforeOtherExtensions(t *testing.T) {
	root := t.TempDir()
	makeFile(t, root, "ABC-123-FHD.mp4", defaultSize+1)
	makeFile(t, root, "ABC-123-C.mp4", defaultSize+2)
	makeFile(t, root, "ABC-123.mkv", defaultSize+3)

	movies, err := Scan(root, defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 1 || len(movies[0].Files) != 1 {
		t.Fatalf("movies = %#v, want one selected duplicate", movies)
	}
	if filepath.Base(movies[0].Files[0]) != "ABC-123-C.mp4" {
		t.Fatalf("selected file = %q, want largest MP4", movies[0].Files[0])
	}
}

func TestSmallVideoNeverJoinsRecognizedMovie(t *testing.T) {
	root := t.TempDir()
	makeFile(t, root, "ABC-123-1.mp4", defaultSize)
	makeFile(t, root, "ABC-123-2.mp4", 1024)
	movies, err := Scan(root, defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 1 {
		t.Fatalf("len = %d, want 1", len(movies))
	}
	if len(movies[0].Files) != 1 || filepath.Base(movies[0].Files[0]) != "ABC-123-1.mp4" {
		t.Fatalf("files = %#v, want only the file at or above the threshold", movies[0].Files)
	}
}

func TestSmallNonSliceWithSameIDDoesNotHideMainMovie(t *testing.T) {
	root := t.TempDir()
	makeFile(t, root, "ATID-687-FHD.mp4", defaultSize)
	makeFile(t, root, "ATID-687-RP.mp4", 1024)

	movies, err := Scan(root, defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 1 {
		t.Fatalf("movies = %#v, want one movie", movies)
	}
	if len(movies[0].Files) != 1 || filepath.Base(movies[0].Files[0]) != "ATID-687-FHD.mp4" {
		t.Fatalf("files = %#v, want only the main movie", movies[0].Files)
	}
}

func TestSmallNonSliceDoesNotBreakValidLargeSlices(t *testing.T) {
	root := t.TempDir()
	makeFile(t, root, "ABC-123-1.mp4", defaultSize)
	makeFile(t, root, "ABC-123-2.mp4", defaultSize)
	makeFile(t, root, "ABC-123-RP.mp4", 1024)

	movies, err := Scan(root, defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 1 || len(movies[0].Files) != 2 {
		t.Fatalf("movies = %#v, want the two valid large slices", movies)
	}
	if filepath.Base(movies[0].Files[0]) != "ABC-123-1.mp4" || filepath.Base(movies[0].Files[1]) != "ABC-123-2.mp4" {
		t.Fatalf("files = %#v, want ordered large slices", movies[0].Files)
	}
}

func TestIgnoredFolderPatternUsesPythonMatchSemantics(t *testing.T) {
	root := t.TempDir()
	makeFile(t, root, "cache-folder/ABC-123.mp4", defaultSize)
	makeFile(t, root, "prefix-cache/DEF-456.mp4", defaultSize)
	opts := defaultOptions()
	opts.IgnoredFolderNamePattern = []string{"cache"}

	movies, err := Scan(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 1 || movies[0].DVDID != "DEF-456" {
		t.Fatalf("movies = %#v, want only DEF-456 from prefix-cache", movies)
	}
}

func TestScanOneGiBThresholdIsInclusive(t *testing.T) {
	root := t.TempDir()
	const oneGiB int64 = 1024 * 1024 * 1024
	makeFile(t, root, "ABC-123.mp4", oneGiB-1)
	makeFile(t, root, "DEF-456.mp4", oneGiB)
	opts := defaultOptions()
	opts.MinimumSize = oneGiB

	movies, err := Scan(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 1 || movies[0].DVDID != "DEF-456" {
		t.Fatalf("movies = %#v, want only the exactly-1GiB movie", movies)
	}
}
