package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Only complete, strictly named movies qualify. CD/part/numeric suffixes cause
// the entire media-ID group to be skipped, never treated as smaller duplicates.
var singleDuplicateMovie = regexp.MustCompile(`(?i)^(?:fc2(?:[-_ ]?ppv)?[-_ ]+\d{5,9}|[a-z0-9]{2,16}[-_]+\d{2,9})(?:[-_](?:uc|u|c))?$`)

type AutomaticDuplicateGroup struct {
	MediaID    string               `json:"media_id"`
	Keep       DuplicateVideoFile   `json:"keep"`
	Remove     []DuplicateVideoFile `json:"remove"`
	SkipReason string               `json:"skip_reason,omitempty"`
}

type AutomaticDuplicateResult struct {
	Directory      string                    `json:"directory"`
	ScannedVideos  int                       `json:"scanned_videos"`
	Groups         []AutomaticDuplicateGroup `json:"groups"`
	Deleted        int                       `json:"deleted"`
	ReclaimedBytes int64                     `json:"reclaimed_bytes"`
	Files          []string                  `json:"files"`
}

type automaticDuplicatePlan struct {
	AutomaticDuplicateResult
	rootInfo os.FileInfo
}

func planAutomaticDuplicates(root string, minimumAge time.Duration) (automaticDuplicatePlan, error) {
	scan, err := scanDuplicateVideos(root)
	if err != nil {
		return automaticDuplicatePlan{}, err
	}
	info, err := os.Lstat(scan.Directory)
	if err != nil {
		return automaticDuplicatePlan{}, err
	}
	plan := automaticDuplicatePlan{AutomaticDuplicateResult: AutomaticDuplicateResult{
		Directory: scan.Directory, ScannedVideos: scan.ScannedVideos,
		Groups: []AutomaticDuplicateGroup{}, Files: []string{},
	}, rootInfo: info}
	cutoff := time.Now().Add(-minimumAge)
	for _, group := range scan.Groups {
		files := group.Files
		sort.Slice(files, func(i, j int) bool {
			if files[i].Size != files[j].Size {
				return files[i].Size > files[j].Size
			}
			return files[i].Path < files[j].Path
		})
		item := AutomaticDuplicateGroup{MediaID: group.MediaID, Keep: files[0], Remove: files[1:]}
		for _, file := range files {
			base := strings.TrimSuffix(file.Name, filepath.Ext(file.Name))
			if !singleDuplicateMovie.MatchString(base) || strings.EqualFold(filepath.Ext(file.Name), ".strm") {
				item.SkipReason = "包含分段影片或 STRM，需人工判断"
				break
			}
			if file.Size == 0 || file.snapshot == nil || file.snapshot.ModTime().After(cutoff) {
				item.SkipReason = "包含空文件或近期修改的文件，暂不处理"
			}
		}
		if item.SkipReason == "" {
			// Require a readable keeper even when duplicate sizes differ.
			fingerprint, readErr := sampledVideoFingerprint(item.Keep.Path, item.Keep.Size)
			if readErr != nil {
				return automaticDuplicatePlan{}, fmt.Errorf("保留文件不可读：%w", readErr)
			}
			item.Keep.Fingerprint = fingerprint
		}
		plan.Groups = append(plan.Groups, item)
	}
	return plan, nil
}

func verifyAutomaticDuplicateFile(plan automaticDuplicatePlan, file DuplicateVideoFile) error {
	rootInfo, err := os.Lstat(plan.Directory)
	if err != nil || !rootInfo.IsDir() || !os.SameFile(rootInfo, plan.rootInfo) {
		return fmt.Errorf("维护根目录已变化")
	}
	resolved, err := filepath.EvalSymlinks(file.Path)
	if err != nil || resolved != file.Path || !pathWithinRoot(plan.Directory, resolved) {
		return fmt.Errorf("%s 路径已变化", file.Name)
	}
	info, err := os.Lstat(file.Path)
	if err != nil || !info.Mode().IsRegular() || file.snapshot == nil ||
		!os.SameFile(info, file.snapshot) || info.Size() != file.Size || !info.ModTime().Equal(file.snapshot.ModTime()) {
		return fmt.Errorf("%s 文件已变化或丢失", file.Name)
	}
	return nil
}

func executeAutomaticDuplicates(plan automaticDuplicatePlan) (AutomaticDuplicateResult, error) {
	result := plan.AutomaticDuplicateResult
	var failures []error
	for index, group := range plan.Groups {
		if group.SkipReason != "" {
			continue
		}
		var groupErr error
		for _, file := range append([]DuplicateVideoFile{group.Keep}, group.Remove...) {
			if groupErr = verifyAutomaticDuplicateFile(plan, file); groupErr != nil {
				break
			}
		}
		if groupErr == nil {
			for _, file := range group.Remove {
				if groupErr = removeAutomaticDuplicate(plan, group.Keep, file); groupErr != nil {
					break
				}
				result.Deleted++
				result.ReclaimedBytes += file.Size
				result.Files = append(result.Files, file.Path)
			}
		}
		if groupErr != nil {
			result.Groups[index].SkipReason = groupErr.Error()
			failures = append(failures, groupErr)
		}
	}
	return result, errors.Join(failures...)
}

func removeAutomaticDuplicate(plan automaticDuplicatePlan, keeper, candidate DuplicateVideoFile) error {
	if candidate.Path == keeper.Path || candidate.Size > keeper.Size {
		return fmt.Errorf("拒绝删除保留文件")
	}
	// Bind unlink to an open parent directory, so swapping an ancestor cannot
	// redirect it to another directory after validation.
	parent, err := os.OpenRoot(filepath.Dir(candidate.Path))
	if err != nil {
		return err
	}
	defer parent.Close()
	boundInfo, err := parent.Lstat(filepath.Base(candidate.Path))
	if err != nil || candidate.snapshot == nil || !os.SameFile(boundInfo, candidate.snapshot) {
		return fmt.Errorf("删除目标已变化")
	}
	for _, file := range []DuplicateVideoFile{keeper, candidate} {
		if err := verifyAutomaticDuplicateFile(plan, file); err != nil {
			return err
		}
	}
	fingerprint, err := sampledVideoFingerprint(keeper.Path, keeper.Size)
	if err != nil || fingerprint != keeper.Fingerprint {
		return fmt.Errorf("保留文件内容校验失败：%s", keeper.Name)
	}
	// Recheck the keeper after reading it; never count a missing or changed
	// keeper as the surviving copy.
	if err := verifyAutomaticDuplicateFile(plan, keeper); err != nil {
		return err
	}
	if err := verifyAutomaticDuplicateFile(plan, candidate); err != nil {
		return err
	}
	return parent.Remove(filepath.Base(candidate.Path))
}
