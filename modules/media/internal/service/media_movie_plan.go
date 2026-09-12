package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
)

type queuedMovieFile struct {
	Identity string `json:"identity"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
	Done     bool   `json:"done"`
}

type queuedMoviePlan struct {
	Root          string                        `json:"root"`
	SourceRoot    string                        `json:"source_root,omitempty"`
	Plan          JavspMoviePlan                `json:"plan"`
	Files         []queuedMovieFile             `json:"files"`
	MetadataReady bool                          `json:"metadata_ready"`
	Artifacts     map[string]intakeContentEntry `json:"artifacts,omitempty"`
}

// Older receipts used one root for both input and artifacts. Keep those plans
// readable while binding new separate-directory moves to their original roots.
func (plan queuedMoviePlan) sourceRoot() string {
	if plan.SourceRoot != "" {
		return plan.SourceRoot
	}
	return plan.Root
}

// Claim and history creation are one transaction. A storage error must leave a
// pending job consumable, without requiring a service restart to clear a claim.
func (d *Database) claimMovieWithHistory(id, version int64) (MediaJob, int64, error) {
	var runID int64
	job, err := d.changeMediaJobTx(id, version, "claimed", func(tx *sql.Tx, job *MediaJob) error {
		if job.Status != "pending" || job.Kind != "movie" || job.Phase != "organize" {
			return errMediaJobConflict
		}
		var waiting bool
		if err := tx.QueryRow(`SELECT `+movieHasUnpublishedIntakeSQL+` FROM media_jobs WHERE id=?`, job.ID).Scan(&waiting); err != nil {
			return err
		}
		if waiting {
			return errMediaJobConflict
		}
		result, err := tx.Exec(`INSERT INTO javsp_runs(trigger_type,status,started_at) VALUES('queue','running',?)`, nowUTC())
		if err != nil {
			return err
		}
		runID, err = result.LastInsertId()
		if err != nil {
			return err
		}
		job.Status = "running"
		job.Attempts++
		job.LastJavspRunID = runID
		return nil
	})
	if err != nil {
		return MediaJob{}, 0, err
	}
	return job, runID, nil
}

// Resolve root aliases, but never resolve the selected leaf or links beneath
// the configured root before validation: a link is not the film that was queued.
func queuedMovieSource(configuredRoot, path string) (string, string, error) {
	canonical, err := filepath.EvalSymlinks(configuredRoot)
	if err != nil {
		return "", "", err
	}
	rel, err := intakeRelativePath(configuredRoot, canonical, path)
	if err != nil {
		return "", "", fmt.Errorf("%w：%v", errIntakeConflict, err)
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return "", "", err
	}
	defer root.Close()
	if err := rejectIntakeLinks(root, rel, false); err != nil {
		return "", "", fmt.Errorf("%w：%v", errIntakeConflict, err)
	}
	info, err := root.Lstat(rel)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("%w：整理来源不是普通视频文件", errIntakeConflict)
	}
	return canonical, filepath.Join(canonical, rel), nil
}

func (job MediaJob) movieSources() []string {
	if len(job.SourcePaths) > 0 {
		return append([]string(nil), job.SourcePaths...)
	}
	return []string{job.SourcePath}
}

func queuedMovieSources(configuredRoot string, job MediaJob) (string, []string, error) {
	// New jobs own the scan boundary captured at discovery. For older jobs,
	// prefer their saved intention; only never-planned legacy jobs use settings.
	if job.MovieSourceRoot != "" {
		configuredRoot = job.MovieSourceRoot
	} else if job.MoviePlan != nil {
		configuredRoot = job.MoviePlan.sourceRoot()
	}
	var canonical string
	paths := []string{}
	seen := map[string]bool{}
	sources := job.movieSources()
	if len(job.SourceIdentities) != 0 && len(job.SourceIdentities) != len(sources) {
		return "", nil, fmt.Errorf("%w：整理来源身份记录不完整", errIntakeConflict)
	}
	for index, path := range sources {
		root, source, err := queuedMovieSource(configuredRoot, path)
		if err != nil {
			return "", nil, err
		}
		if seen[source] {
			return "", nil, fmt.Errorf("%w：整理任务包含重复的来源文件", errIntakeConflict)
		}
		if len(job.SourceIdentities) > 0 {
			info, err := os.Lstat(source)
			if err != nil {
				return "", nil, err
			}
			identity := job.SourceIdentities[index]
			if !info.Mode().IsRegular() || identity.Identity == "" || identity.Identity != intakeFileIdentity(info) || identity.Size != info.Size() || identity.Modified != info.ModTime().UnixNano() {
				return "", nil, fmt.Errorf("%w：入队后的来源文件已变化：%s", errIntakeConflict, source)
			}
		}
		canonical = root
		seen[source] = true
		paths = append(paths, source)
	}
	return canonical, paths, nil
}

// File checkpoints belong to one claim. They do not change its lifecycle
// revision, so an accepted cancel remains addressable while a hook saves its
// progress. The status/revision predicate and event are committed atomically.
func (d *Database) SaveMoviePlan(id, version int64, plan queuedMoviePlan) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	job, err := scanMediaJob(tx.QueryRow(`SELECT `+mediaJobColumns+` FROM media_jobs WHERE id=?`, id))
	if err != nil {
		return err
	}
	if job.Version != version || job.Status != "running" || job.Kind != "movie" || job.Phase != "organize" {
		return errMediaJobConflict
	}
	job.MoviePlan = &plan
	job.UpdatedAt = nowUTC()
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE media_jobs SET payload=?,updated_at=? WHERE id=? AND version=? AND status='running'`, string(data), job.UpdatedAt, id, version)
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errMediaJobConflict
	}
	if err := insertMediaEvent(tx, job, "organize_checkpoint"); err != nil {
		return err
	}
	return tx.Commit()
}

