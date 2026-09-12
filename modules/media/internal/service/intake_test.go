package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildIntakeCandidatesSelectsOnlyCompletedTorrentsUnderSource(t *testing.T) {
	volume := t.TempDir()
	source := filepath.Join(volume, "download", "av")
	destination := filepath.Join(volume, "media", "av", "未处理")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	completed := filepath.Join(source, "MIDA-734.mp4")
	if err := os.WriteFile(completed, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	torrents := []QBTorrent{
		{Hash: "done", Name: "done", ContentPath: completed, Progress: 1, AmountLeft: 0, State: "uploading"},
		{Hash: "partial", Name: "partial", ContentPath: filepath.Join(source, "partial.mp4"), Progress: .99, AmountLeft: 1, State: "downloading"},
		{Hash: "error", Name: "error", ContentPath: filepath.Join(source, "error.mp4"), Progress: 1, AmountLeft: 0, State: "error"},
		{Hash: "outside", Name: "outside", ContentPath: filepath.Join(volume, "other.mp4"), Progress: 1, AmountLeft: 0, State: "stoppedUP"},
	}

	candidates, err := buildIntakeCandidates(torrents, source, destination)
	if err != nil {
		t.Fatal(err)
	}
	canonicalDestination, err := canonicalDirectory(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Hash != "done" || candidates[0].Destination != filepath.Join(canonicalDestination, "MIDA-734.mp4") {
		t.Fatalf("candidates=%+v", candidates)
	}
}

func TestMoveIntakeCandidateMovesContentAndCleansExistingDuplicate(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}

	firstSource := filepath.Join(source, "first.mp4")
	firstDestination := filepath.Join(destination, "first.mp4")
	if err := os.WriteFile(firstSource, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := moveIntakeCandidate(IntakeCandidate{Source: firstSource, Destination: firstDestination}, source, destination)
	if err != nil || result != "moved" {
		t.Fatalf("result=%q err=%v", result, err)
	}
	if _, err := os.Stat(firstSource); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	if content, err := os.ReadFile(firstDestination); err != nil || string(content) != "first" {
		t.Fatalf("destination content=%q err=%v", content, err)
	}

	duplicateSource := filepath.Join(source, "duplicate.mp4")
	duplicateDestination := filepath.Join(destination, "duplicate.mp4")
	for _, path := range []string{duplicateSource, duplicateDestination} {
		if err := os.WriteFile(path, []byte("same"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result, err = moveIntakeCandidate(IntakeCandidate{Source: duplicateSource, Destination: duplicateDestination}, source, destination)
	if err != nil || result != "duplicate_removed" {
		t.Fatalf("result=%q err=%v", result, err)
	}
	if _, err := os.Stat(duplicateSource); !os.IsNotExist(err) {
		t.Fatalf("duplicate source still exists: %v", err)
	}
	if _, err := os.Stat(duplicateDestination); err != nil {
		t.Fatalf("duplicate destination changed: %v", err)
	}
}

func TestValidateIntakeConfigRejectsUnsafeDirectoryRelationships(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	for _, path := range []string{source, destination} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	valid := defaultIntakeConfig()
	valid.SourceDirectory = source
	valid.DestinationDirectory = destination
	valid.ScheduleMinutes = 15
	if _, err := validateIntakeConfig(valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	nested := filepath.Join(source, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	valid.DestinationDirectory = nested
	if _, err := validateIntakeConfig(valid); err == nil {
		t.Fatal("destination nested below source was accepted")
	}
}
