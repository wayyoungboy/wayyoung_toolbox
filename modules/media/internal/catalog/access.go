package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

type assetLocation struct {
	Root         string
	RootIdentity string
	FileIdentity string
	Path         string
	Size         int64
	ModifiedNS   int64
	Available    bool
	Poster       string
	Subtitles    []Subtitle
}

func (s *Store) assetLocation(ctx context.Context, id string) (assetLocation, error) {
	var item assetLocation
	var subtitles string
	err := s.db.QueryRowContext(ctx, `SELECT l.root,l.root_identity,a.file_identity,a.path,a.size,a.modified_ns,a.available,a.poster_path,a.subtitles
FROM catalog_assets a JOIN catalog_libraries l ON l.id=a.library_id WHERE a.id=?`, id).
		Scan(&item.Root, &item.RootIdentity, &item.FileIdentity, &item.Path, &item.Size, &item.ModifiedNS, &item.Available, &item.Poster, &subtitles)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	if !item.Available {
		return item, ErrUnavailable
	}
	if err := json.Unmarshal([]byte(subtitles), &item.Subtitles); err != nil {
		return item, err
	}
	return item, nil
}

// OpenStream serves only a registered asset through its configured root. No
// URL-supplied path or filename participates in filesystem resolution.
func (s *Store) OpenStream(ctx context.Context, id string) (OpenedFile, error) {
	item, err := s.assetLocation(ctx, id)
	if err != nil {
		return OpenedFile{}, err
	}
	root, err := openLibraryRoot(item.Root, item.RootIdentity)
	if err != nil {
		return OpenedFile{}, err
	}
	defer root.Close()
	file, info, err := openRegular(root, item.Path)
	if err != nil {
		return OpenedFile{}, err
	}
	if info.Size() != item.Size || info.ModTime().UnixNano() != item.ModifiedNS {
		file.Close()
		return OpenedFile{}, ErrConflict
	}
	identity, err := directoryIdentity(info)
	if err != nil || item.FileIdentity == "" || item.FileIdentity != stableID(identity, strconv.FormatInt(info.Size(), 10), strconv.FormatInt(info.ModTime().UnixNano(), 10)) {
		file.Close()
		return OpenedFile{}, ErrConflict
	}
	return OpenedFile{File: file, Name: path.Base(item.Path), Size: info.Size(), Modified: info.ModTime(),
		MIME: videoExtensions[strings.ToLower(path.Ext(item.Path))]}, nil
}

func (s *Store) OpenPoster(ctx context.Context, id string) (OpenedFile, error) {
	item, err := s.assetLocation(ctx, id)
	if err != nil {
		return OpenedFile{}, err
	}
	if item.Poster == "" {
		return OpenedFile{}, ErrNotFound
	}
	root, err := openLibraryRoot(item.Root, item.RootIdentity)
	if err != nil {
		return OpenedFile{}, err
	}
	defer root.Close()
	file, info, err := openRegular(root, item.Poster)
	if err != nil {
		return OpenedFile{}, err
	}
	valid := false
	defer func() {
		if !valid {
			file.Close()
		}
	}()
	if info.Size() > 20<<20 {
		return OpenedFile{}, ErrInvalid
	}
	var header [512]byte
	count, err := file.Read(header[:])
	if err != nil && !errors.Is(err, io.EOF) {
		return OpenedFile{}, err
	}
	mime := http.DetectContentType(header[:count])
	if mime != "image/jpeg" && mime != "image/png" && mime != "image/webp" && mime != "image/gif" {
		return OpenedFile{}, ErrInvalid
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return OpenedFile{}, err
	}
	valid = true
	return OpenedFile{File: file, Name: path.Base(item.Poster), Size: info.Size(), Modified: info.ModTime(), MIME: mime}, nil
}

var srtTimestamp = regexp.MustCompile(`(\d{2}:\d{2}:\d{2}),(\d{3})`)

func (s *Store) Subtitle(ctx context.Context, id string, index int) ([]byte, error) {
	if index < 0 {
		return nil, ErrInvalid
	}
	item, err := s.assetLocation(ctx, id)
	if err != nil {
		return nil, err
	}
	if index >= len(item.Subtitles) {
		return nil, ErrNotFound
	}
	root, err := openLibraryRoot(item.Root, item.RootIdentity)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	selected := item.Subtitles[index]
	raw, err := readBounded(root, selected.Path, 2<<20)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(raw) || strings.ContainsRune(string(raw), 0) {
		return nil, ErrInvalid
	}
	text := strings.TrimPrefix(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\ufeff")
	if selected.Format == "srt" {
		text = "WEBVTT\n\n" + srtTimestamp.ReplaceAllString(text, "$1.$2")
	} else if !strings.HasPrefix(text, "WEBVTT") {
		return nil, ErrInvalid
	}
	return []byte(text), nil
}

// The returned descriptor remains valid after its root handle is closed.
var _ io.ReadSeeker = (*os.File)(nil)
