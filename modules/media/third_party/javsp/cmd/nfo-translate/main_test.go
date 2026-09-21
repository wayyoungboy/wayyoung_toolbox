package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuukiy/javsp-go/internal/translate"
)

const fixture = `<?xml version="1.0"?><movie><title>TEST-001 今日は晴れです</title><originaltitle>原題</originaltitle><plot>明日も晴れるでしょう</plot><actor><name>小林あおい</name></actor><!-- keep --><custom key="x">untouched &amp; safe</custom></movie>`

type testEngine struct{}

func (testEngine) Translate(_ context.Context, text string, _ []string) (translate.Result, error) {
	if strings.HasPrefix(text, "TEST-001") {
		return translate.Result{Text: "TEST-001 今天晴朗 & 温暖"}, nil
	}
	return translate.Result{Text: "明天也会晴朗"}, nil
}

func TestRepairFileDryRunAndExistingBackup(t *testing.T) {
	dir, backupDir := t.TempDir(), t.TempDir()
	root, _ := os.OpenRoot(dir)
	defer root.Close()
	backup, _ := os.OpenRoot(backupDir)
	defer backup.Close()
	if err := os.WriteFile(filepath.Join(dir, "test.nfo"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := repairFile(context.Background(), root, nil, "test.nfo", nil, false)
	if err != nil || r.Fields != 2 || r.Before != r.After {
		t.Fatalf("dry run: %+v %v", r, err)
	}
	if _, err := repairFile(context.Background(), root, nil, "test.nfo", testEngine{}, true); err == nil {
		t.Fatal("allowed missing backup")
	}
	if err := os.WriteFile(filepath.Join(backupDir, digest([]byte("test.nfo"))+".nfo"), []byte("old backup"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := repairFile(context.Background(), root, backup, "test.nfo", testEngine{}, true); err == nil {
		t.Fatal("overwrote backup")
	}
	if _, err := repairFile(context.Background(), root, backup, "missing.nfo", testEngine{}, true); err == nil {
		t.Fatal("accepted missing NFO")
	}
	if _, err := repairFile(context.Background(), root, backup, "test.nfo", translate.NoopEngine{}, true); err == nil {
		t.Fatal("accepted unchanged translation")
	}
}

func TestCLIReadOnlyAndValidatedApplyWithoutNetwork(t *testing.T) {
	for _, tc := range []struct {
		name, credentials string
		apply, wantError  bool
	}{
		{"dry", "", false, false},
		{"apply_already_chinese", `{"api_key":"fixture:fx"}`, true, false},
		{"missing_key", `{}`, true, true},
		{"malformed_credentials", `{`, true, true},
		{"invalid_proxy", `{"api_key":"fixture:fx","proxy":"ftp://example.invalid"}`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, parent := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "test.nfo"), []byte(`<movie><title>中文标题</title></movie>`), 0600); err != nil {
				t.Fatal(err)
			}
			credentials := filepath.Join(parent, "stdin.json")
			if err := os.WriteFile(credentials, []byte(tc.credentials), 0600); err != nil {
				t.Fatal(err)
			}
			stdin, err := os.Open(credentials)
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			oldFlags, oldArgs, oldStdin := flag.CommandLine, os.Args, os.Stdin
			defer func() { flag.CommandLine, os.Args, os.Stdin = oldFlags, oldArgs, oldStdin }()
			flag.CommandLine = flag.NewFlagSet("nfo-translate", flag.ContinueOnError)
			os.Args = []string{"nfo-translate", "-root", dir}
			if tc.apply {
				os.Args = append(os.Args, "-apply", "-backup", filepath.Join(parent, "backup"))
			}
			os.Stdin = stdin
			err = run()
			if (err != nil) != tc.wantError {
				t.Fatalf("run error = %v", err)
			}
		})
	}
}

func TestRepairNFOOnlyReplacesRequestedText(t *testing.T) {
	updated, fields, err := translatedNFO(context.Background(), []byte(fixture), testEngine{})
	if err != nil || fields != 2 {
		t.Fatalf("translation: %d %v", fields, err)
	}
	want := strings.Replace(fixture, "TEST-001 今日は晴れです", "TEST-001 今天晴朗 &amp; 温暖", 1)
	want = strings.Replace(want, "明日も晴れるでしょう", "明天也会晴朗", 1)
	if string(updated) != want {
		t.Fatalf("unrelated XML changed: %s", updated)
	}
	_, fields, err = translatedNFO(context.Background(), updated, testEngine{})
	if err != nil || fields != 0 {
		t.Fatalf("repair not idempotent: %d %v", fields, err)
	}
}

func TestRepairNFORejectsInvalidAndPreservesActorNames(t *testing.T) {
	for _, input := range []string{`<movie>`, `<show><title>日文です</title></show>`, `<movie><title>日文です</title><title>duplicate</title></movie>`} {
		if _, _, err := translatedNFO(context.Background(), []byte(input), testEngine{}); err == nil {
			t.Fatalf("accepted invalid document: %s", input)
		}
	}
	input := []byte(`<movie><title>晴朗的一天 小林あおい</title><actor><name>小林あおい</name></actor></movie>`)
	got, fields, err := translatedNFO(context.Background(), input, nil)
	if err != nil || fields != 0 || !bytes.Equal(input, got) {
		t.Fatalf("actor name selected for repair: %d %v", fields, err)
	}
	if _, _, err := translatedNFO(context.Background(), []byte(fixture), translate.NoopEngine{}); err == nil {
		t.Fatal("accepted untranslated result")
	}
}

func TestRepairFileBacksUpAndDoesNotTouchVideoOrSymlink(t *testing.T) {
	dir, backupDir := t.TempDir(), t.TempDir()
	path := filepath.Join(dir, "TEST-001.nfo")
	if err := os.WriteFile(path, []byte(fixture), 0640); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(dir, "TEST-001.mp4")
	if err := os.WriteFile(video, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	root, _ := os.OpenRoot(dir)
	defer root.Close()
	backup, _ := os.OpenRoot(backupDir)
	defer backup.Close()
	record, err := repairFile(context.Background(), root, backup, "TEST-001.nfo", testEngine{}, true)
	if err != nil || record.Fields != 2 || record.Before == record.After {
		t.Fatalf("repair failed: %+v %v", record, err)
	}
	saved, err := backup.ReadFile(record.Backup)
	if err != nil || string(saved) != fixture {
		t.Fatal("original backup lost")
	}
	data, _ := os.ReadFile(video)
	if string(data) != "video" {
		t.Fatal("video changed")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0640 {
		t.Fatal("NFO permissions changed")
	}
	if err := os.Symlink(path, filepath.Join(dir, "link.nfo")); err != nil {
		t.Fatal(err)
	}
	if _, err := repairFile(context.Background(), root, backup, "link.nfo", testEngine{}, true); err == nil {
		t.Fatal("followed NFO symlink")
	}
}
