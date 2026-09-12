package service

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	aniDBTitleURL        = "https://anidb.net/api/anime-titles.dat.gz"
	aniDBCacheLifetime   = 24 * time.Hour
	maxAnimeVideos       = 5000
	maxAniDBTitles       = 250000
	maxAniDBCompressed   = 32 << 20
	maxAniDBDecompressed = 128 << 20
)

var (
	leadingAnimeTagPattern = regexp.MustCompile(`^\s*\[([^\]]+)\]\s*`)
	dateTagPattern         = regexp.MustCompile(`^\d{6}$`)
	episodePattern         = regexp.MustCompile(`(?i)\s+(?:第\s*([0-9一二三四五六七八九十百〇零]+)\s*[話话集巻卷]|[#＃]\s*([0-9]+)|(?:episode|ep\.?)[ _-]*([0-9]+))(?:\s|$)`)
	volumePattern          = regexp.MustCompile(`\s+(上巻|下巻|前編|後編)(?:\s|$)`)
	languageSuffixPattern  = regexp.MustCompile(`(?i)(?:\.(?:chs|cht|sc|tc|gb|big5|jpn|jp|eng|en))+$`)
)

type AniDBTitle struct {
	AID      int    `json:"aid"`
	Kind     int    `json:"kind"`
	Language string `json:"language"`
	Title    string `json:"title"`
}

