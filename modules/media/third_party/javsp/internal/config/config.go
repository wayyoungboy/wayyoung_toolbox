package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Scanner    Scanner    `yaml:"scanner"`
	Network    Network    `yaml:"network"`
	Crawler    Crawler    `yaml:"crawler"`
	Summarizer Summarizer `yaml:"summarizer"`
	Translator Translator `yaml:"translator"`
	Other      Other      `yaml:"other"`
}

type Scanner struct {
	IgnoredIDPattern         []string `yaml:"ignored_id_pattern"`
	InputDirectory           *string  `yaml:"input_directory"`
	FilenameExtensions       []string `yaml:"filename_extensions"`
	IgnoredFolderNamePattern []string `yaml:"ignored_folder_name_pattern"`
	MinimumSize              string   `yaml:"minimum_size"`
	SkipNFODir               bool     `yaml:"skip_nfo_dir"`
	Manual                   bool     `yaml:"manual"`
}

type Network struct {
	ProxyServer *string           `yaml:"proxy_server"`
	ProxyFree   map[string]string `yaml:"proxy_free"`
	Retry       int               `yaml:"retry"`
	Timeout     string            `yaml:"timeout"`
}

type CrawlerSelection struct {
	Normal []string `yaml:"normal"`
	FC2    []string `yaml:"fc2"`
	CID    []string `yaml:"cid"`
	Getchu []string `yaml:"getchu"`
	Gyutto []string `yaml:"gyutto"`
}

type Crawler struct {
	Selection            CrawlerSelection `yaml:"selection"`
	RequiredKeys         []string         `yaml:"required_keys"`
	MovieConcurrency     int              `yaml:"movie_concurrency"`
	Hardworking          bool             `yaml:"hardworking"`
	RespectSiteAvid      bool             `yaml:"respect_site_avid"`
	FC2FanLocalPath      *string          `yaml:"fc2fan_local_path"`
	SleepAfterScraping   string           `yaml:"sleep_after_scraping"`
	UseJavDBCover        string           `yaml:"use_javdb_cover"`
	NormalizeActressName bool             `yaml:"normalize_actress_name"`
}

type Summarizer struct {
	MoveFiles                   bool                  `yaml:"move_files"`
	Path                        PathSummarizer        `yaml:"path"`
	Title                       TitleSummarizer       `yaml:"title"`
	Default                     DefaultValues         `yaml:"default"`
	NFO                         NFOSummarizer         `yaml:"nfo"`
	CensorOptionsRepresentation []string              `yaml:"censor_options_representation"`
	Cover                       CoverSummarizer       `yaml:"cover"`
	Fanart                      FanartSummarizer      `yaml:"fanart"`
	ExtraFanarts                ExtraFanartSummarizer `yaml:"extra_fanarts"`
}

type PathSummarizer struct {
	OutputFolderPattern string `yaml:"output_folder_pattern"`
	BasenamePattern     string `yaml:"basename_pattern"`
	LengthMaximum       int    `yaml:"length_maximum"`
	LengthByByte        bool   `yaml:"length_by_byte"`
	MaxActressCount     int    `yaml:"max_actress_count"`
	HardLink            bool   `yaml:"hard_link"`
}

type TitleSummarizer struct {
	RemoveTrailingActorName bool `yaml:"remove_trailing_actor_name"`
}

type DefaultValues struct {
	Title     string `yaml:"title"`
	Actress   string `yaml:"actress"`
	Series    string `yaml:"series"`
	Director  string `yaml:"director"`
	Producer  string `yaml:"producer"`
	Publisher string `yaml:"publisher"`
}

type NFOSummarizer struct {
	BasenamePattern    string   `yaml:"basename_pattern"`
	TitlePattern       string   `yaml:"title_pattern"`
	CustomGenresFields []string `yaml:"custom_genres_fields"`
	CustomTagsFields   []string `yaml:"custom_tags_fields"`
}

type CoverSummarizer struct {
	BasenamePattern string    `yaml:"basename_pattern"`
	HighRes         bool      `yaml:"highres"`
	AddLabel        bool      `yaml:"add_label"`
	Crop            CoverCrop `yaml:"crop"`
}

type CoverCrop struct {
	OnIDPattern []string `yaml:"on_id_pattern"`
	Engine      any      `yaml:"engine"`
}

