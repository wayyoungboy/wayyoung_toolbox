package service

import (
	"time"

	"github.com/wayyoungboy/fnos-music-converter/internal/converter"
)

var Version = "0.0.4"

const AppName = "music_converter"

type Engine = converter.Engine

type Settings struct {
	SourceDir      string `json:"source_dir"`
	OutputDir      string `json:"output_dir"`
	Recursive      bool   `json:"recursive"`
	PreserveTree   bool   `json:"preserve_tree"`
	FetchRemoteArt bool   `json:"fetch_remote_art"`
	MigrateMP3     bool   `json:"migrate_mp3"`
	Conflict       string `json:"conflict"`
	Concurrency    int    `json:"concurrency"`
	RetainRuns     int    `json:"retain_runs"`
}

type Job struct {
	ID        string             `json:"id"`
	Status    string             `json:"status"`
	StartedAt time.Time          `json:"started_at"`
	EndedAt   *time.Time         `json:"ended_at,omitempty"`
	Total     int                `json:"total"`
	Completed int                `json:"completed"`
	Failed    int                `json:"failed"`
	Skipped   int                `json:"skipped"`
	Canceled  int                `json:"canceled"`
	Results   []converter.Result `json:"results"`
}

type Run struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Total     int       `json:"total"`
	Completed int       `json:"completed"`
	Failed    int       `json:"failed"`
	Skipped   int       `json:"skipped"`
	Canceled  int       `json:"canceled"`
}

type persistedState struct {
	Settings Settings `json:"settings"`
	Runs     []Run    `json:"runs"`
}

func defaultSettings() Settings {
	return Settings{
		Recursive:    true,
		PreserveTree: true,
		Conflict:     string(converter.ConflictSkip),
		Concurrency:  2,
		RetainRuns:   100,
	}
}
