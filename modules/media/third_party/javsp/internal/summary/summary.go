package summary

import (
	"regexp"
	"strings"

	"github.com/yuukiy/javsp-go/internal/avid"
	"github.com/yuukiy/javsp-go/internal/genre"
	"github.com/yuukiy/javsp-go/internal/model"
	"github.com/yuukiy/javsp-go/internal/util"
)

type Result struct {
	CrawlerID string
	Info      *model.MovieInfo
}

type Options struct {
	RequiredKeys            []string
	UseJavDBCover           string
	RespectSiteAvid         bool
	RemoveTrailingActorName bool
	ActressAliases          map[string][]string
	GenreMaps               map[string]genre.Map
}

func Merge(movie *model.Movie, results []Result, opts Options) (bool, error) {
	finalInfo := &model.MovieInfo{
		DVDID: movie.DVDID,
		CID:   movie.CID,
	}

	infos := cloneResults(results)
	applyGenreMaps(infos, opts.GenreMaps)
	if javdb := findResult(infos, "javdb"); javdb != nil && len(javdb.Info.Genre) > 0 {
		finalInfo.Genre = append([]string(nil), javdb.Info.Genre...)
	}

	if opts.RemoveTrailingActorName {
		for _, result := range infos {
			result.Info.Title = removeTrailingActorInTitle(result.Info.Title, result.Info.Actress)
		}
	}

	var covers []string
	var bigCovers []string
	for _, result := range infos {
		info := result.Info
		if info == nil {
			continue
		}
		if info.Cover != "" {
			covers = appendUnique(covers, info.Cover)
		}
		if info.BigCover != "" {
			bigCovers = appendUnique(bigCovers, info.BigCover)
		}
		absorbFields(finalInfo, info)
	}

	if opts.RespectSiteAvid {
		applySiteAvid(movie, finalInfo, infos)
	}

	if javdb := findResult(infos, "javdb"); javdb != nil && javdb.Info.Cover != "" {
		switch opts.UseJavDBCover {
		case "fallback":
			covers = moveToEnd(covers, javdb.Info.Cover)
		case "no":
			covers = removeValue(covers, javdb.Info.Cover)
		}
	}

	finalInfo.Covers = covers
	finalInfo.BigCovers = bigCovers
	if len(covers) > 0 {
		finalInfo.Cover = covers[0]
	}
	if len(bigCovers) > 0 {
		finalInfo.BigCover = bigCovers[0]
	}

	if finalInfo.Genre == nil {
		finalInfo.Genre = []string{}
	}
	attr := ""
	if len(movie.Files) == 1 {
		attr = util.DetectSpecialAttr(movie.Files[0], movie.DVDID)
	}
	if strings.Contains(attr, "C") {
		finalInfo.Genre = append(finalInfo.Genre, "内嵌字幕")
	}
	if strings.Contains(attr, "U") {
		finalInfo.Genre = append(finalInfo.Genre, "无码流出/破解")
	}

	if len(opts.ActressAliases) > 0 && len(finalInfo.ActressPics) > 0 {
		finalInfo.Actress = normalizeNames(finalInfo.Actress, opts.ActressAliases)
		finalInfo.ActressPics = normalizePicNames(finalInfo.ActressPics, opts.ActressAliases)
	}

	for _, key := range opts.RequiredKeys {
		if !hasRequiredKey(finalInfo, key) {
			return false, nil
		}
	}

	movie.Info = finalInfo
	return true, nil
}

func cloneResults(results []Result) []Result {
	out := make([]Result, 0, len(results))
	for _, result := range results {
		if result.Info == nil {
			out = append(out, result)
			continue
		}
		info := *result.Info
		info.Covers = append([]string(nil), result.Info.Covers...)
		info.BigCovers = append([]string(nil), result.Info.BigCovers...)
		info.Genre = append([]string(nil), result.Info.Genre...)
		info.GenreID = append([]string(nil), result.Info.GenreID...)
		info.GenreNorm = append([]string(nil), result.Info.GenreNorm...)
		info.Magnet = append([]string(nil), result.Info.Magnet...)
		info.Actress = append([]string(nil), result.Info.Actress...)
		info.PreviewPics = append([]string(nil), result.Info.PreviewPics...)
		if result.Info.ActressPics != nil {
			info.ActressPics = make(map[string]string, len(result.Info.ActressPics))
			for key, value := range result.Info.ActressPics {
				info.ActressPics[key] = value
			}
		}
		out = append(out, Result{CrawlerID: result.CrawlerID, Info: &info})
	}
	return out
}

func applyGenreMaps(results []Result, genreMaps map[string]genre.Map) {
	if len(genreMaps) == 0 {
		return
	}
	for _, result := range results {
		if result.Info == nil || len(result.Info.GenreID) == 0 {
			continue
		}
		mapping, ok := genreMaps[result.CrawlerID]
		if !ok {
			continue
		}
		result.Info.GenreNorm = mapping.Map(result.Info.GenreID)
	}
}

func findResult(results []Result, crawlerID string) *Result {
	for i := range results {
		if results[i].CrawlerID == crawlerID && results[i].Info != nil {
			return &results[i]
		}
	}
	return nil
}

