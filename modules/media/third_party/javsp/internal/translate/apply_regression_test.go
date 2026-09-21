package translate

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/yuukiy/javsp-go/internal/model"
)

type regressionEngine func(string) (Result, error)

func (f regressionEngine) Translate(_ context.Context, text string, _ []string) (Result, error) {
	return f(text)
}

func TestApplyDoesNotTreatOriginalTitleAsTranslationProof(t *testing.T) {
	info := &model.MovieInfo{Title: "今日は晴れです", OriginalTitle: "今日は晴れです 原題"}
	engine := &fakeEngine{results: map[string]Result{info.Title: {Text: "今天是晴天"}}}
	ok, err := Apply(context.Background(), info, engine, Fields{Title: true})
	if err != nil || !ok || info.Title != "今天是晴天" || len(engine.calls) != 1 {
		t.Fatalf("untranslated title bypassed translation: %+v, %v", info, err)
	}
	if info.OriginalTitle != "今日は晴れです 原題" {
		t.Fatal("lost original source title")
	}
}

func TestApplyRejectsInvalidTranslationWithoutChangingMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		engine Engine
	}{
		{"missing_engine", nil},
		{"empty", regressionEngine(func(string) (Result, error) { return Result{Text: " \n"}, nil })},
		{"unchanged_japanese", NoopEngine{}},
		{"plot_failure", regressionEngine(func(text string) (Result, error) {
			if text == "今日は晴れです" {
				return Result{Text: "今天是晴天"}, nil
			}
			return Result{}, errors.New("provider unavailable")
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &model.MovieInfo{Title: "今日は晴れです", Plot: "明日も晴れるでしょう"}
			before := *info
			ok, err := Apply(context.Background(), info, tc.engine, Fields{Title: true, Plot: true})
			if ok || err == nil {
				t.Fatalf("invalid translation accepted: %v %v", ok, err)
			}
			if !reflect.DeepEqual(*info, before) {
				t.Fatalf("partial metadata mutation after failure: %+v", info)
			}
		})
	}
}

func TestApplyKeepsDeliberatelyDisabledTranslationAndChineseNames(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		info := &model.MovieInfo{Title: "今日は晴れです"}
		ok, err := Apply(context.Background(), info, nil, Fields{})
		if !ok || err != nil || info.OriginalTitle != "" {
			t.Fatalf("disabled translation changed: %+v %v", info, err)
		}
	})
	t.Run("already_chinese_with_japanese_name", func(t *testing.T) {
		info := &model.MovieInfo{Title: "今天是晴天 小林あおい", Actress: []string{"小林あおい"}}
		ok, err := Apply(context.Background(), info, NoopEngine{}, Fields{Title: true})
		if !ok || err != nil {
			t.Fatalf("actor name mistaken for untranslated text: %v", err)
		}
	})
}
