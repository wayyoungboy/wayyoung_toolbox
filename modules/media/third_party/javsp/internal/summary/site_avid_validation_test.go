package summary

import (
	"testing"

	"github.com/yuukiy/javsp-go/internal/model"
)

func TestMergeRejectsPrefixlessSiteDVDID(t *testing.T) {
	movie := &model.Movie{
		DVDID: "PPPD-646",
		Files: []string{"PPPD-646-C.mp4"},
	}
	ok, err := Merge(movie, []Result{
		{
			CrawlerID: "avsox",
			Info: &model.MovieInfo{
				DVDID: "-646",
				Title: "Regression Title",
				Cover: "https://example.test/cover.jpg",
			},
		},
	}, Options{
		RequiredKeys:    []string{"cover", "title"},
		RespectSiteAvid: true,
	})
	if err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}
	if !ok {
		t.Fatal("Merge returned ok=false")
	}
	if movie.Info.DVDID != "PPPD-646" {
		t.Fatalf("DVDID = %q, want original PPPD-646", movie.Info.DVDID)
	}
}

func TestMergeStillAcceptsPlausibleSiteDVDID(t *testing.T) {
	movie := &model.Movie{
		DVDID: "PPPD646",
		Files: []string{"PPPD646.mp4"},
	}
	ok, err := Merge(movie, []Result{
		{
			CrawlerID: "javdb",
			Info: &model.MovieInfo{
				DVDID: "PPPD-646",
				Title: "Corrected Title",
				Cover: "https://example.test/cover.jpg",
			},
		},
	}, Options{
		RequiredKeys:    []string{"cover", "title"},
		RespectSiteAvid: true,
	})
	if err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}
	if !ok {
		t.Fatal("Merge returned ok=false")
	}
	if movie.Info.DVDID != "PPPD-646" {
		t.Fatalf("DVDID = %q, want corrected PPPD-646", movie.Info.DVDID)
	}
}
