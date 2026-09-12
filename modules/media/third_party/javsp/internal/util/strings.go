package util

import (
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

var specialAttrPattern = regexp.MustCompile(`(?i)(uncen(sor(ed)?)?([- _\s]*leak(ed)?)?|[无無][码碼](流出|破解))`)
var parentDirPattern = regexp.MustCompile(`\.{2,}`)

const punctuationRunes = " 　" +
	"!\"#%&'()*,-./:;?@[\\]_{}" +
	"¡§«¶·»¿" +
	";·" +
	"‐‑‒–—―‖‗‘’‚‛“”„‟†‡•‣․‥…‧‰‱′″‴‵‶‷‸‹›※‼‽‾‿⁀⁁⁂⁃⁅⁆⁇⁈⁉⁊⁋⁌⁍⁎⁏⁐⁑⁓⁔⁕⁖⁗⁘⁙⁚⁛⁜⁝⁞" +
	"、。〃〈〉《》「」『』【】〔〕〖〗〘〙〚〛〜〝〞〟〰〽" +
	"゠・" +
	"︐︑︒︓︔︕︖︗︘︙" +
	"︰︱︲︳︴︵︶︷︸︹︺︻︼︽︾︿﹀﹁﹂﹃﹄﹅﹆﹇﹈﹉﹊﹋﹌﹍﹎﹏" +
	"！＂＃％＆＇（）＊，－．／：；？＠［＼］＿｛｝｟｠｡｢｣､･"

func RegexEscape(s string) string {
	return regexp.QuoteMeta(s)
}

func StrftimeToMinutes(s string) (int, error) {
	parts := strings.Split(s, ":")
	values := make([]int, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			return 0, err
		}
		values = append(values, value)
	}

	switch len(values) {
	case 2:
		return values[0] + int(math.RoundToEven(float64(values[1])/60)), nil
	case 3:
		return values[0]*60 + values[1] + int(math.RoundToEven(float64(values[2])/60)), nil
	default:
		return 0, fmt.Errorf("cannot convert %q to minutes", s)
	}
}

func DetectSpecialAttr(path string, avid string) string {
	result := make(map[rune]bool)
	base := strings.ToUpper(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	if specialAttrPattern.MatchString(base) {
		result['U'] = true
	}

	postfixParts := strings.Split(base, "-")
	postfix := postfixParts[len(postfixParts)-1]
	switch postfix {
	case "U", "C", "UC":
		for _, r := range postfix {
			result[r] = true
		}
	default:
		if avid != "" {
			escaped := RegexEscape(avid)
			escaped = regexp.MustCompile(`[_-]`).ReplaceAllString(escaped, `[_-]*`)
			pattern := regexp.MustCompile(`(?i)` + escaped + `(UC|U|C)\b`)
			if match := pattern.FindStringSubmatch(base); len(match) > 1 {
				for _, r := range strings.ToUpper(match[1]) {
					result[r] = true
				}
			}
		}
	}

	attrs := make([]string, 0, len(result))
	for r := range result {
		attrs = append(attrs, string(r))
	}
	sort.Sort(sort.Reverse(sort.StringSlice(attrs)))
	return strings.Join(attrs, "")
}

func SplitByPunc(s string) []string {
	if s == "" {
		return []string{""}
	}
	punc := make(map[rune]bool)
	for _, r := range punctuationRunes {
		punc[r] = true
	}

	var out []string
	start := 0
	for idx, r := range s {
		if punc[r] {
			end := idx + len(string(r))
			out = append(out, s[start:end])
			start = end
		}
	}
	out = append(out, s[start:])
	return out
}

func ReplaceIllegalChars(name string, goos string) string {
	if goos == "" {
		goos = runtime.GOOS
	}
	switch goos {
	case "windows":
		replacements := map[string]string{
			"<":  "❮",
			">":  "❯",
			":":  "：",
			"\"": "″",
			"/":  "／",
			"\\": "＼",
			"|":  "｜",
			"?":  "？",
			"*":  "꘎",
		}
		for old, replacement := range replacements {
			name = strings.ReplaceAll(name, old, replacement)
		}
	case "darwin":
		name = strings.ReplaceAll(name, ":", "：")
	default:
		name = strings.ReplaceAll(name, "/", "／")
	}
	if strings.Contains(name, "..") {
		name = parentDirPattern.ReplaceAllString(name, "…")
	}
	return name
}
