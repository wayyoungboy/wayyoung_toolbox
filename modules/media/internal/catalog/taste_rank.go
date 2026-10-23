package catalog

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

type tasteFeature struct {
	kind, key, label string
	factor           float64
}
type tasteEvidence struct {
	source, workID string
	weight         float64
}
type tasteSignal struct {
	feature tasteFeature
	sources map[string]*tasteAggregate
}
type tasteAggregate struct {
	weight float64
	count  int
	own    map[string]float64
	// Six representatives suffice to return five after excluding the candidate.
	// Never build a full evidence slice for every recommendation.
	workIDs []string
}

func tasteFeatures(item tasteWork) []tasteFeature {
	result := []tasteFeature{}
	seen := map[string]bool{}
	add := func(kind, key, label string, factor float64) {
		if key != "" && !seen[kind+":"+key] {
			seen[kind+":"+key] = true
			result = append(result, tasteFeature{kind, key, label, factor})
		}
	}
	for _, a := range item.Actors {
		add("actor", a.ID, a.DisplayName, 1)
	}
	for _, tag := range item.Work.Genres {
		add("tag", actorNameKey(tag), strings.TrimSpace(tag), .7)
	}
	if studio := strings.TrimSpace(item.Work.Studio); studio != "" {
		add("studio", actorNameKey(studio), studio, .4)
	}
	if year := item.Work.Year; year > 0 && year <= 9999 {
		era := year / 10 * 10
		add("era", fmt.Sprint(era), fmt.Sprintf("%d 年代", era), .2)
	}
	return result
}

// rankTaste uses only explicit local facts, never inferred watch time, remote
// popularity or guessed identities. A work's own evidence cannot recommend it.
// Scores are relative ranking values, not probabilities or predicted ratings.
func rankTaste(ctx context.Context, works []tasteWork, followed []Actor, sources map[string]bool) ([]TasteFacet, []TasteRecommendation, error) {
	signals := map[string]*tasteSignal{}
	features := map[string][]tasteFeature{}
	exclude := map[string]bool{}
	add := func(feature tasteFeature, e tasteEvidence) {
		key := feature.kind + ":" + feature.key
		signal := signals[key]
		if signal == nil {
			signal = &tasteSignal{feature: feature, sources: map[string]*tasteAggregate{}}
			signals[key] = signal
		}
		aggregate := signal.sources[e.source]
		if aggregate == nil {
			aggregate = &tasteAggregate{own: map[string]float64{}, workIDs: []string{}}
			signal.sources[e.source] = aggregate
		}
		aggregate.weight += e.weight
		aggregate.count++
		if e.workID != "" {
			if _, exists := aggregate.own[e.workID]; !exists && len(aggregate.workIDs) < 6 {
				aggregate.workIDs = append(aggregate.workIDs, e.workID)
			}
			aggregate.own[e.workID] += e.weight
		}
	}
	for _, item := range works {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		w := item.Work
		fields := tasteFeatures(item)
		features[w.ID] = fields
		counts := map[string]int{}
		for _, f := range fields {
			counts[f.kind]++
		}
		weights := map[string]float64{}
		// Dislike is a work-level exclusion, not negative evidence about its actors
		// or genres. It also prevents positive signals from the same work.
		if sources["feedback"] && w.Feedback == "dislike" {
			exclude[w.ID] = true
			continue
		}
		if sources["collection"] {
			weights["collection"] = .25
			if w.Favorite {
				weights["collection"] += 3
				exclude[w.ID] = true
			}
			if w.WatchLater {
				weights["collection"] += 1
			}
		}
		if sources["history"] && item.History > 0 {
			weights["history"] = 1
			exclude[w.ID] = true
		}
		if sources["feedback"] {
			if w.Feedback == "like" {
				weights["feedback"] += 4
				exclude[w.ID] = true
			}
			if w.Rating >= 7 {
				weights["feedback"] += float64(w.Rating-6) * .5
				exclude[w.ID] = true
			}
		}
		if w.Watched {
			exclude[w.ID] = true
		}
		for _, source := range tasteSourceLabels {
			weight := weights[source.key]
			if weight <= 0 {
				continue
			}
			for _, feature := range fields {
				add(feature, tasteEvidence{source.key, w.ID, weight * feature.factor / float64(counts[feature.kind])})
			}
		}
	}
	if sources["follows"] {
		for _, a := range followed {
			add(tasteFeature{"actor", a.ID, a.DisplayName, 1}, tasteEvidence{"follows", "", 6})
		}
	}
	facets := []TasteFacet{}
	for _, signal := range signals {
		facet := TasteFacet{Kind: signal.feature.kind, Key: signal.feature.key, Label: signal.feature.label, Sources: []string{}}
		keys := map[string]bool{}
		for _, source := range tasteSourceLabels {
			if aggregate := signal.sources[source.key]; aggregate != nil {
				facet.Weight += aggregate.weight
				facet.EvidenceCount += aggregate.count
				keys[source.key] = true
			}
		}
		facet.Sources = sortedTasteSources(keys)
		facets = append(facets, facet)
	}
	sort.Slice(facets, func(i, j int) bool {
		if facets[i].Weight != facets[j].Weight {
			return facets[i].Weight > facets[j].Weight
		}
		if facets[i].Kind != facets[j].Kind {
			return facets[i].Kind < facets[j].Kind
		}
		return facets[i].Key < facets[j].Key
	})
	items := []TasteRecommendation{}
	for _, item := range works {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		w := item.Work
		if exclude[w.ID] || w.AvailableCount == 0 {
			continue
		}
		candidate := TasteRecommendation{Work: w, Reasons: []TasteReason{}}
		for _, feature := range features[w.ID] {
			signal := signals[feature.kind+":"+feature.key]
			if signal == nil {
				continue
			}
			for _, source := range tasteSourceLabels {
				aggregate := signal.sources[source.key]
				if aggregate == nil {
					continue
				}
				reason := TasteReason{Kind: feature.kind, Label: feature.label, Source: source.key, WorkIDs: []string{}}
				reason.Weight = aggregate.weight - aggregate.own[w.ID]
				for _, id := range aggregate.workIDs {
					if id != w.ID && len(reason.WorkIDs) < 5 {
						reason.WorkIDs = append(reason.WorkIDs, id)
					}
				}
				if reason.Weight > 1e-12 {
					candidate.Reasons = append(candidate.Reasons, reason)
					candidate.Score += reason.Weight
				}
			}
		}
		if candidate.Score <= 0 || math.IsNaN(candidate.Score) || math.IsInf(candidate.Score, 0) {
			continue
		}
		sort.Slice(candidate.Reasons, func(i, j int) bool {
			a, b := candidate.Reasons[i], candidate.Reasons[j]
			if a.Weight != b.Weight {
				return a.Weight > b.Weight
			}
			return a.Kind+":"+a.Label+":"+a.Source < b.Kind+":"+b.Label+":"+b.Source
		})
		if len(candidate.Reasons) > 4 {
			candidate.Reasons = candidate.Reasons[:4]
		}
		items = append(items, candidate)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		return items[i].Work.ID < items[j].Work.ID
	})
	return facets, items, nil
}
