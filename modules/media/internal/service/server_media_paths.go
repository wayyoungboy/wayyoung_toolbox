package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

type mediaPathDraft struct {
	Config MediaPathSettings `json:"config"`
	Sample javspNamingSample `json:"sample"`
}

type mediaPathPreview struct {
	Config  MediaPathSettings `json:"config"`
	Preview javspNamingResult `json:"preview"`
	Digest  string            `json:"preview_digest"`
}

func (s *Service) handleMediaPathSettings(w http.ResponseWriter, r *http.Request) {
	config, err := s.db.MediaPathSettings(r.Context())
	if err != nil {
		writeMediaPathError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": config})
}

func (s *Service) previewMediaPathSettings(ctx context.Context, request mediaPathDraft) (mediaPathPreview, error) {
	stored, err := readSettings(ctx, s.db.db)
	if err != nil {
		return mediaPathPreview{}, err
	}
	intake, javsp, err := applyMediaPathSettings(request.Config, intakeConfigFromSettings(stored), javspConfigFromSettings(stored, false))
	if err != nil {
		return mediaPathPreview{}, err
	}
	names, err := s.previewJavspNaming(ctx, javsp, request.Sample, intake)
	if err != nil {
		return mediaPathPreview{}, fmt.Errorf("%w：%v", errMediaPathSettingsInvalid, err)
	}
	result := mediaPathPreview{Config: mediaPathSettingsFromConfig(intake, javsp), Preview: names}
	// A consistency receipt, not an authentication token. Include revisions,
	// normalized paths, sample and computed output so PUT must match the preview.
	// Authorization is independently enforced by the fnOS admin middleware.
	encoded, err := json.Marshal(struct {
		Config  MediaPathSettings `json:"config"`
		Sample  javspNamingSample `json:"sample"`
		Preview javspNamingResult `json:"preview"`
	}{result.Config, request.Sample, names})
	if err != nil {
		return mediaPathPreview{}, err
	}
	digest := sha256.Sum256(encoded)
	result.Digest = hex.EncodeToString(digest[:])
	return result, nil
}

func (s *Service) handleMediaPathPreview(w http.ResponseWriter, r *http.Request) {
	var request mediaPathDraft
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.previewMediaPathSettings(r.Context(), request)
	if err != nil {
		writeMediaPathError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) handleUpdateMediaPathSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		mediaPathDraft
		Digest string `json:"preview_digest"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(request.Digest) != sha256.Size*2 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("请先预览最终路径，再确认保存"))
		return
	}
	preview, err := s.previewMediaPathSettings(r.Context(), request.mediaPathDraft)
	if err != nil {
		writeMediaPathError(w, err)
		return
	}
	if preview.Digest != request.Digest {
		writeError(w, http.StatusBadRequest, fmt.Errorf("配置、示例或预览路径已变化，请重新预览后再保存"))
		return
	}
	// Recheck both revisions in the same transaction as the writes. A module
	// save racing with the preview calculation must not be overwritten.
	config, err := s.db.UpdateMediaPathSettings(r.Context(), preview.Config)
	if err != nil {
		writeMediaPathError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": config})
}

func writeMediaPathError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, errMediaPathSettingsConflict):
		status = http.StatusConflict
	case errors.Is(err, errMediaPathSettingsInvalid), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = http.StatusBadRequest
	}
	writeError(w, status, err)
}
