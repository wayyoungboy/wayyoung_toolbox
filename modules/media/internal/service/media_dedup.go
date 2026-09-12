package service

import "regexp"

// MediaDuplicate is evidence of an acquisition decision, not permission to
// remove files or take ownership of another torrent. It survives log rotation.
type MediaDuplicate struct {
	Source         string `json:"source"`
	MediaID        string `json:"media_id"`
	Title          string `json:"title,omitempty"`
	Path           string `json:"path,omitempty"`
	TorrentHash    string `json:"torrent_hash,omitempty"`
	TorrentAddedOn int64  `json:"torrent_added_on,omitempty"`
	State          string `json:"state,omitempty"`
	CheckedAt      string `json:"checked_at"`
}

var dedupIDCandidate = regexp.MustCompile(`(?i)fc2(?:[-_ ]?ppv)?[-_ ]+\d{5,9}|[a-z0-9]{2,16}[-_]+\d{2,9}`)

// Collections with multiple distinct IDs cannot prove that a single requested
// movie is present. Do not guess from the first ID or from a parent folder.
func unambiguousMediaID(title string) (string, bool) {
	alnum := func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
	}
	id := ""
	for _, span := range dedupIDCandidate.FindAllStringIndex(title, -1) {
		if span[0] > 0 && alnum(title[span[0]-1]) || span[1] < len(title) && alnum(title[span[1]]) {
			continue
		}
		candidate, ok := mediaIDFromTitle(title[span[0]:span[1]])
		if !ok {
			continue
		}
		if id != "" && candidate != id {
			return "", false
		}
		id = candidate
	}
	return id, id != ""
}
