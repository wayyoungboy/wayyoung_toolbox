package organizer

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yuukiy/javsp-go/internal/avid"
	"github.com/yuukiy/javsp-go/internal/model"
	"github.com/yuukiy/javsp-go/internal/util"
)

type Options struct {
	MoveFiles             bool
	OutputFolderPattern   string
	BasenamePattern       string
	NFOBasenamePattern    string
	NFOTitlePattern       string
	FanartBasenamePattern string
	PosterBasenamePattern string
	LengthMaximum         int
	LengthByByte          bool
	MaxActressCount       int
	DefaultTitle          string
	DefaultActress        string
	DefaultSeries         string
	DefaultDirector       string
	DefaultProducer       string
	DefaultPublisher      string
	CensorText            []string
}

func GenerateNames(movie *model.Movie, opts Options) error {
	if movie.Info == nil {
		return fmt.Errorf("movie info is required")
	}
	if len(movie.Files) == 0 {
		return fmt.Errorf("movie files are required")
	}
	preserveScannedDVDID(movie)
	originalInfo := movie.Info
	fullValues := infoDict(movie, opts)
	fullLabel := strings.ToUpper(fullValues["label"])
	fullNFOTitle := ""
	if opts.NFOTitlePattern != "" {
		fullNFOTitle = formatPattern(opts.NFOTitlePattern, fullValues)
	}
	workingInfo := *originalInfo
	movie.Info = &workingInfo
	defer func() {
		movie.Info = originalInfo
	}()

	for {
		if err := assignNames(movie, opts); err != nil {
			return err
		}
		if !opts.MoveFiles || opts.LengthMaximum <= 0 || pathsWithinLimit(movie, opts) {
			originalInfo.Label = fullLabel
			originalInfo.NFOTitle = fullNFOTitle
			return nil
		}
		if !truncateTitle(movie.Info) {
			return fmt.Errorf("generated path exceeds length maximum %d", opts.LengthMaximum)
		}
	}
}

func preserveScannedDVDID(movie *model.Movie) {
	scanned := strings.TrimSpace(movie.DVDID)
	if scanned == "" {
		return
	}
	metadata := strings.TrimSpace(movie.Info.DVDID)
	metadataID := avid.GetID(metadata, nil)
	scannedID := avid.GetID(scanned, nil)
	if metadataID == "" || (scannedID != "" && !strings.EqualFold(metadataID, scannedID)) {
		movie.Info.DVDID = scanned
	}
}

func assignNames(movie *model.Movie, opts Options) error {
	values := infoDict(movie, opts)
	movie.Info.Label = strings.ToUpper(values["label"])
	if opts.NFOTitlePattern != "" {
		movie.Info.NFOTitle = formatPattern(opts.NFOTitlePattern, values)
	}

	attr := attrString(movie)
	pathValues := copyMap(values)
	pathValues["num"] = pathValues["num"] + attr

	var saveDir string
	var basename string
	if opts.MoveFiles {
		saveDir = filepath.Clean(formatPattern(opts.OutputFolderPattern, pathValues))
		basename = filepath.Clean(formatPattern(opts.BasenamePattern, pathValues))
	} else {
		saveDir = filepath.Dir(movie.Files[0])
		fileBase := filepath.Base(movie.Files[0])
		basename = strings.TrimSuffix(fileBase, filepath.Ext(fileBase))
	}

	movie.SaveDir = legalizePath(saveDir)
	movie.BaseName = legalizePath(basename)
	movie.NFOFile = filepath.Join(movie.SaveDir, legalizePath(formatPattern(opts.NFOBasenamePattern, pathValues))+".nfo")
	movie.FanartFile = filepath.Join(movie.SaveDir, legalizePath(formatPattern(opts.FanartBasenamePattern, pathValues))+".jpg")
	movie.PosterFile = filepath.Join(movie.SaveDir, legalizePath(formatPattern(opts.PosterBasenamePattern, pathValues))+".jpg")
	return nil
}

func pathsWithinLimit(movie *model.Movie, opts Options) bool {
	longestExtension := ""
	for _, source := range movie.Files {
		if extension := filepath.Ext(source); len(extension) > len(longestExtension) {
			longestExtension = extension
		}
	}
	path := filepath.Join(movie.SaveDir, movie.BaseName+longestExtension)
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return pathLen(abs, opts.LengthByByte) <= opts.LengthMaximum
}

func pathLen(path string, byByte bool) int {
	if byByte {
		return len([]byte(path))
	}
	return len([]rune(path))
}

func truncateTitle(info *model.MovieInfo) bool {
	if len([]rune(info.Title)) > 1 {
		info.Title = dropLastRune(info.Title)
		return true
	}
	if len([]rune(info.OriginalTitle)) > 1 {
		info.OriginalTitle = dropLastRune(info.OriginalTitle)
		return true
	}
	return false
}

func dropLastRune(value string) string {
	runes := []rune(value)
	if len(runes) <= 1 {
		return value
	}
	return string(runes[:len(runes)-1])
}

func infoDict(movie *model.Movie, opts Options) map[string]string {
	info := movie.Info
	num := info.DVDID
	if num == "" {
		num = info.CID
	}
	if num == "" {
		num = movie.DVDID
	}
	if num == "" {
		num = movie.CID
	}

	title := valueOr(info.Title, opts.DefaultTitle)
	rawTitle := info.OriginalTitle
	if rawTitle == "" {
		rawTitle = title
	}
	actressValues := append([]string(nil), info.Actress...)
	if opts.MaxActressCount > 0 && len(actressValues) > opts.MaxActressCount {
		actressValues = append(actressValues[:opts.MaxActressCount], "…")
	}
	actress := strings.Join(actressValues, ",")
	if actress == "" {
		actress = opts.DefaultActress
	}
	date := valueOr(info.PublishDate, "0000-00-00")
	year := strings.SplitN(date, "-", 2)[0]
	label := "---"
	if parts := strings.Split(num, "-"); len(parts) > 1 {
		label = parts[0]
	}
	censor := ""
	if len(opts.CensorText) >= 2 {
		index := 0
		if info.Uncensored != nil && *info.Uncensored {
			index = 1
		}
		censor = opts.CensorText[index]
	}

	values := map[string]string{
		"num":       num,
		"title":     title,
		"rawtitle":  rawTitle,
		"actress":   actress,
		"score":     valueOr(info.Score, "0"),
		"censor":    censor,
		"serial":    valueOr(info.Serial, opts.DefaultSeries),
		"director":  valueOr(info.Director, opts.DefaultDirector),
		"producer":  valueOr(info.Producer, opts.DefaultProducer),
		"publisher": valueOr(info.Publisher, opts.DefaultPublisher),
		"date":      date,
		"year":      year,
		"label":     label,
		"genre":     strings.Join(preferredGenres(info), ","),
	}
	for key, value := range values {
		values[key] = util.ReplaceIllegalChars(strings.TrimSpace(value), "")
	}
	return values
}

func preferredGenres(info *model.MovieInfo) []string {
	if len(info.GenreNorm) > 0 {
		return info.GenreNorm
	}
	return info.Genre
}

func attrString(movie *model.Movie) string {
	if len(movie.Files) != 1 {
		return ""
	}
	attr := util.DetectSpecialAttr(movie.Files[0], movie.DVDID)
	if attr == "" {
		return ""
	}
	return "-" + attr
}

func formatPattern(pattern string, values map[string]string) string {
	result := pattern
	for key, value := range values {
		result = strings.ReplaceAll(result, "{"+key+"}", value)
	}
	return result
}

func copyMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func legalizePath(path string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return -1
		}
		return r
	}, path)
}

func valueOr(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
