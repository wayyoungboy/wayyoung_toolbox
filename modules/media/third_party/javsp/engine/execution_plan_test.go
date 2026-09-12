package engine

import (
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/yuukiy/javsp-go/internal/config"
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/model"
	"github.com/yuukiy/javsp-go/internal/organizer"
)

type planMovieRunner struct{ info *model.MovieInfo }

func (r planMovieRunner) Run(context.Context, []model.CrawlerID, crawler.MovieRef) []crawler.Result {
	return []crawler.Result{{CrawlerID: "javbus", Info: r.info}}
}

func planFixture(t *testing.T) (*model.Movie, *config.Config, planMovieRunner) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "pending", "TEST-001.mp4")
	if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	cover := filepath.Join(root, "fixture.png")
	f, err := os.Create(cover)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 40, 20))); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Network: config.Network{Retry: 1},
		Crawler: config.Crawler{RequiredKeys: []string{"cover", "title"}, RespectSiteAvid: true},
		Summarizer: config.Summarizer{
			MoveFiles:                   true,
			Path:                        config.PathSummarizer{OutputFolderPattern: filepath.Join(root, "output", "{num}"), BasenamePattern: "{num}", MaxActressCount: 10},
			NFO:                         config.NFOSummarizer{BasenamePattern: "movie", TitlePattern: "{num} {title}"},
			Fanart:                      config.FanartSummarizer{BasenamePattern: "fanart"},
			Cover:                       config.CoverSummarizer{BasenamePattern: "poster"},
			CensorOptionsRepresentation: []string{"none", "yes", "unknown"},
		},
	}
	movie := &model.Movie{DVDID: "TEST-001", Files: []string{source}, DataSource: model.DataSourceNormal}
	return movie, cfg, planMovieRunner{info: &model.MovieInfo{DVDID: "TEST-001", Title: "Example title", Cover: cover}}
}

func TestOrganizePlanCanRejectBeforeAnyArtifacts(t *testing.T) {
	movie, cfg, runner := planFixture(t)
	blocked := errors.New("plan was not saved")
	called := false
	ok, err := processMovieWithHooks(context.Background(), movie, runner, nil, cfg, nil, nil, nil, "", nil, nil, ExecutionHooks{BeforeOrganize: func(ctx context.Context, plan MoviePlan) error {
		called = true
		if len(plan.Files) != 1 || plan.Files[0].Source != movie.Files[0] || filepath.Base(plan.Files[0].Destination) != "TEST-001.mp4" || plan.NFOPath == "" {
			t.Errorf("incomplete plan: %+v", plan)
		}
		return blocked
	}})
	if ok || !errors.Is(err, blocked) || !called {
		t.Fatalf("plan rejection ignored: %v %v", ok, err)
	}
	if _, err := os.Stat(movie.SaveDir); !os.IsNotExist(err) {
		t.Fatalf("wrote artifacts before plan approval: %v", err)
	}
	if _, err := os.Stat(movie.Files[0]); err != nil {
		t.Fatal(err)
	}
}

func TestMoveHookRunsAfterArtifactsAndReplacesLegacyMove(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "checkpoint_failure"}[fail], func(t *testing.T) {
			movie, cfg, runner := planFixture(t)
			calls := 0
			checkpointErr := errors.New("checkpoint unavailable")
			ok, err := processMovieWithHooks(context.Background(), movie, runner, nil, cfg, nil, nil, nil, "", nil, nil, ExecutionHooks{MoveFiles: func(ctx context.Context, plan MoviePlan) (MoveResult, error) {
				calls++
				if _, err := os.Stat(plan.NFOPath); err != nil {
					t.Errorf("move before metadata ready: %v", err)
				}
				if fail {
					return MoveResult{}, checkpointErr
				}
				if err := os.Rename(plan.Files[0].Source, plan.Files[0].Destination); err != nil {
					return MoveResult{}, err
				}
				return MoveResult{OutputPaths: []string{plan.Files[0].Destination}}, nil
			}})
			if calls != 1 {
				t.Fatalf("callback calls=%d", calls)
			}
			if fail {
				if ok || !errors.Is(err, checkpointErr) {
					t.Fatalf("lost checkpoint failure: %v %v", ok, err)
				}
				if _, err := os.Stat(movie.Files[0]); err != nil {
					t.Fatal("legacy move ran after callback failure")
				}
			} else {
				if !ok || err != nil || len(movie.NewPaths) != 1 {
					t.Fatalf("callback move repeated or paths lost: %+v %v", movie, err)
				}
				if _, err := os.Stat(movie.NewPaths[0]); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestMovieOutcomePreservesPathsAndTerminalReason(t *testing.T) {
	movie, cfg, _ := planFixture(t)
	movie.NewPaths = []string{filepath.Join(filepath.Dir(movie.Files[0]), "output.mp4")}
	for _, tc := range []struct {
		success bool
		err     error
		status  string
	}{
		{true, nil, "success"}, {false, errors.New("failed"), "partial"}, {false, context.Canceled, "canceled"}, {false, context.DeadlineExceeded, "timed_out"},
	} {
		out := movieOutcome(movie, cfg, tc.success, tc.err)
		if out.Status != tc.status || len(out.SourcePaths) != 1 || len(out.OutputPaths) != 1 || out.MediaID != "TEST-001" {
			t.Fatalf("wrong outcome: %+v", out)
		}
	}
}

func TestPartialMoveReportsCommittedOutputThroughRealExecution(t *testing.T) {
	for _, mode := range []string{"hook", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			movie, cfg, runner := planFixture(t)
			second := filepath.Join(filepath.Dir(movie.Files[0]), "TEST-001-CD2.mp4")
			if err := os.WriteFile(second, []byte("second"), 0600); err != nil {
				t.Fatal(err)
			}
			movie.Files = append(movie.Files, second)
			var err error
			if mode == "hook" {
				_, err = processMovieWithHooks(context.Background(), movie, runner, nil, cfg, nil, nil, nil, "", nil, nil, ExecutionHooks{MoveFiles: func(ctx context.Context, plan MoviePlan) (MoveResult, error) {
					if err := os.Rename(plan.Files[0].Source, plan.Files[0].Destination); err != nil {
						return MoveResult{}, err
					}
					return MoveResult{OutputPaths: []string{plan.Files[0].Destination}}, errors.New("second file failed")
				}})
			} else {
				movie.SaveDir = filepath.Join(t.TempDir(), "output")
				movie.BaseName = "TEST-001"
				if err := os.MkdirAll(movie.SaveDir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(movie.SaveDir, "TEST-001-CD2.mp4"), []byte("occupied"), 0600); err != nil {
					t.Fatal(err)
				}
				err = organizer.MoveFilesContext(context.Background(), movie, false)
			}
			out := movieOutcome(movie, cfg, false, err)
			if err == nil || out.Status != "partial" || len(out.OutputPaths) != 1 {
				t.Fatalf("committed output lost after partial move: %+v %v", out, err)
			}
			if _, err := os.Stat(out.OutputPaths[0]); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(second); err != nil {
				t.Fatal("unfinished source lost")
			}
		})
	}
}
