package service

import (
	"context"
	"io"

	javspengine "github.com/yuukiy/javsp-go/engine"
)

type JavspProgress struct {
	Phase     string `json:"phase"`
	Movie     string `json:"movie,omitempty"`
	Site      string `json:"site,omitempty"`
	Message   string `json:"message,omitempty"`
	Current   int    `json:"current"`
	Total     int    `json:"total"`
	Processed int    `json:"processed"`
}

type embeddedJavspOptions struct {
	ConfigData     []byte
	ResourceRoot   string
	InputDirectory string
	InputFiles     []string
	Hooks          JavspExecutionHooks
	Output         io.Writer
	Progress       func(JavspProgress)
}

type embeddedJavspResult struct {
	Total     int
	Processed int
	Failed    int
	Movies    []JavspMovieOutcome
}

type JavspMoviePlan = javspengine.MoviePlan
type JavspFileMove = javspengine.FileMove
type JavspMoveResult = javspengine.MoveResult
type JavspMovieOutcome = javspengine.MovieOutcome
type JavspExecutionHooks = javspengine.ExecutionHooks

type embeddedJavsp interface {
	Run(context.Context, embeddedJavspOptions) (embeddedJavspResult, error)
	Version() string
}

type embeddedJavspAdapter struct {
	version string
}

func (adapter embeddedJavspAdapter) Run(ctx context.Context, options embeddedJavspOptions) (embeddedJavspResult, error) {
	result, err := javspengine.Run(ctx, javspengine.Options{
		ConfigData:     options.ConfigData,
		ResourceRoot:   options.ResourceRoot,
		InputDirectory: options.InputDirectory,
		InputFiles:     options.InputFiles,
		Hooks:          options.Hooks,
		Output:         options.Output,
		Progress: func(event javspengine.Event) {
			if options.Progress != nil {
				options.Progress(JavspProgress{
					Phase: string(event.Phase), Movie: event.Movie, Site: event.Site, Message: event.Message,
					Current: event.Current, Total: event.Total, Processed: event.Processed,
				})
			}
		},
	})
	return embeddedJavspResult{Total: result.Total, Processed: result.Processed, Failed: result.Failed, Movies: result.Movies}, err
}

func (adapter embeddedJavspAdapter) Version() string {
	if adapter.version == "" {
		return "embedded"
	}
	return adapter.version
}
