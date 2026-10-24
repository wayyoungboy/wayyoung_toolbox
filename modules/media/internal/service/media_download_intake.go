package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func torrentKey(torrent QBTorrent) string {
	return fmt.Sprintf("%s:%d", strings.ToLower(torrent.Hash), torrent.AddedOn)
}

func (d *Database) trackedDownloadsForIntake(ctx context.Context, torrents []QBTorrent) ([]MediaJob, error) {
	identities := []map[string]any{}
	for _, torrent := range torrents {
		if torrentCompleted(torrent) && torrent.AddedOn > 0 {
			identities = append(identities, map[string]any{"hash": strings.ToLower(torrent.Hash), "added_on": torrent.AddedOn})
		}
	}
	if len(identities) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(identities)
	if err != nil {
		return nil, err
	}
	// Match only this qB response's exact generations, not every historical
	// download or every torrent found under an old directory.
	rows, err := d.db.QueryContext(ctx, `SELECT `+mediaJobColumns+` FROM media_jobs
WHERE kind='torrent' AND phase='download' AND status='pending'
AND (json_extract(payload,'$.discovery') IS NOT NULL OR json_extract(payload,'$.resource') IS NOT NULL)
AND COALESCE(json_extract(payload,'$.intake_source_root'),'')<>''
AND EXISTS (SELECT 1 FROM json_each(?) remote WHERE json_extract(remote.value,'$.hash')=lower(json_extract(payload,'$.torrent_hash'))
AND json_extract(remote.value,'$.added_on')=json_extract(payload,'$.torrent_added_on')) ORDER BY id`, string(encoded))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []MediaJob{}
	for rows.Next() {
		job, err := scanMediaJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Service) intakeCandidates(ctx context.Context, torrents []QBTorrent, config IntakeConfig) ([]IntakeCandidate, []string, error) {
	candidates, currentRootErr := buildIntakeCandidates(torrents, config.SourceDirectory, config.DestinationDirectory)
	jobs, err := s.db.trackedDownloadsForIntake(ctx, torrents)
	if err != nil {
		return nil, nil, err
	}
	if currentRootErr != nil && len(jobs) == 0 {
		return nil, nil, currentRootErr
	}
	byTorrent := make(map[string]QBTorrent, len(torrents))
	byCandidate := make(map[string]IntakeCandidate, len(candidates))
	for _, torrent := range torrents {
		byTorrent[torrentKey(torrent)] = torrent
	}
	for _, candidate := range candidates {
		byCandidate[torrentKey(QBTorrent{Hash: candidate.Hash, AddedOn: candidate.AddedOn})] = candidate
	}
	issues := []string{}
	if currentRootErr != nil {
		issues = append(issues, "当前下载来源检查失败："+currentRootErr.Error())
	}
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		key := torrentKey(QBTorrent{Hash: job.TorrentHash, AddedOn: job.TorrentAddedOn})
		delete(byCandidate, key)
		matched, err := buildIntakeCandidates([]QBTorrent{byTorrent[key]}, job.IntakeSourceRoot, config.DestinationDirectory)
		if err != nil || len(matched) != 1 {
			detail := "下载路径不在登记的来源边界内或不可访问"
			if err != nil {
				detail = err.Error()
			}
			issues = append(issues, fmt.Sprintf("任务 #%d %s：%s", job.ID, job.Title, detail))
			continue
		}
		matched[0].JobID = job.ID
		byCandidate[key] = matched[0]
	}
	// Keep qB response order for stable previews; de-duplicate repeated rows.
	result := make([]IntakeCandidate, 0, len(byCandidate))
	for _, torrent := range torrents {
		key := torrentKey(torrent)
		if candidate, ok := byCandidate[key]; ok {
			result = append(result, candidate)
			delete(byCandidate, key)
		}
	}
	return result, issues, nil
}
