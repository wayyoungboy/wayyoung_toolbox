package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

func (s *Service) handleIntake(w http.ResponseWriter, _ *http.Request) {
	config, err := s.db.IntakeConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	runs, err := s.db.ListMaintenanceRunsByKinds(20, "intake")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": config, "runs": runs})
}

func (s *Service) handleIntakeDirectories(w http.ResponseWriter, r *http.Request) {
	config, err := s.db.IntakeConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	targetPath := config.SourceDirectory
	if requested := strings.TrimSpace(r.URL.Query().Get("path")); requested != "" {
		targetPath = requested
	}
	target, err := canonicalDirectory(targetPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("目录路径无效或不可访问"))
		return
	}
	root := target
	if candidate := javspBrowseRoot(target); candidate != target {
		if volumeRoot, rootErr := canonicalDirectory(candidate); rootErr == nil && pathWithinRoot(volumeRoot, target) {
			root = volumeRoot
		}
	}
	s.writeDirectoryListing(w, root, target)
}

func (s *Service) handleIntakeConfig(w http.ResponseWriter, r *http.Request) {
	if !s.intakeLock.TryLock() {
		writeError(w, http.StatusConflict, errIntakeBusy)
		return
	}
	defer s.intakeLock.Unlock()
	var config IntakeConfig
	if err := decodeJSON(w, r, &config); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	updated, err := s.db.UpdateIntakeConfigIfRevision(r.Context(), config)
	if errors.Is(err, errIntakeConfigConflict) {
		writeError(w, http.StatusConflict, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Service) handleIntakePreview(w http.ResponseWriter, r *http.Request) {
	revision := decodeIntakeActionRevision(w, r)
	if revision == nil {
		return
	}
	if !s.intakeLock.TryLock() {
		writeError(w, http.StatusConflict, errIntakeBusy)
		return
	}
	defer s.intakeLock.Unlock()
	result, err := s.previewIntakeAtRevision(r.Context(), revision)
	if errors.Is(err, errIntakeConfigConflict) {
		writeError(w, http.StatusConflict, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) previewIntake(ctx context.Context) (IntakeRunResult, error) {
	return s.previewIntakeAtRevision(ctx, nil)
}

func (s *Service) previewIntakeAtRevision(ctx context.Context, revision *int64) (IntakeRunResult, error) {
	config, err := s.intakeActionConfig(ctx, revision)
	if err != nil {
		return IntakeRunResult{}, err
	}
	settings, err := s.db.Settings(true)
	if err != nil {
		return IntakeRunResult{}, err
	}
	session, err := s.collector.OpenQBSession(ctx, settings)
	if err != nil {
		return IntakeRunResult{}, err
	}
	defer session.Close()
	torrents, err := session.Torrents(ctx)
	if err != nil {
		return IntakeRunResult{}, err
	}
	candidates, issues, err := s.intakeCandidates(ctx, torrents, config)
	if err != nil {
		return IntakeRunResult{}, err
	}
	return IntakeRunResult{Scanned: len(torrents), Candidates: candidates, Errors: issues, Failed: len(issues)}, nil
}

func (s *Service) handleIntakeRun(w http.ResponseWriter, r *http.Request) {
	revision := decodeIntakeActionRevision(w, r)
	if revision == nil {
		return
	}
	result, err := s.runIntakeAtRevision(r.Context(), "manual", revision)
	if errors.Is(err, errIntakeBusy) || errors.Is(err, errIntakeConfigConflict) {
		writeError(w, http.StatusConflict, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) runIntake(ctx context.Context, trigger string) (IntakeRunResult, error) {
	return s.runIntakeAtRevision(ctx, trigger, nil)
}

func decodeIntakeActionRevision(w http.ResponseWriter, r *http.Request) *int64 {
	var request struct {
		Revision *int64 `json:"revision"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return nil
	}
	if request.Revision == nil || *request.Revision < 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("请携带已确认的下载入库配置 revision"))
		return nil
	}
	return request.Revision
}

func (s *Service) intakeActionConfig(ctx context.Context, revision *int64) (IntakeConfig, error) {
	values, err := readSettings(ctx, s.db.db)
	if err != nil {
		return IntakeConfig{}, err
	}
	config := intakeConfigFromSettings(values)
	if revision != nil && config.Revision != *revision {
		return IntakeConfig{}, errIntakeConfigConflict
	}
	// Retain this single database snapshot throughout the accepted action.
	// Later settings updates must not replace its confirmed source/target.
	return config, nil
}

func (s *Service) runIntakeAtRevision(ctx context.Context, trigger string, revision *int64) (IntakeRunResult, error) {
	if !s.intakeLock.TryLock() {
		return IntakeRunResult{}, errIntakeBusy
	}
	defer s.intakeLock.Unlock()
	jobCtx := s.runCtx
	if jobCtx == nil {
		jobCtx = context.WithoutCancel(ctx)
	}
	config, err := s.intakeActionConfig(ctx, revision)
	if err != nil {
		return IntakeRunResult{}, err
	}
	if err := s.db.MarkIntakeStarted(config); err != nil {
		return IntakeRunResult{}, err
	}
	runID, err := s.db.StartMaintenanceRun("intake", trigger)
	if err != nil {
		return IntakeRunResult{}, err
	}
	finishFailure := func(result IntakeRunResult, runErr error) (IntakeRunResult, error) {
		_ = s.db.FinishMaintenanceRun(runID, "failed", result.Scanned, len(result.Candidates), result.TasksDeleted, result.Errors, runErr.Error())
		return result, runErr
	}
	settings, err := s.db.Settings(true)
	if err != nil {
		return finishFailure(IntakeRunResult{}, err)
	}
	session, err := s.collector.OpenQBSession(jobCtx, settings)
	if err != nil {
		return finishFailure(IntakeRunResult{}, err)
	}
	defer session.Close()
	torrents, err := session.Torrents(jobCtx)
	if err != nil {
		return finishFailure(IntakeRunResult{}, err)
	}
	candidates, issues, err := s.intakeCandidates(jobCtx, torrents, config)
	if err != nil {
		return finishFailure(IntakeRunResult{Scanned: len(torrents)}, err)
	}
	result := IntakeRunResult{Scanned: len(torrents), Candidates: candidates, Errors: issues, Failed: len(issues)}
	for i, candidate := range candidates {
		job, err := s.enqueueIntakeCandidate(jobCtx, candidate, config)
		if err != nil {
			result.Failed++
			result.Errors = append(result.Errors, candidate.Name+"：登记队列失败："+err.Error())
			continue
		}
		result.Candidates[i].JobID = job.ID
		switch {
		case job.Status == "pending":
			result.Queued++
		case retryableMediaStatus(job.Status):
			result.Failed++
			reason := job.Error
			if reason == "" {
				reason = "队列任务尚未处理，请在任务队列中重试或取消"
			}
			result.Errors = append(result.Errors, fmt.Sprintf("%s：队列任务 #%d 仍需处理：%s", candidate.Name, job.ID, reason))
		case job.Status == "success":
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("%s：队列任务 #%d 已完成但 qB 任务仍存在，请检查是否重新添加", candidate.Name, job.ID))
		}
	}
	s.wakeMediaQueue()
	if err := s.drainIntakeQueue(jobCtx, session, torrents, &result); err != nil {
		return finishFailure(result, err)
	}
	status := "success"
	message := ""
	if result.Failed > 0 {
		status = "partial"
		message = fmt.Sprintf("%d 个任务检查或处理未完成", result.Failed)
	}
	if err := s.db.FinishMaintenanceRun(runID, status, result.Scanned, len(result.Candidates), result.TasksDeleted, result.Errors, message); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Service) handleDuplicateDelete(w http.ResponseWriter, r *http.Request) {
	if !s.acquireMediaFileWriter() {
		writeError(w, http.StatusConflict, errMaintenanceBusy)
		return
	}
	defer s.mediaFiles.Unlock()
	if !s.maintenanceLock.TryLock() {
		writeError(w, http.StatusConflict, errMaintenanceBusy)
		return
	}
	defer s.maintenanceLock.Unlock()
	var payload struct {
		Files []DuplicateDeleteSelection `json:"files"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	config, err := s.db.MaintenanceConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	runID, err := s.db.StartMaintenanceRun("duplicate_delete", "manual")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	result, deleteErr := deleteDuplicateVideos(config.RootDirectory, payload.Files)
	status, message := "success", ""
	if deleteErr != nil {
		status, message = "failed", deleteErr.Error()
	}
	if finishErr := s.db.FinishMaintenanceRun(runID, status, result.Selected, result.Selected, result.Deleted, result.Files, message); finishErr != nil {
		writeError(w, http.StatusInternalServerError, finishErr)
		return
	}
	if deleteErr != nil {
		writeError(w, http.StatusBadRequest, deleteErr)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
