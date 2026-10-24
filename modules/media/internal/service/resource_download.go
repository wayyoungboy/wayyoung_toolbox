package service

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
	javspengine "github.com/yuukiy/javsp-go/engine"
)

// ResourceDownloadSelection names a candidate from an exact successful search
// snapshot. Callers cannot supply a magnet, endpoint, credentials or disk path.
type ResourceDownloadSelection struct {
	Code       string `json:"code"`
	Provider   string `json:"provider"`
	InfoHash   string `json:"infohash"`
	SearchedAt string `json:"searched_at"`
}

// ResourceAcquisition is a standalone producer on the persistent media ledger,
// not an RSS entry. The endpoint identity is opaque; secrets stay in settings.
type ResourceAcquisition struct {
	Provider              string `json:"provider"`
	SearchedAt            string `json:"searched_at"`
	Magnet                string `json:"magnet"`
	DownloadPath          string `json:"download_path"`
	DownloaderIdentity    string `json:"downloader_identity"`
	SubmissionState       string `json:"submission_state,omitempty"`
	ConfirmationCheckedAt string `json:"confirmation_checked_at,omitempty"`
}

func resourceSettings(values map[string]json.RawMessage) Settings {
	defaults := defaultSettings()
	return Settings{QBHost: decodeSetting(values, "qb_host", defaults.QBHost), QBPort: decodeSetting(values, "qb_port", defaults.QBPort), QBUsername: decodeSetting(values, "qb_username", defaults.QBUsername)}
}

func resourceDownloaderIdentity(settings Settings) (string, error) {
	base, err := qbBaseURL(settings)
	if err != nil || base.User != nil || base.RawQuery != "" || base.Fragment != "" || len(base.String()) > 2048 {
		return "", catalog.ErrInvalid
	}
	base.Scheme, base.Host = strings.ToLower(base.Scheme), strings.ToLower(base.Host)
	return fingerprint(base.String() + "\x00" + settings.QBUsername), nil
}

func resourceSelection(input ResourceDownloadSelection) (ResourceDownloadSelection, error) {
	code, err := resourceSearchInput(input.Code, input.Provider)
	if err != nil {
		return input, err
	}
	input.Code, input.InfoHash = code, strings.ToLower(input.InfoHash)
	decoded, err := hex.DecodeString(input.InfoHash)
	if err != nil || len(decoded) != 20 || input.SearchedAt == "" || len(input.SearchedAt) > 64 {
		return input, catalog.ErrInvalid
	}
	return input, nil
}

// EnqueueResourceDownload atomically checks the selected cache snapshot and
// reserves the artifact. Retried POSTs return the original job, even after cache
// eviction or a settings edit; they never reset a canceled or completed job.
func (d *Database) EnqueueResourceDownload(ctx context.Context, input ResourceDownloadSelection) (MediaJob, bool, error) {
	input, err := resourceSelection(input)
	if err != nil {
		return MediaJob{}, false, err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return MediaJob{}, false, err
	}
	defer tx.Rollback()
	key := "resource:" + input.InfoHash
	job, err := scanMediaJob(tx.QueryRowContext(ctx, `SELECT `+mediaJobColumns+` FROM media_jobs
WHERE job_key=? OR (status<>'skipped' AND COALESCE(json_extract(payload,'$.related_job_id'),0)=0
 AND (lower(json_extract(payload,'$.torrent_hash'))=? OR (media_id<>'' AND media_id=?))
 AND (COALESCE(json_extract(payload,'$.discovery.magnet'),'')<>'' OR json_extract(payload,'$.resource') IS NOT NULL OR phase<>'discovered'))
ORDER BY id LIMIT 1`, key, input.InfoHash, input.Code))
	if err == nil {
		if job.MediaID != "" && job.MediaID != input.Code {
			return MediaJob{}, false, catalog.ErrConflict
		}
		return job, false, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return MediaJob{}, false, err
	}
	var raw sql.NullString
	var searchedAt, state string
	err = tx.QueryRowContext(ctx, "SELECT result,searched_at,last_state FROM resource_search_cache WHERE code=? AND provider=?", input.Code, input.Provider).Scan(&raw, &searchedAt, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return MediaJob{}, false, catalog.ErrConflict
	}
	if err != nil {
		return MediaJob{}, false, err
	}
	if !raw.Valid || len(raw.String) > 128<<10 || state != "ready" || searchedAt != input.SearchedAt {
		return MediaJob{}, false, catalog.ErrConflict
	}
	var result javspengine.ResourceSearchResult
	if json.Unmarshal([]byte(raw.String), &result) != nil || result.Code != input.Code || result.Provider != input.Provider || result.State != "ready" || len(result.Items) > 50 {
		return MediaJob{}, false, catalog.ErrConflict
	}
	var selected *javspengine.ResourceCandidate
	for i := range result.Items {
		if result.Items[i].InfoHash == input.InfoHash {
			selected = &result.Items[i]
			break
		}
	}
	if selected == nil {
		return MediaJob{}, false, catalog.ErrConflict
	}
	source, err := url.Parse(selected.SourceURL)
	if err != nil || source.Host == "" || source.User != nil || (source.Scheme != "http" && source.Scheme != "https") || len(selected.SourceURL) > 1024 {
		return MediaJob{}, false, catalog.ErrInvalid
	}
	source.RawQuery, source.Fragment = "", ""
	values, err := readSettings(ctx, tx)
	if err != nil {
		return MediaJob{}, false, err
	}
	settings := resourceSettings(values)
	identity, err := resourceDownloaderIdentity(settings)
	if err != nil {
		return MediaJob{}, false, err
	}
	destination := intakeConfigFromSettings(values).SourceDirectory
	if !filepath.IsAbs(destination) || filepath.Clean(destination) == string(filepath.Separator) || hasParentSegment(destination) || strings.ContainsRune(destination, 0) {
		return MediaJob{}, false, catalog.ErrInvalid
	}
	job, err = enqueueMediaJobTx(tx, MediaJob{Key: key, Kind: "torrent", Phase: "discovered", MediaID: input.Code, Title: input.Code, TorrentHash: input.InfoHash, SourceURL: source.String(), IntakeSourceRoot: destination,
		Resource: &ResourceAcquisition{Provider: input.Provider, SearchedAt: searchedAt, Magnet: "magnet:?xt=urn:btih:" + input.InfoHash, DownloadPath: destination, DownloaderIdentity: identity}})
	if err != nil {
		return MediaJob{}, false, err
	}
	return job, true, tx.Commit()
}