func absorbFields(dst *model.MovieInfo, src *model.MovieInfo) {
	if dst.URL == "" {
		dst.URL = src.URL
	}
	if dst.Plot == "" {
		dst.Plot = src.Plot
	}
	if len(dst.Genre) == 0 {
		dst.Genre = append([]string(nil), src.Genre...)
	}
	if len(dst.GenreID) == 0 {
		dst.GenreID = append([]string(nil), src.GenreID...)
	}
	if len(dst.GenreNorm) == 0 {
		dst.GenreNorm = append([]string(nil), src.GenreNorm...)
	}
	if dst.Score == "" {
		dst.Score = src.Score
	}
	if dst.Title == "" {
		dst.Title = src.Title
	}
	if dst.OriginalTitle == "" {
		dst.OriginalTitle = src.OriginalTitle
	}
	if len(dst.Magnet) == 0 {
		dst.Magnet = append([]string(nil), src.Magnet...)
	}
	if dst.Serial == "" {
		dst.Serial = src.Serial
	}
	if len(dst.Actress) == 0 {
		dst.Actress = append([]string(nil), src.Actress...)
	}
	if len(dst.ActressPics) == 0 && len(src.ActressPics) > 0 {
		dst.ActressPics = make(map[string]string, len(src.ActressPics))
		for key, value := range src.ActressPics {
			dst.ActressPics[key] = value
		}
	}
	if dst.Director == "" {
		dst.Director = src.Director
	}
	if dst.Duration == "" {
		dst.Duration = src.Duration
	}
	if dst.Producer == "" {
		dst.Producer = src.Producer
	}
	if dst.Publisher == "" {
		dst.Publisher = src.Publisher
	}
	if dst.Uncensored == nil && src.Uncensored != nil {
		value := *src.Uncensored
		dst.Uncensored = &value
	}
	if dst.PublishDate == "" {
		dst.PublishDate = src.PublishDate
	}
	if len(dst.PreviewPics) == 0 {
		dst.PreviewPics = append([]string(nil), src.PreviewPics...)
	}
	if dst.PreviewVideo == "" {
		dst.PreviewVideo = src.PreviewVideo
	}
}

func applySiteAvid(movie *model.Movie, finalInfo *model.MovieInfo, results []Result) {
	counts := make(map[string]int)
	var order []string
	for _, result := range results {
		if result.Info == nil || result.Info.Title == "" {
			continue
		}
		id := result.Info.DVDID
		if movie.DVDID == "" {
			id = result.Info.CID
		}
		if id == "" {
			continue
		}
		if movie.DVDID != "" && avid.GetID(id, nil) == "" {
			continue
		}
		if _, exists := counts[id]; !exists {
			order = append(order, id)
		}
		counts[id]++
	}
	if len(counts) == 0 {
		return
	}
	best := order[0]
	for _, id := range order[1:] {
		if counts[id] > counts[best] {
			best = id
		}
	}
	if movie.DVDID != "" {
		finalInfo.DVDID = best
	} else {
		finalInfo.CID = best
	}
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func moveToEnd(values []string, value string) []string {
	values = removeValue(values, value)
	return append(values, value)
}

func removeValue(values []string, value string) []string {
	out := values[:0]
	for _, existing := range values {
		if existing != value {
			out = append(out, existing)
		}
	}
	return out
}

func normalizeNames(values []string, aliases map[string][]string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, resolveAlias(value, aliases))
	}
	return out
}

func normalizePicNames(values map[string]string, aliases map[string][]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[resolveAlias(key, aliases)] = value
	}
	return out
}

func resolveAlias(name string, aliases map[string][]string) string {
	for fixed, names := range aliases {
		for _, alias := range names {
			if name == alias {
				return fixed
			}
		}
	}
	return name
}

func hasRequiredKey(info *model.MovieInfo, key string) bool {
	switch key {
	case "dvdid":
		return info.DVDID != ""
	case "cid":
		return info.CID != ""
	case "url":
		return info.URL != ""
	case "plot":
		return info.Plot != ""
	case "cover":
		return info.Cover != ""
	case "big_cover":
		return info.BigCover != ""
	case "genre":
		return len(info.Genre) > 0
	case "genre_id":
		return len(info.GenreID) > 0
	case "genre_norm":
		return len(info.GenreNorm) > 0
	case "score":
		return info.Score != ""
	case "title":
		return info.Title != ""
	case "ori_title":
		return info.OriginalTitle != ""
	case "magnet":
		return len(info.Magnet) > 0
	case "serial":
		return info.Serial != ""
	case "actress":
		return len(info.Actress) > 0
	case "actress_pics":
		return len(info.ActressPics) > 0
	case "director":
		return info.Director != ""
	case "duration":
		return info.Duration != ""
	case "producer":
		return info.Producer != ""
	case "publisher":
		return info.Publisher != ""
	case "uncensored":
		return info.Uncensored != nil
	case "publish_date":
		return info.PublishDate != ""
	case "preview_pics":
		return len(info.PreviewPics) > 0
	case "preview_video":
		return info.PreviewVideo != ""
	default:
		return true
	}
}

func removeTrailingActorInTitle(title string, actors []string) string {
	if title == "" || len(actors) == 0 {
		return title
	}
	var escaped []string
	for _, actor := range actors {
		if actor != "" {
			escaped = append(escaped, regexp.QuoteMeta(actor))
		}
	}
	if len(escaped) == 0 {
		return title
	}
	delimiters := regexp.QuoteMeta("-xX &·,;　＆・，；")
	pattern := regexp.MustCompile(`^(.*?)([` + delimiters + `]{1,3}(` + strings.Join(escaped, "|") + `))+$`)
	if match := pattern.FindStringSubmatch(title); len(match) > 1 {
		return match[1]
	}
	return title
}
