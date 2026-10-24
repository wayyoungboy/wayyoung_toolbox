package service

import (
	"context"
	"database/sql"
	"fmt"

	javspengine "github.com/yuukiy/javsp-go/engine"
)

// Correlated with the outer media_jobs movie row. Prefix matching includes the
// separator, so /pending/pack cannot reserve /pending/pack2. This is checked at
// both selection and claim: intake can register between those two operations.
// Only a never-claimed transfer may release its reservation on cancellation.
// HoldFiles=false alone is not proof: a done file receipt can survive a crash
// before the DB advances past transfer. Claim attempts are persisted before I/O.
const unpublishedIntakeMatchSQL = `intake.kind='torrent' AND intake.phase IN ('transfer','qb_cleanup')
 AND NOT (intake.phase='transfer' AND intake.status='canceled' AND intake.attempts=0 AND COALESCE(json_extract(intake.payload,'$.hold_files'),0)=0)
 AND json_extract(intake.payload,'$.organize')=1
 AND COALESCE(json_extract(intake.payload,'$.intake_movies_ready'),0)=0
 AND COALESCE(json_extract(intake.payload,'$.target_path'),'')<>''
 AND EXISTS (
   SELECT 1 FROM json_each(COALESCE(json_extract(media_jobs.payload,'$.source_paths'),json_array(json_extract(media_jobs.payload,'$.source_path')))) AS source
   WHERE source.value=json_extract(intake.payload,'$.target_path')
     OR substr(source.value,1,length(json_extract(intake.payload,'$.target_path'))+1)=json_extract(intake.payload,'$.target_path')||'/'
 )`

const movieHasUnpublishedIntakeSQL = `EXISTS (
 SELECT 1 FROM media_jobs AS intake WHERE ` + unpublishedIntakeMatchSQL + `)`

func (s *Service) discoverIntakeMovies(ctx context.Context, job MediaJob) ([]MediaJob, error) {
	if len(job.PlannedPaths) == 0 {
		return nil, fmt.Errorf("%w：未找到可整理的视频，请检查目标目录", errIntakeConflict)
	}
	root, err := canonicalDirectory(job.IntakeTargetRoot)
	if err != nil || root == "/" {
		return nil, fmt.Errorf("%w：搬运任务缺少有效的未处理目录记录", errIntakeConflict)
	}
	// Keep the root captured when this transfer was registered. A later path
	// settings save must not redirect discovery, even after a service restart.
	// Validate every explicit file; never fall back to a directory-wide scan.
	root, files, err := queuedMovieSources(job.IntakeTargetRoot, MediaJob{SourcePaths: job.PlannedPaths})
	if err != nil {
		return nil, fmt.Errorf("%w：%v", errIntakeConflict, err)
	}
	cfg, err := s.db.JavspConfigPrivate()
	if err != nil {
		return nil, err
	}
	// Resolve exclusions before narrowing the input root. Legacy output patterns
	// are relative to the saved scan root, not to this transfer's subdirectory.
	var excluded []string
	if cfg.MoveFiles {
		output, available, err := plannedJavspOutputRoot(cfg)
		if err != nil || !available {
			return nil, fmt.Errorf("%w：请先设置明确的整理完成目录", errIntakeConflict)
		}
		for _, file := range files {
			if configuredPathWithin(output, file) || pathWithinRoot(output, file) {
				return nil, fmt.Errorf("%w：影片位于整理完成目录内，已从入库发现中排除", errIntakeConflict)
			}
		}
		if output != root && configuredPathWithin(root, output) {
			excluded = []string{output}
		}
	}
	cfg.InputDirectory = root
	data, err := s.javsp.prepareConfigDataContext(ctx, cfg, false)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	options := javspengine.Options{ConfigData: data, ResourceRoot: s.javsp.workRoot, InputDirectory: cfg.InputDirectory, InputFiles: files}
	options.ExcludedDirectories = excluded
	discover := s.discoverMovies
	if discover == nil {
		discover = javspengine.Discover
	}
	result, err := discover(ctx, options)
	if err != nil {
		return nil, err
	}
	children, err := discoveredMovieJobs(ctx, cfg, 0, result)
	if err != nil {
		return nil, err
	}
	if len(children) == 0 {
		return nil, fmt.Errorf("%w：当前扫描规则未选出影片，请检查大小、扩展名及忽略规则", errIntakeConflict)
	}
	for i := range children {
		children[i].ParentID = job.ID
		children[i].TorrentHash, children[i].TorrentAddedOn = job.TorrentHash, job.TorrentAddedOn
		children[i].RSSID, children[i].RSSItemKey, children[i].SourceURL = job.RSSID, job.RSSItemKey, job.SourceURL
		children[i].Resource = job.Resource
	}
	return children, nil
}