type FanartSummarizer struct {
	BasenamePattern string `yaml:"basename_pattern"`
}

type ExtraFanartSummarizer struct {
	Enabled       bool   `yaml:"enabled"`
	ScrapInterval string `yaml:"scrap_interval"`
}

type Translator struct {
	Engine any             `yaml:"engine"`
	Fields TranslateFields `yaml:"fields"`
}

type TranslateFields struct {
	Title bool `yaml:"title"`
	Plot  bool `yaml:"plot"`
}

type Other struct {
	Interactive bool `yaml:"interactive"`
	CheckUpdate bool `yaml:"check_update"`
	AutoUpdate  bool `yaml:"auto_update"`
}

func Load(path string) (*Config, error) {
	return LoadWithOverrides(path, nil, nil)
}

func LoadWithOverrides(path string, env []string, overrides []string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadDataWithOverrides(data, env, overrides)
}

// LoadDataWithOverrides parses a configuration document already owned by the
// caller. It allows embedded users to configure the engine without creating a
// temporary config.yml on disk.
func LoadDataWithOverrides(data []byte, env []string, overrides []string) (*Config, error) {
	data = append([]byte(nil), data...)
	if len(env) > 0 || len(overrides) > 0 {
		var err error
		data, err = applyOverrides(data, env, overrides)
		if err != nil {
			return nil, err
		}
	}
	cfg := Config{Crawler: Crawler{MovieConcurrency: 1}}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func ParseISODurationSeconds(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	if strings.HasPrefix(s, "P") {
		return parseISODuration(s)
	}
	return time.ParseDuration(s)
}

func applyOverrides(data []byte, env []string, overrides []string) ([]byte, error) {
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if !ok || !strings.HasPrefix(key, "JAVSP_") {
			continue
		}
		path, ok := envKeyToPath(strings.TrimPrefix(key, "JAVSP_"))
		if !ok {
			continue
		}
		if err := setPath(root, path, value); err != nil {
			return nil, err
		}
	}
	for _, item := range overrides {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			return nil, fmt.Errorf("override must be key=value: %s", item)
		}
		if err := setPath(root, normalizePath(key), value); err != nil {
			return nil, err
		}
	}
	return yaml.Marshal(root)
}

func envKeyToPath(key string) (string, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false
	}
	if strings.Contains(key, "__") {
		return normalizePath(strings.ReplaceAll(key, "__", ".")), true
	}
	upper := strings.ToUpper(key)
	if path, ok := envAliases()[upper]; ok {
		return path, true
	}
	for prefix, pathPrefix := range map[string]string{
		"NETWORK_PROXY_FREE_":           "network.proxy_free.",
		"TRANSLATOR_ENGINE_":            "translator.engine.",
		"SUMMARIZER_COVER_CROP_ENGINE_": "summarizer.cover.crop.engine.",
	} {
		if strings.HasPrefix(upper, prefix) {
			return pathPrefix + strings.ToLower(strings.TrimPrefix(upper, prefix)), true
		}
	}
	return "", false
}

func envAliases() map[string]string {
	paths := []string{
		"scanner.input_directory",
		"scanner.ignored_id_pattern",
		"scanner.filename_extensions",
		"scanner.ignored_folder_name_pattern",
		"scanner.minimum_size",
		"scanner.skip_nfo_dir",
		"scanner.manual",
		"network.proxy_server",
		"network.retry",
		"network.timeout",
		"crawler.selection.normal",
		"crawler.selection.fc2",
		"crawler.selection.cid",
		"crawler.selection.getchu",
		"crawler.selection.gyutto",
		"crawler.required_keys",
		"crawler.movie_concurrency",
		"crawler.hardworking",
		"crawler.respect_site_avid",
		"crawler.fc2fan_local_path",
		"crawler.sleep_after_scraping",
		"crawler.use_javdb_cover",
		"crawler.normalize_actress_name",
		"summarizer.move_files",
		"summarizer.path.output_folder_pattern",
		"summarizer.path.basename_pattern",
		"summarizer.path.length_maximum",
		"summarizer.path.length_by_byte",
		"summarizer.path.max_actress_count",
		"summarizer.path.hard_link",
		"summarizer.title.remove_trailing_actor_name",
		"summarizer.nfo.basename_pattern",
		"summarizer.nfo.title_pattern",
		"summarizer.nfo.custom_genres_fields",
		"summarizer.nfo.custom_tags_fields",
		"summarizer.cover.basename_pattern",
		"summarizer.cover.highres",
		"summarizer.cover.add_label",
		"summarizer.cover.crop.on_id_pattern",
		"summarizer.fanart.basename_pattern",
		"summarizer.censor_options_representation",
		"summarizer.extra_fanarts.enabled",
		"summarizer.extra_fanarts.scrap_interval",
		"translator.fields.title",
		"translator.fields.plot",
		"other.interactive",
		"other.check_update",
		"other.auto_update",
	}
	out := make(map[string]string, len(paths))
	for _, path := range paths {
		out[strings.ToUpper(strings.ReplaceAll(path, ".", "_"))] = path
	}
	return out
}

