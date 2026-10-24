package engine

import (
	"context"
	"errors"
)

// Resource searches never run scanner, organizer, translator or download code.
type ResourceSearchOptions struct {
	Options
	Code     string
	Provider string
}

type ResourceCandidate struct {
	InfoHash     string `json:"infohash"`
	Magnet       string `json:"magnet"`
	Name         string `json:"name"`
	Source       string `json:"source"`
	SourceURL    string `json:"source_url"`
	Verification string `json:"verification"`
}

type ResourceSearchResult struct {
	Code     string              `json:"code"`
	Provider string              `json:"provider"`
	State    string              `json:"state"`
	Items    []ResourceCandidate `json:"items"`
}

var ErrResourceInput = errors.New("invalid resource search input")

func SearchResources(ctx context.Context, options ResourceSearchOptions) (ResourceSearchResult, error) {
	return ResourceSearchResult{}, errors.New("resource search not implemented")
}