// Publishing children and recording the handoff are atomic. This checkpoint
// does not change the claim revision: cancellation still addresses the same
// worker. Once committed, retries never discover these files again.
func (d *Database) saveIntakeMovies(ctx context.Context, id, version int64, children []MediaJob) (MediaJob, int, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return MediaJob{}, 0, err
	}
	defer tx.Rollback()
	job, err := scanMediaJob(tx.QueryRowContext(ctx, `SELECT `+mediaJobColumns+` FROM media_jobs WHERE id=?`, id))
	if err != nil {
		return MediaJob{}, 0, err
	}
	if job.Version != version || job.Status != "running" || job.Kind != "torrent" || job.Phase != "qb_cleanup" {
		return MediaJob{}, 0, errMediaJobConflict
	}
	if job.IntakeMoviesReady {
		return job, 0, nil
	}
	if len(children) == 0 {
		return MediaJob{}, 0, fmt.Errorf("整理任务不能为空")
	}
	created := 0
	linked := map[int64]bool{}
	for _, candidate := range children {
		if candidate.ParentID != id {
			return MediaJob{}, 0, fmt.Errorf("无效的整理子任务来源")
		}
		child, isNew, err := enqueueDiscoveredMovieTx(ctx, tx, candidate)
		if err != nil {
			return MediaJob{}, 0, err
		}
		if child.ParentID == 0 {
			child.ParentID = id
			child.TorrentHash, child.TorrentAddedOn = candidate.TorrentHash, candidate.TorrentAddedOn
			child.RSSID, child.RSSItemKey, child.SourceURL = candidate.RSSID, candidate.RSSItemKey, candidate.SourceURL
			child.Resource = candidate.Resource
			if _, err := tx.ExecContext(ctx, `UPDATE media_jobs SET parent_id=?,payload=? WHERE id=?`, id, encodeJSON(child), child.ID); err != nil {
				return MediaJob{}, 0, err
			}
		}
		if !linked[child.ID] {
			job.ChildJobIDs = append(job.ChildJobIDs, child.ID)
			linked[child.ID] = true
			if err := linkIntakeMovieEvent(ctx, tx, child, id); err != nil {
				return MediaJob{}, 0, err
			}
		}
		if isNew {
			created++
		}
	}
	job.IntakeMoviesReady, job.UpdatedAt = true, nowUTC()
	res, err := tx.ExecContext(ctx, `UPDATE media_jobs SET payload=?,updated_at=? WHERE id=? AND version=? AND status='running'`, encodeJSON(job), job.UpdatedAt, id, version)
	if err != nil {
		return MediaJob{}, 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return MediaJob{}, 0, err
	}
	if n != 1 {
		return MediaJob{}, 0, errMediaJobConflict
	}
	if err := insertMediaEvent(tx, job, "intake_movies_published"); err != nil {
		return MediaJob{}, 0, err
	}
	if err := tx.Commit(); err != nil {
		return MediaJob{}, 0, err
	}
	return job, created, nil
}

func linkIntakeMovieEvent(ctx context.Context, tx *sql.Tx, child MediaJob, parentID int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO media_job_events(job_id,phase,status,reason,message,created_at) VALUES(?,?,?,?,?,?)`, child.ID, child.Phase, child.Status, "intake_linked", fmt.Sprintf("关联搬运任务 %d", parentID), nowUTC())
	return err
}
