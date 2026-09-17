package service

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

func (s *Service) handleAutomaticDuplicatePreview(w http.ResponseWriter, _ *http.Request) {
	if !s.maintenanceLock.TryLock() {
		writeError(w, http.StatusConflict, errMaintenanceBusy)
		return
	}
	defer s.maintenanceLock.Unlock()
	config, err := s.db.MaintenanceConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	plan, err := planAutomaticDuplicates(config.RootDirectory, time.Duration(config.MinimumAgeHours)*time.Hour)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, plan.AutomaticDuplicateResult)
}

func (s *Service) handleAutomaticDuplicateCleanup(w http.ResponseWriter, _ *http.Request) {
	result, err := s.runAutomaticDuplicateCleanup("manual")
	if errors.Is(err, errMaintenanceBusy) {
		writeError(w, http.StatusConflict, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) runAutomaticDuplicateCleanup(trigger string) (AutomaticDuplicateResult, error) {
	if !s.acquireMediaFileWriter() {
		return AutomaticDuplicateResult{}, errMaintenanceBusy
	}
	defer s.mediaFiles.Unlock()
	if !s.maintenanceLock.TryLock() {
		return AutomaticDuplicateResult{}, errMaintenanceBusy
	}
	defer s.maintenanceLock.Unlock()
	config, err := s.db.MaintenanceConfig()
	if err != nil {
		return AutomaticDuplicateResult{}, err
	}
	return s.automaticDuplicateCleanupLocked(config, trigger)
}

func (s *Service) automaticDuplicateCleanupLocked(config MaintenanceConfig, trigger string) (AutomaticDuplicateResult, error) {
	id, err := s.db.StartMaintenanceRun("duplicate_cleanup", trigger)
	if err != nil {
		return AutomaticDuplicateResult{}, err
	}
	plan, cleanupErr := planAutomaticDuplicates(config.RootDirectory, time.Duration(config.MinimumAgeHours)*time.Hour)
	result := plan.AutomaticDuplicateResult
	if cleanupErr == nil {
		result, cleanupErr = executeAutomaticDuplicates(plan)
	}
	status, message := "success", ""
	if cleanupErr != nil {
		status, message = "failed", cleanupErr.Error()
	}
	details := make([]string, 0)
	for _, group := range result.Groups {
		if len(details) >= 1000 {
			break
		}
		if group.SkipReason != "" {
			details = append(details, fmt.Sprintf("跳过 %s：%s", group.MediaID, group.SkipReason))
		} else {
			details = append(details, fmt.Sprintf("保留 %s (%d bytes)", group.Keep.Path, group.Keep.Size))
		}
	}
	for _, path := range result.Files {
		if len(details) >= 1000 {
			break
		}
		details = append(details, "已永久删除 "+path)
	}
	details = append(details, fmt.Sprintf("删除 %d 个，释放逻辑大小 %d bytes", result.Deleted, result.ReclaimedBytes))
	finishErr := s.db.FinishMaintenanceRun(id, status, result.ScannedVideos, len(result.Groups), result.Deleted, details, message)
	return result, errors.Join(cleanupErr, finishErr)
}

// Maintenance actions share one schedule, but each has its own opt-in switch.
func (s *Service) runScheduledMaintenance() error {
	if !s.acquireMediaFileWriter() {
		return errMaintenanceBusy
	}
	defer s.mediaFiles.Unlock()
	if !s.maintenanceLock.TryLock() {
		return errMaintenanceBusy
	}
	defer s.maintenanceLock.Unlock()
	config, err := s.db.MaintenanceConfig()
	if err != nil {
		return err
	}
	if !config.CleanupEnabled && !config.DuplicateCleanupEnabled && !config.OrphanCleanupEnabled {
		return nil
	}
	if err := s.db.MarkMaintenanceStarted(config); err != nil {
		return err
	}
	var duplicateErr, orphanErr, emptyErr error
	if config.DuplicateCleanupEnabled {
		_, duplicateErr = s.automaticDuplicateCleanupLocked(config, "schedule")
	}
	if config.OrphanCleanupEnabled {
		_, orphanErr = s.orphanCleanupLocked(config, "schedule")
	}
	if config.CleanupEnabled {
		_, emptyErr = s.emptyDirectoryCleanupLocked(config, "schedule")
	}
	return errors.Join(duplicateErr, orphanErr, emptyErr)
}
