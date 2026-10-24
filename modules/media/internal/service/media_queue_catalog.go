package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

func catalogTargetsForMovie(job MediaJob) ([]catalog.RegistrationTarget, error) {
	plan := job.MoviePlan
	if plan == nil || !plan.MetadataReady || len(plan.Files) == 0 || len(plan.Files) > 1000 || len(plan.Files) != len(plan.Plan.Files) || len(job.OutputPaths) != len(plan.Files) {
		return nil, fmt.Errorf("%w：缺少完整的整理完成凭据，不能自动登记馆藏", catalog.ErrInvalid)
	}
	outputs := make(map[string]bool, len(job.OutputPaths))
	for _, filename := range job.OutputPaths {
		if outputs[filename] {
			return nil, fmt.Errorf("%w：整理输出重复", catalog.ErrInvalid)
		}
		outputs[filename] = true
	}
	targets := make([]catalog.RegistrationTarget, 0, len(plan.Files))
	for i, file := range plan.Plan.Files {
		proof := plan.Files[i]
		if !proof.Done || proof.Identity == "" || proof.Size <= 0 || !outputs[file.Destination] {
			return nil, fmt.Errorf("%w：整理输出尚未全部确认，不能自动登记馆藏", catalog.ErrInvalid)
		}
		target := catalog.RegistrationTarget{Path: file.Destination, Identity: proof.Identity, Size: proof.Size, ModifiedNS: proof.Modified}
		if plan.Plan.MoveFiles && !plan.Plan.HardLink && file.Source != file.Destination {
			target.PreviousPath = file.Source
		}
		targets = append(targets, target)
	}
	return targets, nil
}

// completeCatalogJobTx cannot publish a stale/canceled attempt. Catalog assets,
// the producer's final state, and its audit event commit together.
func completeCatalogJobTx(ctx context.Context, tx *sql.Tx, attempt MediaJob, result catalog.RegistrationResult) error {
	job, err := scanMediaJob(tx.QueryRowContext(ctx, `SELECT `+mediaJobColumns+` FROM media_jobs WHERE id=?`, attempt.ID))
	if err != nil {
		return err
	}
	if job.Archived || job.Version != attempt.Version || job.Status != "running" || job.Kind != "movie" || job.Phase != "catalog" {
		return errMediaJobConflict
	}
	job.Phase, job.Status, job.Error = "done", "success", ""
	job.HoldFiles = false
	job.CatalogRegistration = &result
	_, err = saveMediaJobChange(tx, job, attempt.Version, "catalog_registered")
	if err != nil {
		return err
	}
	return catalog.ReconcileWantsTx(ctx, tx, result.AssetIDs, "organizer", job.ID, nowUTC())
}

// The caller owns mediaFiles. This successor consumes only durable organizer
// outputs; retry/restart never invokes JAVSP, file discovery, or full Scan.
func (s *Service) registerQueuedMovie(parent context.Context, pending MediaJob) error {
	if err := parent.Err(); err != nil {
		return err
	}
	job, err := s.db.ClaimMediaJob(pending.ID, pending.Version)
	if errors.Is(err, errMediaJobConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	jobCtx, finish := s.mediaJobContext(parent, job)
	defer finish()
	ctx, cancel := context.WithTimeout(jobCtx, 2*time.Minute)
	defer cancel()
	targets, runErr := catalogTargetsForMovie(job)
	if runErr == nil {
		_, runErr = s.db.catalog.RegisterFiles(ctx, targets, func(tx *sql.Tx, result catalog.RegistrationResult) error {
			return completeCatalogJobTx(ctx, tx, job, result)
		})
	}
	if runErr == nil || errors.Is(runErr, errMediaJobConflict) {
		return nil
	}
	status, message := "failed", "馆藏登记失败；整理结果保留，重试只登记馆藏"
	switch {
	case errors.Is(runErr, catalog.ErrUnregistered):
		status, message = "needs_attention", "输出目录尚未登记到馆藏。请添加或重新关联媒体库，再重试；不会重新整理影片"
	case errors.Is(runErr, catalog.ErrConflict), errors.Is(runErr, catalog.ErrUnavailable), errors.Is(runErr, catalog.ErrInvalid):
		status, message = "needs_attention", "整理凭据、输出文件或媒体库挂载无法核对。请检查后重试登记；不会重新整理影片"
	case errors.Is(runErr, catalog.ErrBusy):
		status, message = "pending", "等待当前馆藏操作结束"
	case errors.Is(runErr, context.DeadlineExceeded):
		status, message = "timed_out", "馆藏登记超时；原馆藏和整理结果保留"
	case errors.Is(runErr, context.Canceled):
		status, message = "canceled", "馆藏登记已取消；已完成的整理和原馆藏保留"
		if s.runCtx != nil && s.runCtx.Err() != nil {
			status, message = "pending", "服务停止，保留馆藏登记待办"
		}
	}
	_, err = s.db.TransitionMediaJob(job.ID, job.Version, MediaJobTransition{Phase: "catalog", Status: status, Error: message})
	if errors.Is(err, errMediaJobConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	if errors.Is(runErr, catalog.ErrBusy) {
		return runErr
	}
	return nil
}