func moviePlanRelative(root, path string) (string, error) {
	if !filepath.IsAbs(path) || hasParentSegment(path) || strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("%w：无效的整理路径", errIntakeConflict)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) || rel == "." || strings.HasPrefix(rel, ".media-center-intake") {
		return "", fmt.Errorf("%w：整理路径超出配置目录", errIntakeConflict)
	}
	return rel, nil
}

func moviePlanArtifacts(plan JavspMoviePlan) []string {
	paths := []string{}
	for _, path := range []string{plan.NFOPath, plan.PosterPath, plan.FanartPath} {
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func prepareMoviePlan(ctx context.Context, job MediaJob, cfg JavspConfig, plan JavspMoviePlan) (queuedMoviePlan, error) {
	stored := queuedMoviePlan{Root: cfg.InputDirectory, Plan: plan}
	if err := ctx.Err(); err != nil {
		return stored, err
	}
	if len(plan.Files) == 0 || len(plan.Files) != len(job.movieSources()) || plan.NFOPath == "" {
		return stored, fmt.Errorf("%w：整理任务必须覆盖该影片所有明确的来源文件", errIntakeConflict)
	}
	sourceRoot, expected, err := queuedMovieSources(cfg.InputDirectory, job)
	if err != nil {
		return stored, err
	}
	stored.Root = sourceRoot
	outputRoot := filepath.Dir(expected[0])
	if plan.MoveFiles {
		var available bool
		outputRoot, available, err = plannedJavspOutputRoot(cfg)
		if err != nil || !available {
			return stored, fmt.Errorf("%w：未配置明确的整理输出根目录", errIntakeConflict)
		}
		if cfg.OutputDirectory != "" {
			stored.Root = outputRoot
			stored.SourceRoot = sourceRoot
		} else {
			stored.Root, err = canonicalDirectory(cfg.InputDirectory)
			if err != nil {
				return stored, err
			}
			if stored.Root != sourceRoot {
				stored.SourceRoot = sourceRoot
			}
		}
	}
	sources := make(map[string]bool, len(expected))
	for _, source := range expected {
		sources[source] = true
	}
	seenSources, seenDestinations := map[string]bool{}, map[string]bool{}
	for _, file := range plan.Files {
		if !sources[file.Source] || seenSources[file.Source] || seenDestinations[file.Destination] || sources[file.Destination] && file.Destination != file.Source {
			return stored, fmt.Errorf("%w：整理来源或目标与队列不一致", errIntakeConflict)
		}
		seenSources[file.Source], seenDestinations[file.Destination] = true, true
	}
	for _, artifact := range moviePlanArtifacts(plan) {
		if sources[artifact] {
			return stored, fmt.Errorf("%w：元数据路径不能覆盖来源视频", errIntakeConflict)
		}
	}
	root, err := os.OpenRoot(stored.Root)
	if err != nil {
		return stored, err
	}
	defer root.Close()
	source, err := os.OpenRoot(stored.sourceRoot())
	if err != nil {
		return stored, err
	}
	defer source.Close()
	paths := moviePlanArtifacts(plan)
	if plan.ExtraFanartDirectory != "" {
		paths = append(paths, plan.ExtraFanartDirectory)
	}
	for _, file := range plan.Files {
		rel, err := moviePlanRelative(stored.sourceRoot(), file.Source)
		if err != nil {
			return stored, err
		}
		if err := rejectIntakeLinks(source, rel, false); err != nil {
			return stored, err
		}
		info, err := source.Lstat(rel)
		if err != nil {
			return stored, err
		}
		if !info.Mode().IsRegular() || intakeFileIdentity(info) == "" {
			return stored, fmt.Errorf("%w：来源不是可校验的普通文件", errIntakeConflict)
		}
		stored.Files = append(stored.Files, queuedMovieFile{Identity: intakeFileIdentity(info), Size: info.Size(), Modified: info.ModTime().UnixNano()})
		paths = append(paths, file.Destination, strings.TrimSuffix(file.Destination, filepath.Ext(file.Destination))+".nfo")
	}
	for _, path := range paths {
		rel, err := moviePlanRelative(stored.Root, path)
		if err != nil {
			return stored, err
		}
		if _, err := moviePlanRelative(outputRoot, path); err != nil && (plan.MoveFiles || !sources[path]) {
			return stored, fmt.Errorf("%w：输出路径与整理目录不一致", errIntakeConflict)
		}
		if err := rejectIntakeLinks(root, rel, true); err != nil {
			return stored, fmt.Errorf("%w：%v", errIntakeConflict, err)
		}
		// A saved intention does not prove ownership of existing artifacts.
		// Before metadata_ready, any existing output requires manual resolution,
		// even on retry. Verified metadata_ready plans bypass scraping entirely.
		if !sources[path] {
			if _, err := root.Lstat(rel); err == nil {
				return stored, fmt.Errorf("%w：整理目标已经存在：%s", errIntakeConflict, path)
			} else if !os.IsNotExist(err) {
				return stored, err
			}
		}
	}
	if job.MoviePlan != nil {
		if job.MoviePlan.Root != stored.Root || job.MoviePlan.sourceRoot() != stored.sourceRoot() || !reflect.DeepEqual(job.MoviePlan.Plan, plan) || !reflect.DeepEqual(job.MoviePlan.Files, stored.Files) {
			return stored, fmt.Errorf("%w：原整理计划或文件身份已变化", errIntakeConflict)
		}
		return *job.MoviePlan, nil
	}
	return stored, nil
}

func syncMovieArtifacts(ctx context.Context, root *os.Root, plan JavspMoviePlan) error {
	paths := moviePlanArtifacts(plan)
	if plan.ExtraFanartDirectory != "" {
		rel, err := moviePlanRelative(root.Name(), plan.ExtraFanartDirectory)
		if err != nil {
			return err
		}
		if err := rejectIntakeLinks(root, rel, true); err != nil {
			return err
		}
		err = fs.WalkDir(root.FS(), rel, func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return walkErr
			}
			if len(paths) >= maxMaintenanceEntries {
				return fmt.Errorf("整理元数据条目过多")
			}
			if !entry.IsDir() {
				paths = append(paths, filepath.Join(root.Name(), path))
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := moviePlanRelative(root.Name(), path)
		if err != nil {
			return err
		}
		if err := rejectIntakeLinks(root, rel, false); err != nil {
			return err
		}
		info, err := root.Lstat(rel)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("元数据不是普通文件")
		}
		f, err := root.Open(rel)
		if err != nil {
			return err
		}
		err = errors.Join(f.Sync(), f.Close())
		if err != nil {
			return err
		}
		if err := syncIntakePathParents(root, rel); err != nil {
			return err
		}
	}
	return nil
}

func sameMovieFile(info fs.FileInfo, expected queuedMovieFile) bool {
	return info.Mode().IsRegular() && intakeFileIdentity(info) == expected.Identity && info.Size() == expected.Size && info.ModTime().UnixNano() == expected.Modified
}

// Capture only the artifacts named by this plan. This is not a library scan;
// full hashes distinguish replacements even when size/timestamps are retained.
func movieArtifactManifest(ctx context.Context, root *os.Root, plan JavspMoviePlan) (map[string]intakeContentEntry, error) {
	manifest := map[string]intakeContentEntry{}
	for _, path := range moviePlanArtifacts(plan) {
		rel, err := moviePlanRelative(root.Name(), path)
		if err != nil {
			return nil, err
		}
		if err := rejectIntakeLinks(root, rel, false); err != nil {
			return nil, err
		}
		info, err := root.Lstat(rel)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("元数据不是普通文件")
		}
		entry, err := intakeEntry(ctx, root, rel, info)
		if err != nil {
			return nil, err
		}
		manifest[rel] = entry
	}
	if plan.ExtraFanartDirectory != "" {
		rel, err := moviePlanRelative(root.Name(), plan.ExtraFanartDirectory)
		if err != nil {
			return nil, err
		}
		if err := rejectIntakeLinks(root, rel, true); err != nil {
			return nil, err
		}
		entries, err := intakeContentManifest(ctx, root, rel)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil {
			for path, entry := range entries {
				manifest[filepath.Join(rel, path)] = entry
			}
		}
	}
	return manifest, nil
}

func (s *Service) moveQueuedMovie(ctx context.Context, job MediaJob, plan *queuedMoviePlan) (JavspMoveResult, error) {
	result := JavspMoveResult{OutputPaths: []string{}}
	root, err := os.OpenRoot(plan.Root)
	if err != nil {
		return result, err
	}
	defer root.Close()
	source, err := os.OpenRoot(plan.sourceRoot())
	if err != nil {
		return result, err
	}
	defer source.Close()
	if !plan.MetadataReady {
		if err := syncMovieArtifacts(ctx, root, plan.Plan); err != nil {
			return result, err
		}
		plan.Artifacts, err = movieArtifactManifest(ctx, root, plan.Plan)
		if err != nil {
			return result, err
		}
		plan.MetadataReady = true
		if err := s.db.SaveMoviePlan(job.ID, job.Version, *plan); err != nil {
			return result, err
		}
	} else {
		current, err := movieArtifactManifest(ctx, root, plan.Plan)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return result, fmt.Errorf("%w：整理元数据无法复核：%v", errIntakeConflict, err)
		}
		if len(plan.Artifacts) == 0 || !equalIntakeManifests(plan.Artifacts, current) {
			return result, fmt.Errorf("%w：整理元数据已变化或缺少完整校验记录", errIntakeConflict)
		}
	}
	if len(plan.Files) != len(plan.Plan.Files) {
		return result, fmt.Errorf("%w：整理凭据不完整", errIntakeConflict)
	}
	for index, file := range plan.Plan.Files {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		src, err := moviePlanRelative(plan.sourceRoot(), file.Source)
		if err != nil {
			return result, err
		}
		dst, err := moviePlanRelative(plan.Root, file.Destination)
		if err != nil {
			return result, err
		}
		if err := rejectIntakeLinks(source, src, true); err != nil {
			return result, err
		}
		if err := rejectIntakeLinks(root, dst, true); err != nil {
			return result, err
		}
		info, targetErr := root.Lstat(dst)
		if targetErr == nil {
			if !sameMovieFile(info, plan.Files[index]) {
				return result, fmt.Errorf("%w：整理目标文件身份不匹配", errIntakeConflict)
			}
			if plan.Plan.MoveFiles && !plan.Plan.HardLink {
				if _, err := source.Lstat(src); !os.IsNotExist(err) {
					return result, fmt.Errorf("%w：来源路径已被重新占用", errIntakeConflict)
				}
			}
		} else if !os.IsNotExist(targetErr) {
			return result, targetErr
		} else {
			info, err := source.Lstat(src)
			if err != nil {
				return result, err
			}
			if !sameMovieFile(info, plan.Files[index]) {
				return result, fmt.Errorf("%w：来源文件身份已变化", errIntakeConflict)
			}
			if !plan.Plan.MoveFiles {
				return result, fmt.Errorf("%w：原视频已不存在", errIntakeConflict)
			}
			if plan.Plan.HardLink {
				err = linkMovieNoReplace(source, src, root, dst)
			} else {
				err = renameIntakeNoReplace(source, src, root, dst)
			}
			if errors.Is(err, syscall.EXDEV) {
				return result, fmt.Errorf("%w：跨文件系统搬运需要人工处理，来源保留", errIntakeConflict)
			}
			if err != nil {
				return result, err
			}
		}
		if err := syncIntakePathParents(root, dst); err != nil {
			return result, err
		}
		if err := syncIntakePathParents(source, src); err != nil {
			return result, err
		}
		result.OutputPaths = append(result.OutputPaths, file.Destination)
		plan.Files[index].Done = true
		if err := s.db.SaveMoviePlan(job.ID, job.Version, *plan); err != nil {
			return result, err
		}
	}
	return result, nil
}
