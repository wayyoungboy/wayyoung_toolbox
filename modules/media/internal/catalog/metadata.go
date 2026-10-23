package catalog

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const maxNFOBytes = 1 << 20

var videoExtensions = map[string]string{
	".mp4": "video/mp4", ".m4v": "video/mp4", ".mkv": "video/x-matroska", ".mov": "video/quicktime",
	".webm": "video/webm", ".avi": "video/x-msvideo", ".wmv": "video/x-ms-wmv", ".flv": "video/x-flv",
	".ts": "video/mp2t", ".m2ts": "video/mp2t", ".mpg": "video/mpeg", ".mpeg": "video/mpeg",
}

type metadata struct {
	TitleProvided bool     `json:"title_provided"`
	Code          string   `json:"code"`
	Title         string   `json:"title"`
	OriginalTitle string   `json:"original_title"`
	Plot          string   `json:"plot"`
	Studio        string   `json:"studio"`
	ReleaseDate   string   `json:"release_date"`
	Year          int      `json:"year"`
	Actors        []string `json:"actors"`
	Genres        []string `json:"genres"`
	Poster        string   `json:"poster"`
	Source        string   `json:"source"`
}

type nfoDocument struct {
	XMLName       xml.Name
	Title         string `xml:"title"`
	OriginalTitle string `xml:"originaltitle"`
	Plot          string `xml:"plot"`
	Studio        string `xml:"studio"`
	Premiered     string `xml:"premiered"`
	ReleaseDate   string `xml:"releasedate"`
	Year          int    `xml:"year"`
	Number        string `xml:"num"`
	ID            string `xml:"id"`
	UniqueIDs     []struct {
		Type  string `xml:"type,attr"`
		Value string `xml:",chardata"`
	} `xml:"uniqueid"`
	Actors []struct {
		Name string `xml:"name"`
	} `xml:"actor"`
	Genres []string `xml:"genre"`
	Tags   []string `xml:"tag"`
	Art    struct {
		Poster string `xml:"poster"`
	} `xml:"art"`
	Thumbs []struct {
		Aspect string `xml:"aspect,attr"`
		Value  string `xml:",chardata"`
	} `xml:"thumb"`
}

func uniqueText(values []string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func (s *Store) code(value string) string {
	if s.resolveCode == nil {
		return ""
	}
	code, ok := s.resolveCode(value)
	if !ok {
		return ""
	}
	return code
}

func (s *Store) parseNFO(raw []byte) (metadata, error) {
	data := metadata{Actors: []string{}, Genres: []string{}, Source: "local_nfo"}
	declarations := bytes.ToUpper(bytes.ReplaceAll(raw, []byte{0}, nil))
	if len(raw) > maxNFOBytes || bytes.Contains(declarations, []byte("<!DOCTYPE")) || bytes.Contains(declarations, []byte("<!ENTITY")) {
		return data, fmt.Errorf("NFO declaration or size is not supported")
	}
	var document nfoDocument
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&document); err != nil {
		return data, err
	}
	if document.XMLName.Local != "movie" && document.XMLName.Local != "episodedetails" && document.XMLName.Local != "musicvideo" {
		return data, fmt.Errorf("unsupported NFO root")
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return data, err
		}
		if text, ok := token.(xml.CharData); !ok || len(bytes.TrimSpace(text)) != 0 {
			return data, fmt.Errorf("trailing NFO document")
		}
	}
	type identifier struct {
		value  string
		strict bool
	}
	identifiers := []identifier{{document.Number, true}, {document.ID, false}}
	for _, id := range document.UniqueIDs {
		switch strings.ToLower(strings.TrimSpace(id.Type)) {
		case "num", "dvdid", "jav", "javid", "javboss", "":
			identifiers = append(identifiers, identifier{id.Value, strings.TrimSpace(id.Type) != ""})
		}
	}
	for _, candidate := range identifiers {
		code := s.code(candidate.value)
		if code == "" {
			if candidate.strict && strings.TrimSpace(candidate.value) != "" {
				return data, fmt.Errorf("unresolved or ambiguous NFO identity")
			}
			continue
		}
		if data.Code != "" && data.Code != code {
			return data, fmt.Errorf("multiple NFO identities")
		}
		data.Code = code
	}
	data.Title, data.OriginalTitle = strings.TrimSpace(document.Title), strings.TrimSpace(document.OriginalTitle)
	data.TitleProvided = data.Title != ""
	data.Plot, data.Studio = strings.TrimSpace(document.Plot), strings.TrimSpace(document.Studio)
	data.ReleaseDate = strings.TrimSpace(document.Premiered)
	if data.ReleaseDate == "" {
		data.ReleaseDate = strings.TrimSpace(document.ReleaseDate)
	}
	data.Year = document.Year
	if data.Year == 0 && len(data.ReleaseDate) >= 4 {
		data.Year, _ = strconv.Atoi(data.ReleaseDate[:4])
	}
	if data.Year < 1800 || data.Year > 2200 {
		data.Year = 0
	}
	for _, actor := range document.Actors {
		data.Actors = append(data.Actors, actor.Name)
	}
	data.Actors = uniqueText(data.Actors)
	data.Genres = uniqueText(append(document.Genres, document.Tags...))
	data.Poster = strings.TrimSpace(document.Art.Poster)
	if data.Poster == "" {
		for _, thumb := range document.Thumbs {
			if thumb.Aspect == "poster" || thumb.Aspect == "" {
				data.Poster = strings.TrimSpace(thumb.Value)
				break
			}
		}
	}
	return data, nil
}

