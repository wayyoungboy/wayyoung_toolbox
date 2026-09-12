package engine

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/yuukiy/javsp-go/internal/model"
	"github.com/yuukiy/javsp-go/internal/scanner"
)

// scanMoviesWithDiagnostics makes unsupported media visible in the existing
// run log, including runs in which no supported movies could be recognized.
func scanMoviesWithDiagnostics(ctx context.Context, root string, opts scanner.Options, output io.Writer) ([]model.Movie, error) {
	return scanWithDiagnostics(opts, output, func(options scanner.Options) ([]model.Movie, error) {
		return scanner.ScanContext(ctx, root, options)
	})
}

func scanWithDiagnostics(opts scanner.Options, output io.Writer, scan func(scanner.Options) ([]model.Movie, error)) ([]model.Movie, error) {
	const detailLimit = 100
	unrecognized := 0
	var outputErr error
	previous := opts.OnUnrecognized
	opts.OnUnrecognized = func(file scanner.UnrecognizedFile) {
		unrecognized++
		if previous != nil {
			previous(file)
		}
		if unrecognized <= detailLimit && outputErr == nil {
			_, outputErr = fmt.Fprintf(output, "跳过：%q；未识别出番号或 CID，文件未改动。无番号/日期式命名需要独立元数据源。\n", file.Path)
		}
	}
	movies, err := scan(opts)
	if err != nil {
		return nil, err
	}
	if outputErr != nil {
		return nil, fmt.Errorf("write scan diagnostics: %w", outputErr)
	}
	if unrecognized > 0 {
		if _, err := fmt.Fprintf(output, "扫描结果：已识别 %d 项；未识别文件 %d 个（最多显示前 %d 个路径，不计入刮削成功）。\n", len(movies), unrecognized, detailLimit); err != nil {
			return nil, fmt.Errorf("write scan summary: %w", err)
		}
		if len(movies) == 0 {
			return nil, fmt.Errorf("发现 %d 个候选媒体文件，但未识别出番号或 CID；详见跳过原因", unrecognized)
		}
	}
	if len(movies) == 0 {
		return nil, errors.New("no movie files found in scan input directory")
	}
	return movies, nil
}
