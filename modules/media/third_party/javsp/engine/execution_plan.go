package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/yuukiy/javsp-go/internal/config"
	"github.com/yuukiy/javsp-go/internal/model"
)

type FileMove struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

// MoviePlan uses the same original scan root and naming rules as recursive
// execution. Hook arguments contain copied paths, never mutable model state.
type MoviePlan struct {
	MediaID              string     `json:"media_id"`
	Files                []FileMove `json:"files"`
	NFOPath              string     `json:"nfo_path"`
	PosterPath           string     `json:"poster_path"`
	FanartPath           string     `json:"fanart_path"`
	ExtraFanartDirectory string     `json:"extra_fanart_directory,omitempty"`
	MoveFiles            bool       `json:"move_files"`
	HardLink             bool       `json:"hard_link"`
}

// ExecutionHooks let an embedding application persist/validate planned paths
// before artifact writes and own the final media-file transaction. MoveFiles
// runs only after metadata is ready; nil retains the standalone mover. A hook
// error aborts that movie, without falling back to the standalone mover.
// Hooks may run concurrently for different movies and must honor ctx.
type ExecutionHooks struct {
	BeforeOrganize func(context.Context, MoviePlan) error
	MoveFiles      func(context.Context, MoviePlan) (MoveResult, error)
}

// MoveResult reports committed paths even when a later file or checkpoint
// fails. Recovery still requires the embedding application's durable proof.
type MoveResult struct {
	OutputPaths []string `json:"output_paths"`
}

func recordMoveResult(movie *model.Movie, cfg *config.Config, result MoveResult) error {
	allowed := make(map[string]bool)
	for _, file := range buildMoviePlan(movie, cfg).Files {
		allowed[file.Destination] = true
	}
	seen := make(map[string]bool)
	for _, path := range result.OutputPaths {
		if !allowed[path] || seen[path] {
			return fmt.Errorf("move hook reported an unplanned or duplicate output")
		}
		seen[path] = true
	}
	movie.NewPaths = append([]string{}, result.OutputPaths...)
	return nil
}

type MovieOutcome struct {
	MediaID     string   `json:"media_id"`
	SourcePaths []string `json:"source_paths"`
	OutputPaths []string `json:"output_paths"`
	NFOPath     string   `json:"nfo_path"`
	PosterPath  string   `json:"poster_path"`
	FanartPath  string   `json:"fanart_path"`
	Status      string   `json:"status"`
	Error       string   `json:"error,omitempty"`
}

func buildMoviePlan(movie *model.Movie, cfg *config.Config) MoviePlan {
	plan := MoviePlan{MediaID: movieName(movie), NFOPath: movie.NFOFile, PosterPath: movie.PosterFile, FanartPath: movie.FanartFile, MoveFiles: cfg.Summarizer.MoveFiles, HardLink: cfg.Summarizer.Path.HardLink}
	if cfg.Summarizer.ExtraFanarts.Enabled {
		plan.ExtraFanartDirectory = filepath.Join(movie.SaveDir, "extrafanart")
	}
	for index, source := range movie.Files {
		destination := source
		if plan.MoveFiles {
			base := movie.BaseName
			if len(movie.Files) > 1 {
				base = fmt.Sprintf("%s-CD%d", base, index+1)
			}
			destination = filepath.Join(movie.SaveDir, base+filepath.Ext(source))
		}
		plan.Files = append(plan.Files, FileMove{Source: source, Destination: destination})
	}
	return plan
}

func movieOutcome(movie *model.Movie, cfg *config.Config, success bool, err error) MovieOutcome {
	outcome := MovieOutcome{MediaID: movieName(movie), SourcePaths: append([]string{}, movie.Files...), OutputPaths: append([]string{}, movie.NewPaths...), NFOPath: movie.NFOFile, PosterPath: movie.PosterFile, FanartPath: movie.FanartFile, Status: "failed"}
	if success && err == nil {
		outcome.Status = "success"
		if !cfg.Summarizer.MoveFiles {
			outcome.OutputPaths = append([]string{}, movie.Files...)
		}
		return outcome
	}
	if len(outcome.OutputPaths) > 0 {
		outcome.Status = "partial"
	}
	if errors.Is(err, context.Canceled) {
		outcome.Status = "canceled"
	} else if errors.Is(err, context.DeadlineExceeded) {
		outcome.Status = "timed_out"
	}
	if err != nil {
		outcome.Error = err.Error()
	} else {
		outcome.Error = "missing required metadata"
	}
	return outcome
}