// mergeMetadata fills missing fields without allowing an incomplete edition's
// sidecar to displace richer metadata from another edition in the same scan.
func mergeMetadata(first, next metadata) metadata {
	if first.Source != "local_nfo" && next.Source == "local_nfo" {
		first, next = next, first
	}
	if !first.TitleProvided && next.TitleProvided {
		first.Title, first.TitleProvided = next.Title, true
	}
	for _, pair := range [][2]*string{{&first.OriginalTitle, &next.OriginalTitle}, {&first.Plot, &next.Plot},
		{&first.Studio, &next.Studio}, {&first.ReleaseDate, &next.ReleaseDate}, {&first.Poster, &next.Poster}} {
		if *pair[0] == "" {
			*pair[0] = *pair[1]
		}
	}
	if first.Year == 0 {
		first.Year = next.Year
	}
	first.Actors = uniqueText(append(first.Actors, next.Actors...))
	first.Genres = uniqueText(append(first.Genres, next.Genres...))
	return first
}

// openRegular refuses symlinks and checks the opened descriptor against the
// directory entry. os.Root also confines intermediate path resolution.
func openRegular(root *os.Root, relative string) (*os.File, os.FileInfo, error) {
	if !fs.ValidPath(relative) || relative == "." {
		return nil, nil, ErrConflict
	}
	before, err := root.Lstat(relative)
	if err != nil {
		return nil, nil, ErrUnavailable
	}
	if !before.Mode().IsRegular() {
		return nil, nil, ErrConflict
	}
	file, err := root.Open(relative)
	if err != nil {
		return nil, nil, ErrUnavailable
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		file.Close()
		return nil, nil, ErrConflict
	}
	return file, after, nil
}

func readBounded(root *os.Root, relative string, limit int64) ([]byte, error) {
	file, info, err := openRegular(root, relative)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info.Size() > limit {
		return nil, ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, ErrInvalid
	}
	return raw, nil
}

type directorySidecars struct {
	files  map[string]string
	videos []string
}

func sidecarDirectory(root *os.Root, directory string) (directorySidecars, error) {
	result := directorySidecars{files: map[string]string{}}
	entries, err := fs.ReadDir(root.FS(), directory)
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		relative := path.Join(directory, entry.Name())
		key := strings.ToLower(entry.Name())
		// A case-sensitive directory containing colliding sidecars is ambiguous.
		if existing, ok := result.files[key]; ok && existing != relative {
			result.files[key] = ""
		} else {
			result.files[key] = relative
		}
		if videoExtensions[strings.ToLower(path.Ext(entry.Name()))] != "" {
			result.videos = append(result.videos, entry.Name())
		}
	}
	return result, nil
}

func (s *Store) sharedSidecarAllowed(directory directorySidecars, code string) bool {
	if len(directory.videos) == 1 {
		return true
	}
	if code == "" {
		return false
	}
	for _, video := range directory.videos {
		if s.code(strings.TrimSuffix(video, filepath.Ext(video))) != code {
			return false
		}
	}
	return true
}

func (s *Store) nfoSidecar(directory directorySidecars, filename, code string) string {
	stem := strings.TrimSuffix(filename, path.Ext(filename))
	if exact := directory.files[strings.ToLower(stem+".nfo")]; exact != "" {
		return exact
	}
	matches := []string{}
	for name, relative := range directory.files {
		if relative != "" && path.Ext(name) == ".nfo" && code != "" && s.code(strings.TrimSuffix(name, ".nfo")) == code {
			matches = append(matches, relative)
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	if len(matches) == 0 && s.sharedSidecarAllowed(directory, code) {
		return directory.files["movie.nfo"]
	}
	return ""
}

func safeArtwork(directory, value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	if value == "" || strings.ContainsAny(value, ":\x00") || strings.HasPrefix(value, "/") {
		return ""
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return ""
		}
	}
	relative := path.Join(directory, value)
	if !fs.ValidPath(relative) {
		return ""
	}
	return relative
}

func (s *Store) posterSidecar(root *os.Root, directory directorySidecars, filename, code, declared string) string {
	stem := strings.TrimSuffix(filename, path.Ext(filename))
	candidates := []string{}
	if relative := safeArtwork(path.Dir(filename), declared); relative != "" {
		candidates = append(candidates, relative)
	}
	names := []string{path.Base(stem) + "-poster", path.Base(stem)}
	if s.sharedSidecarAllowed(directory, code) {
		names = append(names, "poster", "folder", "cover")
	}
	for _, name := range names {
		for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp", ".gif"} {
			if relative := directory.files[strings.ToLower(name+ext)]; relative != "" {
				candidates = append(candidates, relative)
			}
		}
	}
	for _, relative := range candidates {
		if info, err := root.Lstat(relative); err == nil && info.Mode().IsRegular() {
			return relative
		}
	}
	return ""
}

func subtitleSidecars(directory directorySidecars, filename string) []Subtitle {
	stem := strings.ToLower(strings.TrimSuffix(path.Base(filename), path.Ext(filename)))
	paths := []string{}
	for name, relative := range directory.files {
		ext := path.Ext(name)
		base := strings.TrimSuffix(name, ext)
		if relative != "" && (ext == ".srt" || ext == ".vtt") && (base == stem || strings.HasPrefix(base, stem+".")) {
			paths = append(paths, relative)
		}
	}
	sort.Strings(paths)
	result := []Subtitle{}
	for _, relative := range paths {
		language := "und"
		base := strings.TrimSuffix(path.Base(relative), path.Ext(relative))
		if len(base) > len(stem)+1 {
			candidate := strings.ToLower(strings.Split(base[len(stem)+1:], ".")[0])
			if len(candidate) >= 2 && len(candidate) <= 3 {
				language = candidate
			}
		}
		result = append(result, Subtitle{Index: len(result), Label: path.Base(relative), Language: language,
			Format: strings.TrimPrefix(strings.ToLower(path.Ext(relative)), "."), Path: relative})
	}
	return result
}
