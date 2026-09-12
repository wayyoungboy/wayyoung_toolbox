package model

type DataSource string

const (
	DataSourceNormal DataSource = "normal"
	DataSourceFC2    DataSource = "fc2"
	DataSourceCID    DataSource = "cid"
	DataSourceGetchu DataSource = "getchu"
	DataSourceGyutto DataSource = "gyutto"
)

type CrawlerID string

type Movie struct {
	DVDID      string
	CID        string
	Files      []string
	DataSource DataSource
	Info       *MovieInfo
	SaveDir    string
	BaseName   string
	NFOFile    string
	FanartFile string
	PosterFile string
	NewPaths   []string
}

type MovieInfo struct {
	DVDID              string
	CID                string
	URL                string
	Plot               string
	OriginalPlot       string
	Cover              string
	BigCover           string
	Covers             []string
	BigCovers          []string
	Genre              []string
	GenreID            []string
	GenreNorm          []string
	Score              string
	Title              string
	OriginalTitle      string
	Magnet             []string
	Serial             string
	Actress            []string
	ActressPics        map[string]string
	Director           string
	Duration           string
	Producer           string
	Publisher          string
	Uncensored         *bool
	PublishDate        string
	PreviewPics        []string
	PreviewVideo       string
	NFOTitle           string
	Label              string
	TitleBreak         []string
	OriginalTitleBreak []string
}
