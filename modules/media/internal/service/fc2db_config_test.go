package service

import (
	"slices"
	"testing"
)

func TestFC2DBConfiguration(t *testing.T) {
	cfg := defaultJavspConfig()
	if !slices.Contains(cfg.FC2Crawlers, "fc2db") {
		t.Error("new configurations should include fc2db")
	}
	cfg.InputDirectory = t.TempDir()
	cfg.OutputFolderPattern = "organized/{num}"
	cfg.FC2Crawlers = []string{"javmenu", "fc2db"}
	got, err := validateJavspConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.FC2Crawlers, cfg.FC2Crawlers) {
		t.Fatal("custom source order changed")
	}
}
