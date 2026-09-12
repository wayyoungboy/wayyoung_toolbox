package service

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	javspengine "github.com/yuukiy/javsp-go/engine"
)

type javspNamingSample struct {
	MediaID     string   `json:"media_id"`
	Title       string   `json:"title"`
	Actress     []string `json:"actress"`
	PublishDate string   `json:"publish_date"`
	Filenames   []string `json:"filenames"`
}

type javspNamingResult struct {
	javspengine.NamingPreview
	SameNameNFO []string `json:"same_name_nfo"`
	Warnings    []string `json:"warnings"`
}

func (s *Service) handleJavspNamingPreview(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Config JavspConfig       `json:"config"`
		Sample javspNamingSample `json:"sample"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, 400, err)
		return
	}
	intake, err := s.db.IntakeConfig()
	if err != nil {
		writeError(w, 500, err)
		return
	}
	result, err := s.previewJavspNaming(r.Context(), payload.Config, payload.Sample, intake)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, result)
}

// Shared by the advanced JAVSP page and central path settings. This computes
// hypothetical names only: no scanner, translator, queue or filesystem writes.
func (s *Service) previewJavspNaming(ctx context.Context, previewConfig JavspConfig, sample javspNamingSample, intake IntakeConfig) (javspNamingResult, error) {
	// The sample already supplies final metadata. Translation is neither run
	// nor validated here, and the renderer must not use stored credentials.
	previewConfig.TranslatorEngine, previewConfig.TranslatorAPIKey = "none", ""
	previewConfig.TranslatorAPIKeySet = false
	previewConfig.TranslatorURL, previewConfig.TranslatorModel, previewConfig.TranslatorAppID = "", "", ""
	previewConfig.TranslateTitle, previewConfig.TranslatePlot = false, false
	cfg, err := validateJavspConfig(previewConfig)
	if err != nil {
		return javspNamingResult{}, err
	}
	if !validJavspBasename(sample.MediaID, 128) || len(sample.Title) > 1024 || strings.ContainsRune(sample.Title, 0) || len(sample.Actress) > 100 || len(sample.PublishDate) > 32 || len(sample.Filenames) == 0 || len(sample.Filenames) > 16 {
		return javspNamingResult{}, fmt.Errorf("预览需要有效的示例番号和 1 到 16 个文件名")
	}
	for _, name := range sample.Actress {
		if len(name) > 256 || strings.ContainsRune(name, 0) {
			return javspNamingResult{}, fmt.Errorf("示例演员字段过长或无效")
		}
	}
	sources := []string{}
	seen := map[string]bool{}
	for _, name := range sample.Filenames {
		allowed := false
		for _, extension := range cfg.FilenameExtensions {
			if strings.EqualFold(filepath.Ext(name), extension) {
				allowed = true
				break
			}
		}
		if !validJavspBasename(name, 512) || strings.ContainsAny(name, "/\\") || !allowed || seen[name] {
			return javspNamingResult{}, fmt.Errorf("示例文件必须是不重复的视频文件名，不能包含目录")
		}
		seen[name] = true
		sources = append(sources, filepath.Join(cfg.InputDirectory, name))
	}
	raw, err := s.javsp.prepareConfigDataContext(ctx, cfg, false)
	if err != nil {
		return javspNamingResult{}, err
	}
	defer clear(raw)
	result, err := javspengine.PreviewNames(ctx, javspengine.Options{ConfigData: raw, ResourceRoot: s.javsp.workRoot, InputDirectory: cfg.InputDirectory}, javspengine.NamingSample{MediaID: sample.MediaID, Title: sample.Title, Actress: sample.Actress, PublishDate: sample.PublishDate, SourceFiles: sources})
	if err != nil {
		return javspNamingResult{}, err
	}
	outputRoot := cfg.InputDirectory
	if cfg.MoveFiles {
		var available bool
		outputRoot, available, err = plannedJavspOutputRoot(cfg)
		if err != nil || !available {
			return javspNamingResult{}, fmt.Errorf("请先设置明确的整理完成目录")
		}
	}
	// Validate hypothetical destinations through the existing root, without
	// requiring sample videos or output subdirectories to exist.
	accessRoot := cfg.InputDirectory
	if cfg.MoveFiles && cfg.OutputDirectory != "" {
		accessRoot = cfg.OutputDirectory
	}
	root, err := os.OpenRoot(accessRoot)
	if err != nil {
		return javspNamingResult{}, err
	}
	defer root.Close()
	sameNameNFO := []string{}
	paths := moviePlanArtifacts(result.Plan)
	if result.Plan.ExtraFanartDirectory != "" {
		paths = append(paths, result.Plan.ExtraFanartDirectory)
	}
	for _, file := range result.Plan.Files {
		sameNameNFO = append(sameNameNFO, strings.TrimSuffix(file.Destination, filepath.Ext(file.Destination))+".nfo")
		paths = append(paths, file.Destination)
	}
	paths = append(paths, sameNameNFO...)
	for _, path := range paths {
		if _, err := moviePlanRelative(outputRoot, path); err != nil {
			return javspNamingResult{}, err
		}
		rel, err := moviePlanRelative(accessRoot, path)
		if err != nil {
			return javspNamingResult{}, err
		}
		if err := rejectIntakeLinks(root, rel, true); err != nil {
			return javspNamingResult{}, err
		}
	}
	warnings := []string{}
	if intake.RunJavsp && !pathWithinRoot(cfg.InputDirectory, intake.DestinationDirectory) {
		warnings = append(warnings, "下载入库的未处理目录不在当前扫描目录内，搬运后可能无法发现影片；请统一两处路径。")
	}
	if intake.RunJavsp {
		exclusions, err := javspDiscoveryExclusions(cfg)
		if err != nil {
			return javspNamingResult{}, err
		}
		for _, excluded := range exclusions {
			if pathWithinRoot(excluded, intake.DestinationDirectory) {
				warnings = append(warnings, "下载入库的未处理目录位于整理完成目录内，会被文件发现排除，搬运后无法自动整理；请分离未处理与完成目录。")
				break
			}
		}
	}
	if cfg.MoveFiles && intake.RunJavsp && (pathWithinRoot(outputRoot, intake.SourceDirectory) || pathWithinRoot(intake.SourceDirectory, outputRoot)) {
		warnings = append(warnings, "整理完成目录与下载来源目录重叠，请分离下载与完成目录。")
	}
	return javspNamingResult{result, sameNameNFO, warnings}, nil
}
