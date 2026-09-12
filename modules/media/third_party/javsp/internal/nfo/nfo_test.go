package nfo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuukiy/javsp-go/internal/model"
)

func TestWriteNFO(t *testing.T) {
	uncensored := true
	info := &model.MovieInfo{
		DVDID:         "ABC-123",
		CID:           "cid001",
		Title:         "Translated Title",
		OriginalTitle: "Original Title",
		NFOTitle:      "ABC-123 Translated Title",
		Score:         "8.5",
		Plot:          "Plot text",
		Duration:      "120",
		Genre:         []string{"Drama"},
		Serial:        "Series Name",
		Director:      "Director Name",
		Producer:      "Studio Name",
		PublishDate:   "2024-01-02",
		PreviewVideo:  "https://example.test/trailer.mp4",
		Actress:       []string{"Actor A", "Actor B"},
		ActressPics:   map[string]string{"Actor A": "https://example.test/a.jpg"},
		Uncensored:    &uncensored,
	}
	path := filepath.Join(t.TempDir(), "movie.nfo")
	err := Write(info, path, Options{
		CustomGenresFields: []string{"{genre}", "{censor}"},
		CustomTagsFields:   []string{"{label}", "{year}"},
		CensorText:         []string{"无码", "有码", "打码情况未知"},
	})
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	xmlText := string(data)
	for _, want := range []string{
		"<title>ABC-123 Translated Title</title>",
		"<originaltitle>Original Title</originaltitle>",
		"<rating>8.5</rating>",
		"<plot>Plot text</plot>",
		"<runtime>120</runtime>",
		"<uniqueid type=\"num\" default=\"true\">ABC-123</uniqueid>",
		"<uniqueid type=\"cid\">cid001</uniqueid>",
		"<genre>Drama</genre>",
		"<tag>ABC</tag>",
		"<country>日本</country>",
		"<name>Series Name</name>",
		"<director>Director Name</director>",
		"<premiered>2024-01-02</premiered>",
		"<studio>Studio Name</studio>",
		"<trailer>https://example.test/trailer.mp4</trailer>",
		"<name>Actor A</name>",
		"<thumb>https://example.test/a.jpg</thumb>",
	} {
		if !strings.Contains(xmlText, want) {
			t.Fatalf("NFO missing %q:\n%s", want, xmlText)
		}
	}
}

func TestWriteCustomFieldsUseConfiguredDefaults(t *testing.T) {
	info := &model.MovieInfo{}
	path := filepath.Join(t.TempDir(), "movie.nfo")
	err := Write(info, path, Options{
		CustomGenresFields: []string{"{title}|{actress}|{serial}"},
		CustomTagsFields:   []string{"{director}|{producer}|{publisher}"},
		DefaultTitle:       "#Unknown Title",
		DefaultActress:     "#Unknown Actress",
		DefaultSeries:      "#Unknown Series",
		DefaultDirector:    "#Unknown Director",
		DefaultProducer:    "#Unknown Producer",
		DefaultPublisher:   "#Unknown Publisher",
	})
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	xmlText := string(data)
	for _, want := range []string{
		"<genre>#Unknown Title|#Unknown Actress|#Unknown Series</genre>",
		"<tag>#Unknown Director|#Unknown Producer|#Unknown Publisher</tag>",
	} {
		if !strings.Contains(xmlText, want) {
			t.Fatalf("NFO missing %q:\n%s", want, xmlText)
		}
	}
}