func normalizePath(path string) string {
	return strings.ToLower(strings.TrimSpace(path))
}

func setPath(root map[string]any, path string, rawValue string) error {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return nil
	}
	value, err := parseOverrideValue(rawValue)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	current := root
	for _, part := range parts[:len(parts)-1] {
		if next, ok := current[part].(map[string]any); ok {
			current = next
			continue
		}
		next := map[string]any{}
		current[part] = next
		current = next
	}
	current[parts[len(parts)-1]] = value
	return nil
}

func parseOverrideValue(raw string) (any, error) {
	var value any
	if err := yaml.Unmarshal([]byte(raw), &value); err != nil {
		return nil, err
	}
	return value, nil
}

func parseISODuration(s string) (time.Duration, error) {
	re := regexp.MustCompile(`^P(?:(\d+(?:\.\d+)?)W)?(?:(\d+(?:\.\d+)?)D)?(?:T(?:(\d+(?:\.\d+)?)H)?(?:(\d+(?:\.\d+)?)M)?(?:(\d+(?:\.\d+)?)S)?)?$`)
	match := re.FindStringSubmatch(s)
	if match == nil {
		return 0, fmt.Errorf("invalid ISO8601 duration: %s", s)
	}
	var total time.Duration
	units := []time.Duration{
		7 * 24 * time.Hour,
		24 * time.Hour,
		time.Hour,
		time.Minute,
		time.Second,
	}
	anyValue := false
	for i, unit := range units {
		if match[i+1] == "" {
			continue
		}
		value, err := strconv.ParseFloat(match[i+1], 64)
		if err != nil {
			return 0, err
		}
		total += time.Duration(value * float64(unit))
		anyValue = true
	}
	if !anyValue {
		return 0, fmt.Errorf("invalid ISO8601 duration: %s", s)
	}
	return total, nil
}

func (c *Config) Validate() error {
	if c.Network.Retry < 0 {
		return fmt.Errorf("network.retry must be non-negative")
	}
	for key, rawURL := range c.Network.ProxyFree {
		if err := validateURL("network.proxy_free."+key, rawURL, true); err != nil {
			return err
		}
	}
	if c.Network.ProxyServer != nil {
		if err := validateURL("network.proxy_server", *c.Network.ProxyServer, true); err != nil {
			return err
		}
	}
	if _, err := ParseISODurationSeconds(c.Network.Timeout); err != nil {
		return fmt.Errorf("network.timeout: %w", err)
	}
	if _, err := ParseISODurationSeconds(c.Crawler.SleepAfterScraping); err != nil {
		return fmt.Errorf("crawler.sleep_after_scraping: %w", err)
	}
	if _, err := ParseISODurationSeconds(c.Summarizer.ExtraFanarts.ScrapInterval); err != nil {
		return fmt.Errorf("summarizer.extra_fanarts.scrap_interval: %w", err)
	}
	if err := validateCrawlerSelections(c.Crawler.Selection); err != nil {
		return err
	}
	for _, key := range c.Crawler.RequiredKeys {
		if !validMovieInfoFields[key] {
			return fmt.Errorf("crawler.required_keys contains unsupported field: %s", key)
		}
	}
	if c.Crawler.MovieConcurrency <= 0 {
		return fmt.Errorf("crawler.movie_concurrency must be positive")
	}
	if c.Crawler.UseJavDBCover != "" && c.Crawler.UseJavDBCover != "yes" && c.Crawler.UseJavDBCover != "no" && c.Crawler.UseJavDBCover != "fallback" {
		return fmt.Errorf("crawler.use_javdb_cover must be yes, no, or fallback")
	}
	if c.Summarizer.Path.LengthMaximum <= 0 {
		return fmt.Errorf("summarizer.path.length_maximum must be positive")
	}
	if c.Summarizer.Path.MaxActressCount <= 0 {
		return fmt.Errorf("summarizer.path.max_actress_count must be positive")
	}
	if err := validateTranslatorEngine(c.Translator.Engine); err != nil {
		return err
	}
	if c.Summarizer.Cover.Crop.Engine != nil {
		values, err := engineMap("summarizer.cover.crop.engine", c.Summarizer.Cover.Crop.Engine)
		if err != nil {
			return err
		}
		name, err := requiredEngineName("summarizer.cover.crop.engine", values)
		if err != nil {
			return err
		}
		if name != "pigo" && name != "slimeface" {
			return fmt.Errorf("summarizer.cover.crop.engine.name unsupported: %s", name)
		}
	}
	return nil
}

