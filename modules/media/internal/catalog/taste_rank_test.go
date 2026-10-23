package catalog

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func tasteRankFixture() ([]tasteWork, map[string]bool) {
	return []tasteWork{
		{Work: Work{ID: "seed", Favorite: true, AvailableCount: 1, Genres: []string{"Test tag", "TEST TAG", " Test tag "}, Studio: "Sample studio", Year: 2024}, Actors: []Actor{{ID: "actor", DisplayName: "Test Actor"}, {ID: "actor", DisplayName: "Test Actor"}}},
		{Work: Work{ID: "candidate", AvailableCount: 1, Genres: []string{"Test tag"}, Studio: "Sample studio", Year: 2023}, Actors: []Actor{{ID: "actor", DisplayName: "Test Actor"}}},
	}, map[string]bool{"collection": true, "history": true, "feedback": true, "follows": true}
}
func TestTasteRankingNoSelfEvidenceDuplicatesOrFabricatedSignals(t *testing.T) {
	works, sources := tasteRankFixture()
	facets, items, err := rankTaste(context.Background(), works, nil, sources)
	if err != nil || len(facets) != 4 || len(items) != 1 {
		t.Fatalf("%+v %+v %v", facets, items, err)
	}
	for _, reason := range items[0].Reasons {
		if !reflect.DeepEqual(reason.WorkIDs, []string{"seed"}) {
			t.Fatalf("self evidence: %+v", reason)
		}
	}
	_, single, err := rankTaste(context.Background(), works[1:], nil, sources)
	if err != nil || len(single) != 0 {
		t.Fatal("sole work recommended itself")
	}
	works[0].Work.Feedback = "dislike"
	_, items, err = rankTaste(context.Background(), works, nil, sources)
	if err != nil || len(items) != 0 {
		t.Fatal("dislike contributed positive preference")
	}
	works = []tasteWork{{Work: Work{ID: "unknown", AvailableCount: 1}}}
	facets, items, err = rankTaste(context.Background(), works, nil, sources)
	if err != nil || len(facets) != 0 || len(items) != 0 {
		t.Fatal("unknown metadata invented a preference")
	}
}
func TestTasteRankingStableIndependentInterestsAndCancellation(t *testing.T) {
	works, sources := tasteRankFixture()
	_, first, _ := rankTaste(context.Background(), works, nil, sources)
	works = append(works, tasteWork{Work: Work{ID: "other", Favorite: true, AvailableCount: 1, Genres: []string{"Unrelated"}}})
	_, next, err := rankTaste(context.Background(), works, nil, sources)
	if err != nil || len(next) != 1 || first[0].Score != next[0].Score {
		t.Fatal("unrelated interest diluted matching score")
	}
	for range 5 {
		_, again, err := rankTaste(context.Background(), works, nil, sources)
		if err != nil || !reflect.DeepEqual(next, again) {
			t.Fatal("nondeterministic ranking")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := rankTaste(ctx, works, nil, sources); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestTasteRankingDisabledFeedbackCannotAffectActiveSources(t *testing.T) {
	works, sources := tasteRankFixture()
	sources["feedback"] = false
	_, before, err := rankTaste(context.Background(), works, nil, sources)
	if err != nil {
		t.Fatal(err)
	}
	works[0].Work.Feedback = "dislike"
	_, after, err := rankTaste(context.Background(), works, nil, sources)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("disabled feedback changed the profile or recommendations")
	}
}
func BenchmarkTasteRankingSharedMetadata(b *testing.B) {
	works := make([]tasteWork, 5000)
	for i := range works {
		works[i] = tasteWork{Work: Work{ID: fmt.Sprint(i), Genres: []string{"Shared"}, Year: 2024, AvailableCount: 1}}
	}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		if _, _, err := rankTaste(context.Background(), works, nil, map[string]bool{"collection": true}); err != nil {
			b.Fatal(err)
		}
	}
}
