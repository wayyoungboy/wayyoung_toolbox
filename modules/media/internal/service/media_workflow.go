package service

import (
	"context"
	"fmt"
)

// Show durable failures even when the scheduler is idle and the component is
// healthy. Run logs can rotate, so they cannot supply these counts.
func (d *Database) annotatePipelineQueue(ctx context.Context, stages []pipelineStage) error {
	rows, err := d.db.QueryContext(ctx, `SELECT kind,phase,status,COUNT(*) FROM media_jobs
WHERE COALESCE(json_extract(payload,'$.archived'),0)=0 AND status IN ('pending','running','failed','partial','timed_out','needs_attention')
GROUP BY kind,phase,status`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type counts struct{ pending, running, attention int }
	byStage := map[string]*counts{}
	for rows.Next() {
		var kind, phase, status string
		var count int
		if err := rows.Scan(&kind, &phase, &status, &count); err != nil {
			return err
		}
		key := ""
		switch {
		case kind == "movie" && phase == "catalog":
			key = "catalog"
		case kind == "movie":
			key = "javsp"
		case phase == "discovered":
			key = "rss"
		case phase == "download":
			key = "qb"
		case phase == "transfer" || phase == "qb_cleanup":
			key = "intake"
		}
		if key == "" {
			continue
		}
		if byStage[key] == nil {
			byStage[key] = &counts{}
		}
		c := byStage[key]
		switch status {
		case "pending":
			c.pending += count
		case "running":
			c.running += count
		default:
			c.attention += count
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range stages {
		stage := &stages[i]
		c := byStage[stage.Key]
		if c == nil {
			continue
		}
		stage.AttentionCount = c.attention
		if stage.Key == "javsp" || stage.Key == "catalog" {
			stage.Count = c.pending + c.running
			stage.Summary = fmt.Sprintf("%d 条排队 · %d 条运行", c.pending, c.running)
		}
		if c.running > 0 && stage.Status != "error" {
			stage.Status = "running"
		}
		if c.attention > 0 {
			stage.Summary += fmt.Sprintf(" · %d 条待处理", c.attention)
			if stage.Status != "error" && stage.Status != "running" {
				stage.Status = "warning"
			}
		}
	}
	return nil
}

// A resume receipt proves ownership of the original files, but is not
// permission to publish into an obsolete media library after a settings change.
func validateMovieResumeOutput(cfg JavspConfig, plan queuedMoviePlan) error {
	if cfg.MoveFiles != plan.Plan.MoveFiles {
		return fmt.Errorf("%w：整理方式已变更，旧任务保留原文件与处理记录，请先核对", errIntakeConflict)
	}
	if !cfg.MoveFiles {
		return nil
	}
	root, available, err := plannedJavspOutputRoot(cfg)
	if err != nil || !available {
		return fmt.Errorf("%w：当前整理完成目录不可用，请检查目录设置", errIntakeConflict)
	}
	paths := moviePlanArtifacts(plan.Plan)
	if plan.Plan.ExtraFanartDirectory != "" {
		paths = append(paths, plan.Plan.ExtraFanartDirectory)
	}
	for _, file := range plan.Plan.Files {
		paths = append(paths, file.Destination)
	}
	for _, path := range paths {
		if _, err := moviePlanRelative(root, path); err != nil {
			return fmt.Errorf("%w：整理完成目录已变更，旧任务的输出不在当前媒体库内；已保留原文件与处理记录，请先核对", errIntakeConflict)
		}
	}
	return nil
}