func validateURL(field string, rawURL string, requireScheme bool) error {
	if strings.TrimSpace(rawURL) == "" {
		return nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (requireScheme && parsed.Scheme == "") {
		return fmt.Errorf("%s must be an absolute URL", field)
	}
	return nil
}

func validateCrawlerSelections(selection CrawlerSelection) error {
	groups := map[string][]string{
		"normal": selection.Normal,
		"fc2":    selection.FC2,
		"cid":    selection.CID,
		"getchu": selection.Getchu,
		"gyutto": selection.Gyutto,
	}
	for group, values := range groups {
		for _, id := range values {
			if !validCrawlerIDs[id] {
				return fmt.Errorf("crawler.selection.%s contains unsupported crawler: %s", group, id)
			}
		}
	}
	return nil
}

func validateTranslatorEngine(raw any) error {
	if raw == nil {
		return nil
	}
	values, err := engineMap("translator.engine", raw)
	if err != nil {
		return err
	}
	name, err := requiredEngineName("translator.engine", values)
	if err != nil {
		return err
	}
	if !validTranslatorEngines[name] {
		return fmt.Errorf("translator.engine.name unsupported: %s", name)
	}
	required := map[string][]string{
		"baidu":  {"app_id", "api_key"},
		"bing":   {"api_key"},
		"claude": {"api_key"},
		"deepl":  {"api_key"},
		"openai": {"url", "api_key", "model"},
	}
	for _, key := range required[name] {
		if _, ok := values[key]; !ok {
			return fmt.Errorf("translator.engine.%s is required for %s", key, name)
		}
	}
	if name == "openai" {
		rawURL, ok := values["url"].(string)
		if !ok {
			return fmt.Errorf("translator.engine.url must be an absolute URL")
		}
		if err := validateURL("translator.engine.url", rawURL, true); err != nil {
			return err
		}
	}
	return nil
}

func engineMap(field string, raw any) (map[string]any, error) {
	values, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a map", field)
	}
	return values, nil
}

func requiredEngineName(field string, values map[string]any) (string, error) {
	rawName, ok := values["name"]
	if !ok {
		return "", fmt.Errorf("%s.name is required", field)
	}
	name, ok := rawName.(string)
	if !ok || strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("%s.name must be a non-empty string", field)
	}
	return strings.ToLower(name), nil
}

var validCrawlerIDs = map[string]bool{
	"airav": true, "avsox": true, "avwiki": true, "dl_getchu": true, "fanza": true, "faleno": true,
	"fc2": true, "fc2db": true, "fc2fan": true, "fc2ppvdb": true, "gyutto": true, "jav321": true,
	"javbus": true, "javdb": true, "javlib": true, "javmenu": true, "mgstage": true,
	"njav": true, "prestige": true, "arzon": true, "arzon_iv": true,
}

var validMovieInfoFields = map[string]bool{
	"dvdid": true, "cid": true, "url": true, "plot": true, "cover": true, "big_cover": true,
	"genre": true, "genre_id": true, "genre_norm": true, "score": true, "title": true,
	"ori_title": true, "magnet": true, "serial": true, "actress": true, "actress_pics": true,
	"director": true, "duration": true, "producer": true, "publisher": true, "uncensored": true,
	"publish_date": true, "preview_pics": true, "preview_video": true,
}

var validTranslatorEngines = map[string]bool{
	"none": true, "openai": true, "google": true, "bing": true, "baidu": true, "claude": true, "deepl": true,
}
