package sites

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/antchfx/htmlquery"
	"github.com/yuukiy/javsp-go/internal/avid"
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/model"
	"github.com/yuukiy/javsp-go/internal/util"
	"golang.org/x/net/html"
)

var fc2MetadataID = regexp.MustCompile(`(?i)^FC2-(?:PPV-)?(\d{5,7})$`)
var pageFC2ID = regexp.MustCompile(`(?i)FC2[ _-]*(?:PPV[ _-]*)?([0-9]+)`)
var leadingFC2ID = regexp.MustCompile(`(?i)^\s*[\[【(（]?FC2[ _-]*(?:PPV[ _-]*)?\d{5,7}\b[\]】)）]?[\s:：-]*`)

// structuredVideo reads only page metadata; it never follows media or actor links.
func structuredVideo(doc *html.Node, pageURL, requestedID string, requireSchemaID bool) (*model.MovieInfo, error) {
	heading := strings.Join(strings.Fields(firstText(doc, "//h1")), " ")
	requested := avid.GetID(requestedID, nil)
	headingID, valid := metadataPageID(heading, requested)
	if requested == "" || !valid || headingID != requested {
		return nil, crawler.ErrMovieNotFound
	}
	var videos []map[string]any
	for _, script := range htmlquery.Find(doc, "//script[@type='application/ld+json']") {
		var value any
		if json.Unmarshal([]byte(htmlquery.InnerText(script)), &value) == nil {
			videos = append(videos, collectVideoObjects(value)...)
		}
	}
	var candidates []map[string]any
	for _, video := range videos {
		id, valid := metadataPageID(jsonText(video["name"]), requested)
		if !valid || (requireSchemaID && id != requested) || (id != "" && id != requested) {
			continue
		}
		candidates = append(candidates, video)
	}
	if len(candidates) != 1 {
		if requireSchemaID && len(videos) > 0 && len(candidates) == 0 {
			return nil, crawler.ErrMovieNotFound
		}
		return nil, crawler.ErrWebsite
	}
	video := candidates[0]
	title := jsonText(video["name"])
	if requireSchemaID {
		title = heading
	}
	if id, valid := metadataPageID(title, requested); valid && id == requested {
		title = leadingFC2ID.ReplaceAllString(title, "")
	}
	if strings.IndexFunc(title, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) < 0 {
		title = ""
	}
	info := &model.MovieInfo{
		DVDID:       requested,
		URL:         pageURL,
		Title:       strings.TrimSpace(title),
		Plot:        jsonText(video["description"]),
		Cover:       metadataImageURL(pageURL, video["thumbnailUrl"]),
		PublishDate: metadataDate(jsonText(video["uploadDate"])),
		Producer:    jsonObjectName(video["publisher"]),
		Actress:     metadataNames(video["actor"]),
	}
	if duration := jsonText(video["duration"]); duration != "" {
		if minutes, err := util.StrftimeToMinutes(duration); err == nil && minutes > 0 {
			info.Duration = strconv.Itoa(minutes)
		}
	}
	return info, nil
}

// Webpage identities must match in full. Filename extraction deliberately accepts
// extra text, so it must not be used to truncate or pick one of several FC2 IDs.
func metadataPageID(text, requested string) (string, bool) {
	if !strings.HasPrefix(requested, "FC2-") {
		return avid.GetID(text, nil), true
	}
	var result string
	for _, match := range pageFC2ID.FindAllStringSubmatchIndex(text, -1) {
		start, end := match[0], match[1]
		digits := text[match[2]:match[3]]
		if len(digits) < 5 || len(digits) > 7 ||
			(start > 0 && asciiAlphanumeric(text[start-1])) ||
			(end < len(text) && asciiAlphanumeric(text[end])) {
			return "", false
		}
		id := "FC2-" + digits
		if result != "" && result != id {
			return "", false
		}
		result = id
	}
	return result, true
}

func asciiAlphanumeric(ch byte) bool {
	return ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z'
}

func collectVideoObjects(value any) []map[string]any {
	var result []map[string]any
	switch value := value.(type) {
	case []any:
		for _, item := range value {
			result = append(result, collectVideoObjects(item)...)
		}
	case map[string]any:
		if jsonText(value["@type"]) == "VideoObject" {
			result = append(result, value)
		}
		if graph, ok := value["@graph"]; ok {
			result = append(result, collectVideoObjects(graph)...)
		}
	}
	return result
}

func jsonText(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func jsonObjectName(value any) string {
	if object, ok := value.(map[string]any); ok {
		return jsonText(object["name"])
	}
	return jsonText(value)
}

func metadataNames(value any) []string {
	items, ok := value.([]any)
	if !ok {
		items = []any{value}
	}
	var names []string
	for _, item := range items {
		if name := jsonObjectName(item); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func metadataImageURL(base string, value any) string {
	items, ok := value.([]any)
	if !ok {
		items = []any{value}
	}
	for _, item := range items {
		ref := jsonText(item)
		if object, ok := item.(map[string]any); ok {
			ref = jsonText(object["url"])
		}
		if ref == "" {
			continue
		}
		parsed, err := url.Parse(absURL(base, ref))
		if err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil {
			return parsed.String()
		}
	}
	return ""
}

func metadataDate(value string) string {
	if len(value) < 10 {
		return ""
	}
	date := strings.ReplaceAll(value[:10], "/", "-")
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return ""
	}
	return date
}
