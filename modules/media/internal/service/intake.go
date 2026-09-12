package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type QBTorrent struct {
	Hash        string  `json:"hash"`
	Name        string  `json:"name"`
	ContentPath string  `json:"content_path"`
	State       string  `json:"state"`
	Progress    float64 `json:"progress"`
	AmountLeft  int64   `json:"amount_left"`
	AddedOn     int64   `json:"added_on"`
}

type IntakeCandidate struct {
	SourceRoot  string `json:"source_root,omitempty"`
	JobID       int64  `json:"job_id,omitempty"`
	AddedOn     int64  `json:"added_on"`
	Hash        string `json:"hash"`
	Name        string `json:"name"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	State       string `json:"state"`
}

type IntakeRunResult struct {
	Queued            int               `json:"queued"`
	Pending           int               `json:"pending"`
	JavspQueued       int               `json:"javsp_queued"`
	Scanned           int               `json:"scanned"`
	Candidates        []IntakeCandidate `json:"candidates"`
	Moved             int               `json:"moved"`
	DuplicatesCleaned int               `json:"duplicates_cleaned"`
	TasksDeleted      int               `json:"tasks_deleted"`
	Failed            int               `json:"failed"`
	Errors            []string          `json:"errors"`
	JavspStarted      bool              `json:"javsp_started"`
	JavspError        string            `json:"javsp_error,omitempty"`
}

func torrentCompleted(torrent QBTorrent) bool {
	if torrent.Progress < 1 || torrent.AmountLeft != 0 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(torrent.State)) {
	case "error", "missingfiles", "unknown":
		return false
	default:
		return true
	}
}

func buildIntakeCandidates(torrents []QBTorrent, source, destination string) ([]IntakeCandidate, error) {
	config, err := validateIntakeConfig(IntakeConfig{
		SourceDirectory: source, DestinationDirectory: destination, ScheduleMinutes: 15,
	})
	if err != nil {
		return nil, err
	}
	candidates := make([]IntakeCandidate, 0)
	root, err := os.OpenRoot(config.SourceDirectory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, torrent := range torrents {
		if !torrentCompleted(torrent) {
			continue
		}
		contentPath := strings.TrimSpace(torrent.ContentPath)
		if contentPath == "" || !filepath.IsAbs(contentPath) || hasParentSegment(contentPath) {
			continue
		}
		relative, relativeErr := intakeRelativePath(source, config.SourceDirectory, contentPath)
		if relativeErr != nil || rejectIntakeLinks(root, relative, false) != nil {
			continue
		}
		candidates = append(candidates, IntakeCandidate{
			SourceRoot: config.SourceDirectory,
			Hash:       torrent.Hash, AddedOn: torrent.AddedOn, Name: torrent.Name, Source: filepath.Join(config.SourceDirectory, relative),
			Destination: filepath.Join(config.DestinationDirectory, relative), State: torrent.State,
		})
	}
	return candidates, nil
}

func moveIntakeCandidate(candidate IntakeCandidate, sourceRoot, destinationRoot string) (string, error) {
	return moveIntakeCandidateContext(context.Background(), candidate, sourceRoot, destinationRoot)
}

func moveIntakeCandidateContext(ctx context.Context, candidate IntakeCandidate, sourceRoot, destinationRoot string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	config, err := validateIntakeConfig(IntakeConfig{
		SourceDirectory: sourceRoot, DestinationDirectory: destinationRoot, ScheduleMinutes: 15,
	})
	if err != nil {
		return "", err
	}
	relative, err := intakeRelativePath(sourceRoot, config.SourceDirectory, candidate.Source)
	if err != nil {
		return "", err
	}
	targetRelative, err := intakeRelativePath(destinationRoot, config.DestinationDirectory, candidate.Destination)
	if err != nil || relative != targetRelative {
		return "", fmt.Errorf("目标路径与来源目录结构不一致")
	}
	source, err := os.OpenRoot(config.SourceDirectory)
	if err != nil {
		return "", err
	}
	defer source.Close()
	destination, err := os.OpenRoot(config.DestinationDirectory)
	if err != nil {
		return "", err
	}
	defer destination.Close()
	// Recovery must be selected by its durable journal before checking public
	// source/target paths: either may legitimately have disappeared meanwhile.
	if exists, err := intakeOperationExists(source, destination, relative, candidate.JobID); err != nil {
		return "", fmt.Errorf("%w：%w", errIntakePending, err)
	} else if exists {
		journal, err := readIntakeOperation(source, destination, relative, candidate.JobID)
		if err != nil {
			return "", fmt.Errorf("%w：%w", errIntakePending, err)
		}
		if journal.Outcome == "moved" {
			return moveIntakeWithReceipt(ctx, source, destination, relative, candidate.JobID)
		}
		if err := removeVerifiedIntakeDuplicate(ctx, source, destination, relative, candidate.JobID); err != nil {
			return "", err
		}
		return "duplicate_removed", nil
	}
	if err := rejectIntakeLinks(destination, relative, true); err != nil {
		return "", err
	}
	if _, err := destination.Lstat(relative); err == nil {
		if err := removeVerifiedIntakeDuplicate(ctx, source, destination, relative, candidate.JobID); err != nil {
			return "", err
		}
		return "duplicate_removed", nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return moveIntakeWithReceipt(ctx, source, destination, relative, candidate.JobID)
}

func intakeRelativePath(configured, canonical, target string) (string, error) {
	if !filepath.IsAbs(target) || hasParentSegment(target) {
		return "", fmt.Errorf("搬运路径无效")
	}
	for _, root := range []string{configured, canonical} {
		rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !strings.HasPrefix(rel, ".media-center-intake") {
			return rel, nil
		}
	}
	// A configured root may already be canonical (for example /private/var on
	// macOS), while qB reports its /var alias. Resolve ancestors only, never the
	// content path or descendants within the configured root.
	for parent := filepath.Dir(target); parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil && resolved == canonical {
			rel, err := filepath.Rel(parent, target)
			if err == nil && filepath.IsLocal(rel) && !strings.HasPrefix(rel, ".media-center-intake") {
				return rel, nil
			}
		}
	}
	return "", fmt.Errorf("搬运路径不在已配置目录内")
}

func rejectIntakeLinks(root *os.Root, relative string, allowMissing bool) error {
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	for i := range parts {
		info, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if allowMissing && os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("拒绝搬运符号链接")
		}
	}
	return nil
}

func renameIntakeNoReplace(source *os.Root, old string, destination *os.Root, next string) error {
	if err := rejectIntakeLinks(source, filepath.Dir(old), false); err != nil {
		return err
	}
	if err := rejectIntakeLinks(destination, filepath.Dir(next), false); err != nil {
		return err
	}
	src, err := source.Open(filepath.Dir(old))
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := destination.Open(filepath.Dir(next))
	if err != nil {
		return err
	}
	defer dst.Close()
	if err := renameIntakeAtNoReplace(src, filepath.Base(old), dst, filepath.Base(next)); err != nil {
		return err
	}
	if err := dst.Sync(); err != nil {
		return fmt.Errorf("%w：同步目标目录：%w", errIntakePending, err)
	}
	if err := src.Sync(); err != nil {
		return fmt.Errorf("%w：同步来源目录：%w", errIntakePending, err)
	}
	return nil
}