type AniDBCacheInfo struct {
	Source     string `json:"source"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	TitleCount int    `json:"title_count"`
	Stale      bool   `json:"stale,omitempty"`
}

type aniDBTitleProvider interface {
	Load(context.Context, bool) ([]AniDBTitle, AniDBCacheInfo, error)
}

type aniDBTitleCache struct {
	path   string
	url    string
	client *http.Client
	now    func() time.Time
}

func newAniDBTitleCache(dataDir string) *aniDBTitleCache {
	return &aniDBTitleCache{
		path:   filepath.Join(dataDir, "anidb", "anime-titles.dat.gz"),
		url:    aniDBTitleURL,
		client: &http.Client{Timeout: 45 * time.Second},
		now:    time.Now,
	}
}

func (cache *aniDBTitleCache) Load(ctx context.Context, force bool) ([]AniDBTitle, AniDBCacheInfo, error) {
	info, statErr := os.Stat(cache.path)
	if !force && statErr == nil && cache.now().Sub(info.ModTime()) < aniDBCacheLifetime {
		return cache.read(info, false)
	}
	if err := cache.download(ctx); err != nil {
		if statErr == nil {
			titles, cached, readErr := cache.read(info, true)
			if readErr == nil {
				return titles, cached, nil
			}
		}
		return nil, AniDBCacheInfo{}, err
	}
	info, err := os.Stat(cache.path)
	if err != nil {
		return nil, AniDBCacheInfo{}, err
	}
	return cache.read(info, false)
}

func (cache *aniDBTitleCache) download(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, cache.url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "fnos-media-acquisition-center/"+Version)
	response, err := cache.client.Do(request)
	if err != nil {
		return fmt.Errorf("更新 AniDB 标题库失败：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("更新 AniDB 标题库失败：HTTP %d", response.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(cache.path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(cache.path), ".anime-titles-*.gz")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	written, copyErr := io.Copy(temporary, io.LimitReader(response.Body, maxAniDBCompressed+1))
	closeErr := temporary.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > maxAniDBCompressed {
		return fmt.Errorf("AniDB 标题库压缩文件超过安全上限")
	}
	if _, err := parseAniDBTitleFile(temporaryPath); err != nil {
		return fmt.Errorf("AniDB 标题库格式无效：%w", err)
	}
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return err
	}
	return os.Rename(temporaryPath, cache.path)
}

func (cache *aniDBTitleCache) read(info fs.FileInfo, stale bool) ([]AniDBTitle, AniDBCacheInfo, error) {
	titles, err := parseAniDBTitleFile(cache.path)
	if err != nil {
		return nil, AniDBCacheInfo{}, err
	}
	return titles, AniDBCacheInfo{
		Source:     aniDBTitleURL,
		UpdatedAt:  info.ModTime().Format(time.RFC3339),
		TitleCount: len(titles),
		Stale:      stale,
	}, nil
}

func parseAniDBTitleFile(path string) ([]AniDBTitle, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer compressed.Close()
	scanner := bufio.NewScanner(io.LimitReader(compressed, maxAniDBDecompressed))
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	titles := make([]AniDBTitle, 0, 100000)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue
		}
		aid, aidErr := strconv.Atoi(parts[0])
		kind, kindErr := strconv.Atoi(parts[1])
		if aidErr != nil || kindErr != nil || aid < 1 || strings.TrimSpace(parts[3]) == "" {
			continue
		}
		titles = append(titles, AniDBTitle{AID: aid, Kind: kind, Language: parts[2], Title: strings.TrimSpace(parts[3])})
		if len(titles) > maxAniDBTitles {
			return nil, fmt.Errorf("标题数量超过安全上限")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(titles) == 0 {
		return nil, fmt.Errorf("标题库为空")
	}
	return titles, nil
}

type parsedAnimeFilename struct {
	Title       string
	Studio      string
	ReleaseDate string
	Episode     int
}

func parseAnimeFilename(filename string) parsedAnimeFilename {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	base = languageSuffixPattern.ReplaceAllString(base, "")
	parsed := parsedAnimeFilename{}
	tags := make([]string, 0, 3)
	for {
		match := leadingAnimeTagPattern.FindStringSubmatch(base)
		if match == nil {
			break
		}
		tags = append(tags, strings.TrimSpace(match[1]))
		base = strings.TrimSpace(base[len(match[0]):])
	}
	for _, tag := range tags {
		if dateTagPattern.MatchString(tag) {
			if parsed.ReleaseDate == "" {
				if date, err := time.Parse("060102", tag); err == nil {
					parsed.ReleaseDate = date.Format("2006-01-02")
				}
			}
			continue
		}
		parsed.Studio = tag
	}
	if location := episodePattern.FindStringSubmatchIndex(base); location != nil {
		parsed.Title = strings.TrimSpace(base[:location[0]])
		for group := 2; group+1 < len(location); group += 2 {
			if location[group] >= 0 {
				parsed.Episode = parseEpisodeNumber(base[location[group]:location[group+1]])
				break
			}
		}
	} else if location := volumePattern.FindStringSubmatchIndex(base); location != nil {
		parsed.Title = strings.TrimSpace(base[:location[0]])
		marker := base[location[2]:location[3]]
		if marker == "上巻" || marker == "前編" {
			parsed.Episode = 1
		} else {
			parsed.Episode = 2
		}
	} else {
		parsed.Title = strings.TrimSpace(base)
	}
	parsed.Title = strings.Trim(parsed.Title, " ._-　")
	return parsed
}

func parseEpisodeNumber(value string) int {
	if number, err := strconv.Atoi(value); err == nil {
		return number
	}
	digits := map[rune]int{'〇': 0, '零': 0, '一': 1, '二': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	total, current := 0, 0
	for _, character := range value {
		switch character {
		case '十':
			if current == 0 {
				current = 1
			}
			total += current * 10
			current = 0
		case '百':
			if current == 0 {
				current = 1
			}
			total += current * 100
			current = 0
		default:
			if digit, ok := digits[character]; ok {
				current = digit
			}
		}
	}
	return total + current
}

type AniDBMatch struct {
	AID        int    `json:"aid"`
	Title      string `json:"title"`
	Language   string `json:"language"`
	Method     string `json:"method"`
	Confidence int    `json:"confidence"`
}

type aniDBMatchCandidate struct {
	title      AniDBTitle
	normalized string
}

type aniDBMatcher struct {
	exact  map[string]AniDBTitle
	prefix []aniDBMatchCandidate
}

func newAniDBMatcher(titles []AniDBTitle) aniDBMatcher {
	matcher := aniDBMatcher{exact: make(map[string]AniDBTitle), prefix: make([]aniDBMatchCandidate, 0, len(titles))}
	for _, title := range titles {
		normalized := normalizeAnimeTitle(title.Title)
		if len([]rune(normalized)) < 4 {
			continue
		}
		if current, exists := matcher.exact[normalized]; !exists || titlePriority(title) < titlePriority(current) {
			matcher.exact[normalized] = title
		}
		matcher.prefix = append(matcher.prefix, aniDBMatchCandidate{title: title, normalized: normalized})
	}
	sort.SliceStable(matcher.prefix, func(i, j int) bool {
		return len([]rune(matcher.prefix[i].normalized)) > len([]rune(matcher.prefix[j].normalized))
	})
	return matcher
}

func titlePriority(title AniDBTitle) int {
	priority := title.Kind * 10
	if title.Language == "ja" {
		priority -= 3
	}
	if title.Language == "x-jat" {
		priority -= 2
	}
	return priority
}

func normalizeAnimeTitle(value string) string {
	value = strings.ToLower(norm.NFKC.String(value))
	return strings.Map(func(character rune) rune {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			return character
		}
		return -1
	}, value)
}

func (matcher aniDBMatcher) Match(value string) AniDBMatch {
	normalized := normalizeAnimeTitle(value)
	if len([]rune(normalized)) < 4 {
		return AniDBMatch{}
	}
	if title, ok := matcher.exact[normalized]; ok {
		return AniDBMatch{AID: title.AID, Title: title.Title, Language: title.Language, Method: "exact", Confidence: 100}
	}
	for _, candidate := range matcher.prefix {
		if strings.HasPrefix(normalized, candidate.normalized) {
			return AniDBMatch{AID: candidate.title.AID, Title: candidate.title.Title, Language: candidate.title.Language, Method: "prefix", Confidence: 92}
		}
	}
	return AniDBMatch{}
}

type AnimeScanItem struct {
	FilePath      string `json:"file_path"`
	FileName      string `json:"file_name"`
	ParsedTitle   string `json:"parsed_title"`
	Studio        string `json:"studio,omitempty"`
	ReleaseDate   string `json:"release_date,omitempty"`
	Episode       int    `json:"episode,omitempty"`
	ExistingNFO   bool   `json:"existing_nfo"`
	AID           int    `json:"aid,omitempty"`
	MatchedTitle  string `json:"matched_title,omitempty"`
	MatchLanguage string `json:"match_language,omitempty"`
	MatchMethod   string `json:"match_method,omitempty"`
	Confidence    int    `json:"confidence"`
}

type AnimeScanResult struct {
	Directory string          `json:"directory"`
	Total     int             `json:"total"`
	Matched   int             `json:"matched"`
	Review    int             `json:"review"`
	Unmatched int             `json:"unmatched"`
	Truncated bool            `json:"truncated"`
	Cache     AniDBCacheInfo  `json:"cache"`
	Items     []AnimeScanItem `json:"items"`
}

func scanAnimeDirectory(root string, titles []AniDBTitle, cache AniDBCacheInfo) (AnimeScanResult, error) {
	canonicalRoot, err := canonicalDirectory(root)
	if err != nil {
		return AnimeScanResult{}, fmt.Errorf("番剧扫描目录不可访问")
	}
	matcher := newAniDBMatcher(titles)
	result := AnimeScanResult{Directory: canonicalRoot, Cache: cache, Items: make([]AnimeScanItem, 0, 512)}
	err = filepath.WalkDir(canonicalRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if path != canonicalRoot && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "@eaDir" || entry.Name() == "#recycle") {
				return filepath.SkipDir
			}
			return nil
		}
		if result.Total >= maxAnimeVideos {
			result.Truncated = true
			return fs.SkipAll
		}
		if !isAnimeVideo(path) {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			return infoErr
		}
		parsed := parseAnimeFilename(entry.Name())
		match := matcher.Match(parsed.Title)
		nfoPath := strings.TrimSuffix(path, filepath.Ext(path)) + ".nfo"
		_, nfoErr := os.Lstat(nfoPath)
		item := AnimeScanItem{
			FilePath: path, FileName: entry.Name(), ParsedTitle: parsed.Title, Studio: parsed.Studio,
			ReleaseDate: parsed.ReleaseDate, Episode: parsed.Episode, ExistingNFO: nfoErr == nil,
			AID: match.AID, MatchedTitle: match.Title, MatchLanguage: match.Language,
			MatchMethod: match.Method, Confidence: match.Confidence,
		}
		result.Items = append(result.Items, item)
		result.Total++
		switch {
		case match.Confidence >= 90:
			result.Matched++
		case match.AID > 0:
			result.Review++
		default:
			result.Unmatched++
		}
		return nil
	})
	return result, err
}

func isAnimeVideo(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".mkv", ".avi", ".mov", ".m4v", ".wmv", ".flv", ".ts", ".m2ts", ".webm":
		return true
	default:
		return false
	}
}

type animeNFO struct {
	XMLName       xml.Name      `xml:"movie"`
	Title         string        `xml:"title"`
	OriginalTitle string        `xml:"originaltitle,omitempty"`
	Year          string        `xml:"year,omitempty"`
	Studio        string        `xml:"studio,omitempty"`
	Episode       int           `xml:"episode,omitempty"`
	UniqueID      animeUniqueID `xml:"uniqueid"`
	Genres        []string      `xml:"genre"`
}

type animeUniqueID struct {
	Type    string `xml:"type,attr"`
	Default bool   `xml:"default,attr"`
	Value   int    `xml:",chardata"`
}

type animeNFORequest struct {
	Directory string          `json:"directory"`
	Overwrite bool            `json:"overwrite"`
	Items     []AnimeScanItem `json:"items"`
}

type animeNFOResult struct {
	Created int      `json:"created"`
	Skipped int      `json:"skipped"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors"`
}

