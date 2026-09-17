package service

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

func (s *Service) handleOrphanPreview(w http.ResponseWriter, _ *http.Request) {
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
	plan, err := planOrphanFolders(config.RootDirectory, time.Duration(config.MinimumAgeHours)*time.Hour)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, plan.OrphanCleanupResult)
}

func (s *Service) handleOrphanCleanup(w http.ResponseWriter, _ *http.Request) {
	result, err := s.runOrphanCleanup("manual")
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

func (s *Service) runOrphanCleanup(trigger string) (OrphanCleanupResult, error) {
	if !s.acquireMediaFileWriter() {
		return OrphanCleanupResult{}, errMaintenanceBusy
	}
	defer s.mediaFiles.Unlock()
	if !s.maintenanceLock.TryLock() {
		return OrphanCleanupResult{}, errMaintenanceBusy
	}
	defer s.maintenanceLock.Unlock()
	config, err := s.db.MaintenanceConfig()
	if err != nil {
		return OrphanCleanupResult{}, err
	}
	return s.orphanCleanupLocked(config, trigger)
}

func (s *Service) orphanCleanupLocked(config MaintenanceConfig, trigger string) (OrphanCleanupResult, error) {
	id, err := s.db.StartMaintenanceRun("orphan_cleanup", trigger)
	if err != nil {
		return OrphanCleanupResult{}, err
	}
	plan, cleanupErr := planOrphanFolders(config.RootDirectory, time.Duration(config.MinimumAgeHours)*time.Hour)
	result := plan.OrphanCleanupResult
	if cleanupErr == nil {
		result, cleanupErr = executeOrphanFolders(plan)
	}
	status, message := "success", ""
	if cleanupErr != nil {
		status, message = "failed", cleanupErr.Error()
	}
	details := []string{}
	for _, path := range result.Directories {
		if len(details) >= 1000 {
			break
		}
		details = append(details, "已永久清理无视频目录："+path)
	}
	for _, folder := range result.Candidates {
		if len(details) >= 1000 {
			break
		}
		if folder.SkipReason != "" {
			details = append(details, "跳过 "+folder.Path+"："+folder.SkipReason)
		}
	}
	details = append(details, fmt.Sprintf("清理 %d 个影片目录、%d 个附件，逻辑大小 %d bytes", result.Removed, result.DeletedFiles, result.ReclaimedBytes))
	finishErr := s.db.FinishMaintenanceRun(id, status, result.ScannedDirs, result.Found, result.Removed, details, message)
	return result, errors.Join(cleanupErr, finishErr)
}
