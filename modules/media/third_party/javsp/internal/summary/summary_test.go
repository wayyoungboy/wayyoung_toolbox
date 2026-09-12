package summary

import (
	"testing"

	"github.com/yuukiy/javsp-go/internal/genre"
	"github.com/yuukiy/javsp-go/internal/model"
)

func TestMergeUsesPriorityAndJavDBGenreAndCoverFallback(t *testing.T) {
	movie := &model.Movie{
		DVDID: "ABC-123",
		Files: []string{"ABC-123-UC.mp4"},
	}
	ok, err := Merge(movie, []Result{
		{
			CrawlerID: "javdb",
			Info: &model.MovieInfo{
				DVDID:     "ABC-123",
				Title:     "JavDB Title",
				Cover:     "https://javdb.example/cover.jpg",
				BigCover:  "https://javdb.example/big.jpg",
				Genre:     []string{"JavDB Genre"},
				Actress:   []string{"Alias Name"},
				Duration:  "120",
				Publisher: "JavDB Publisher",
			},
		},
		{
			CrawlerID: "javbus",
			Info: &model.MovieInfo{
				DVDID:       "ABC-123",
				Title:       "JavBus Title",
				Cover:       "https://javbus.example/cover.jpg",
				BigCover:    "https://javbus.example/big.jpg",
				Genre:       []string{"JavBus Genre"},
				Actress:     []string{"Bus Actress"},
				ActressPics: map[string]string{"Alias Name": "https://img.example/a.jpg"},
				PublishDate: "2024-01-02",
			},
		},
	}, Options{
		RequiredKeys:    []string{"cover", "title"},
		UseJavDBCover:   "fallback",
		RespectSiteAvid: true,
		ActressAliases: map[string][]string{
			"Fixed Name": []string{"Alias Name"},
		},
	})
	if err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}
	if !ok {
		t.Fatalf("Merge returned ok=false")
	}
	if movie.Info.Title != "JavDB Title" {
		t.Fatalf("Title = %q, want JavDB Title", movie.Info.Title)
	}
	if movie.Info.Genre[0] != "JavDB Genre" {
		t.Fatalf("first genre = %q, want JavDB Genre", movie.Info.Genre[0])
	}
	if movie.Info.Cover != "https://javbus.example/cover.jpg" {
		t.Fatalf("Cover = %q, want javbus cover", movie.Info.Cover)
	}
	if got := movie.Info.Covers; len(got) != 2 || got[0] != "https://javbus.example/cover.jpg" || got[1] != "https://javdb.example/cover.jpg" {
		t.Fatalf("Covers = %#v, want javbus then javdb", got)
	}
	if movie.Info.Actress[0] != "Fixed Name" {
		t.Fatalf("Actress = %#v, want normalized Fixed Name", movie.Info.Actress)
	}
	if !contains(movie.Info.Genre, "内嵌字幕") || !contains(movie.Info.Genre, "无码流出/破解") {
		t.Fatalf("Genre missing file attrs: %#v", movie.Info.Genre)
	}
}

func TestMergeFailsWhenRequiredKeyMissing(t *testing.T) {
	movie := &model.Movie{DVDID: "ABC-123", Files: []string{"ABC-123.mp4"}}
	ok, err := Merge(movie, []Result{
		{CrawlerID: "javbus", Info: &model.MovieInfo{Title: "Title Only"}},
	}, Options{RequiredKeys: []string{"cover", "title"}})
	if err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}
	if ok {
		t.Fatalf("Merge returned ok=true, want false")
	}
	if movie.Info != nil {
		t.Fatalf("movie.Info = %#v, want nil", movie.Info)
	}
}

func TestMergeAppliesGenreMap(t *testing.T) {
	movie := &model.Movie{DVDID: "ABC-123", Files: []string{"ABC-123.mp4"}}
	ok, err := Merge(movie, []Result{
		{
			CrawlerID: "javdb",
			Info: &model.MovieInfo{
				DVDID:   "ABC-123",
				Title:   "Title",
				Cover:   "cover.jpg",
				Genre:   []string{"Raw A", "Raw B", "Raw C"},
				GenreID: []string{"mapped", "drop", "unknown"},
			},
		},
	}, Options{
		RequiredKeys: []string{"cover", "title"},
		GenreMaps: map[string]genre.Map{
			"javdb": {
				"mapped": "Mapped Genre",
				"drop":   "",
			},
		},
	})
	if err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}
	if !ok {
		t.Fatalf("Merge returned ok=false")
	}
	want := []string{"Mapped Genre", "unknown"}
	if len(movie.Info.GenreNorm) != len(want) || movie.Info.GenreNorm[0] != want[0] || movie.Info.GenreNorm[1] != want[1] {
		t.Fatalf("GenreNorm = %#v, want %#v", movie.Info.GenreNorm, want)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