func writeAnimeNFOs(request animeNFORequest) (animeNFOResult, error) {
	root, err := canonicalDirectory(request.Directory)
	if err != nil {
		return animeNFOResult{}, fmt.Errorf("番剧扫描目录不可访问")
	}
	if len(request.Items) > maxAnimeVideos {
		return animeNFOResult{}, fmt.Errorf("单次写入数量超过安全上限")
	}
	result := animeNFOResult{Errors: []string{}}
	for _, item := range request.Items {
		if item.AID < 1 || item.Confidence < 90 || strings.TrimSpace(item.MatchedTitle) == "" {
			result.Skipped++
			continue
		}
		video, resolveErr := filepath.EvalSymlinks(filepath.Clean(item.FilePath))
		if resolveErr != nil || !pathWithinRoot(root, video) || !isAnimeVideo(video) {
			result.Failed++
			result.Errors = append(result.Errors, filepath.Base(item.FilePath)+"：文件路径无效")
			continue
		}
		info, statErr := os.Lstat(video)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			result.Failed++
			result.Errors = append(result.Errors, filepath.Base(item.FilePath)+"：不是可写入元数据的普通视频文件")
			continue
		}
		year := ""
		if len(item.ReleaseDate) >= 4 {
			year = item.ReleaseDate[:4]
		}
		document := animeNFO{
			Title: item.MatchedTitle, OriginalTitle: item.MatchedTitle, Year: year,
			Studio: item.Studio, Episode: item.Episode,
			UniqueID: animeUniqueID{Type: "anidb", Default: true, Value: item.AID},
			Genres:   []string{"动画"},
		}
		content, marshalErr := xml.MarshalIndent(document, "", "  ")
		if marshalErr != nil {
			return result, marshalErr
		}
		nfoPath := strings.TrimSuffix(video, filepath.Ext(video)) + ".nfo"
		flags := os.O_WRONLY | os.O_CREATE
		if request.Overwrite {
			flags |= os.O_TRUNC
		} else {
			flags |= os.O_EXCL
		}
		file, openErr := os.OpenFile(nfoPath, flags, 0o600)
		if errors.Is(openErr, os.ErrExist) {
			result.Skipped++
			continue
		}
		if openErr != nil {
			result.Failed++
			result.Errors = append(result.Errors, filepath.Base(item.FilePath)+"："+openErr.Error())
			continue
		}
		_, writeErr := file.Write(append([]byte(xml.Header), append(content, '\n')...))
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			_ = os.Remove(nfoPath)
			result.Failed++
			if writeErr == nil {
				writeErr = closeErr
			}
			result.Errors = append(result.Errors, filepath.Base(item.FilePath)+"："+writeErr.Error())
			continue
		}
		result.Created++
	}
	return result, nil
}
