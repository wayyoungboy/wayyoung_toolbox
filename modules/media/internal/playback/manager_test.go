package playback

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func fakeTools(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	probe := filepath.Join(dir, "ffprobe")
	ffmpeg := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(probe, []byte("#!/bin/sh\nprintf '%s' '{\"format\":{\"duration\":\"300\",\"format_name\":\"matroska,webm\"},\"streams\":[{\"index\":0,\"codec_type\":\"video\",\"codec_name\":\"h264\",\"width\":320,\"height\":180},{\"index\":1,\"codec_type\":\"audio\",\"codec_name\":\"aac\"}],\"frames\":[{\"best_effort_timestamp_time\":\"4.0\"}]}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
for arg; do output="$arg"; done
case "$*" in *-encoders*) printf 'libx264'; exit 0;; esac
case "$*" in *h264_vaapi*) exit 3;; esac
dir=${output%/*}
printf 'segment' > "$dir/seg000000.ts"
printf '#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:2.0,\nseg000000.ts\n' > "$output"
exec sleep 300
`
	if err := os.WriteFile(ffmpeg, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return ffmpeg, probe
}

func TestHardwareFailureFallsBackAndCacheLimitStops(t *testing.T) {
	manager, _ := testManager(t, nil)
	manager.options.DisableHardware = false
	manager.caps = &Capabilities{Available: true, Encoders: []Encoder{{ID: "h264_vaapi", Device: "/dev/dri/renderD128", Verified: true}}}
	session, err := manager.Start(context.Background(), Request{AssetID: "asset", Mode: "hardware"})
	if err != nil {
		t.Fatal(err)
	}
	if session.Backend != "libx264" || !session.Fallback {
		t.Fatalf("hardware failure mislabeled: %+v", session)
	}
	if _, err := manager.Pulse(context.Background(), session.ID, true); err != nil {
		t.Fatal(err)
	}
	if view, err := manager.Pulse(context.Background(), session.ID, false); err != nil || view.Paused {
		t.Fatalf("resume: %+v %v", view, err)
	}
	if err := manager.Stop(context.Background(), session.ID); err != nil {
		t.Fatal(err)
	}
	limited, _ := testManager(t, func(options *Options) { options.MaxBytes = 32 })
	_, err = limited.Start(context.Background(), Request{AssetID: "asset", Mode: "software"})
	if err != nil && !errors.Is(err, ErrCacheLimit) {
		t.Fatalf("cache cap: %v", err)
	}
	timeout := time.After(2 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for limited.Snapshot().Active != 0 {
		select {
		case <-timeout:
			t.Fatal("oversized cache did not stop")
		case <-tick.C:
		}
	}
}

func TestStopDoesNotDeleteReplacementCacheDirectory(t *testing.T) {
	manager, _ := testManager(t, nil)
	session, err := manager.Start(context.Background(), Request{AssetID: "asset", Mode: "software"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(manager.options.Dir, "session-"+session.ID)
	if err := os.Rename(dir, dir+"-retained"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "not-ours.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(context.Background(), session.ID); !errors.Is(err, ErrChanged) {
		t.Fatalf("cache replacement not rejected: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("replacement directory deleted")
	}
}

func testManager(t *testing.T, edit func(*Options)) (*Manager, string) {
	t.Helper()
	ffmpeg, probe := fakeTools(t)
	source := filepath.Join(t.TempDir(), "TEST-001.mkv")
	if err := os.WriteFile(source, []byte("registered media"), 0600); err != nil {
		t.Fatal(err)
	}
	options := Options{Dir: filepath.Join(t.TempDir(), "cache"), FFmpeg: ffmpeg, FFprobe: probe, DisableHardware: true, MaxSessions: 1, PollInterval: 10 * time.Millisecond, IdleTimeout: time.Minute,
		Open: func(_ context.Context, id string) (Source, error) {
			if id != "asset" {
				return Source{}, os.ErrNotExist
			}
			f, err := os.Open(source)
			return Source{File: f, Name: "TEST-001.mkv"}, err
		}}
	if runtime.GOOS == "darwin" {
		options.commandForTest = testCacheCommand
	}
	if edit != nil {
		edit(&options)
	}
	manager, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := manager.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return manager, source
}

// macOS cannot traverse a directory via /dev/fd/N. The isolated test helper
// performs fchdir(4)+exec, exercising the same pinned-directory guarantee as
// the Linux production /proc/self/fd/4 path without a shell or path fallback.
func testCacheCommand(ctx context.Context, tool string, args ...string) *exec.Cmd {
	executable, err := os.Executable()
	if err != nil {
		panic(err)
	}
	argv := append([]string{"-test.run=^TestPlaybackCommandHelper$", "--", tool}, args...)
	command := exec.CommandContext(ctx, executable, argv...)
	command.Env = append(os.Environ(), "WAYYOUNG_PLAYBACK_TEST_EXEC=1")
	return command
}
func TestPlaybackCommandHelper(t *testing.T) {
	if os.Getenv("WAYYOUNG_PLAYBACK_TEST_EXEC") != "1" {
		return
	}
	at := 0
	for index, arg := range os.Args {
		if arg == "--" {
			at = index + 1
			break
		}
	}
	if at == 0 || at >= len(os.Args) {
		os.Exit(2)
	}
	if err := syscall.Fchdir(4); err != nil {
		os.Exit(3)
	}
	argv := append([]string{}, os.Args[at:]...)
	for i := range argv {
		argv[i] = strings.ReplaceAll(argv[i], cacheDescriptorPath()+"/", "./")
	}
	if err := syscall.Exec(argv[0], argv, os.Environ()); err != nil {
		os.Exit(4)
	}
}

func TestProbeAndSessionCancellationReapProcessAndCache(t *testing.T) {
	manager, source := testManager(t, nil)
	ctx := context.Background()
	info, err := manager.Probe(ctx, "asset")
	if err != nil || info.Duration != 300 || info.VideoCodec != "h264" || info.AudioCodec != "aac" {
		t.Fatalf("probe: %+v %v", info, err)
	}
	session, err := manager.Start(ctx, Request{AssetID: "asset", Mode: "software", Start: 4.7})
	if err != nil {
		t.Fatal(err)
	}
	if session.Backend != "libx264" || session.Offset != 4.7 || session.Duration != 300 {
		t.Fatalf("session: %+v", session)
	}
	playlist, err := manager.Read(ctx, session.ID, "index.m3u8")
	if err != nil || !strings.HasPrefix(string(playlist), "#EXTM3U") {
		t.Fatalf("playlist: %s %v", playlist, err)
	}
	if _, err := manager.Read(ctx, session.ID, "../TEST-001.mkv"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("path accepted: %v", err)
	}
	if _, err := manager.Read(ctx, session.ID, "seg000000.ts.tmp"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("partial segment accepted: %v", err)
	}
	if _, err := manager.Start(ctx, Request{AssetID: "asset", Mode: "software"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrency cap: %v", err)
	}
	if err := manager.Stop(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	status := manager.Snapshot()
	if status.Active != 0 || status.CacheBytes != 0 {
		t.Fatalf("resources leaked: %+v", status)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("source file touched")
	}
	if _, err := manager.Read(ctx, session.ID, "index.m3u8"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stopped session readable: %v", err)
	}
	next, err := manager.Start(ctx, Request{AssetID: "asset", Mode: "auto", Start: 4.7})
	if err != nil {
		t.Fatal(err)
	}
	if next.Mode != "remux" || next.Offset != 4 || math.Abs(next.Seek-0.7) > 1e-6 {
		t.Fatalf("keyframe offset: %+v", next)
	}
}

func TestSessionRejectsChangedMediaAndInvalidRequests(t *testing.T) {
	manager, source := testManager(t, nil)
	ctx := context.Background()
	for _, request := range []Request{{AssetID: "asset", Mode: "auto|software"}, {AssetID: "asset", Mode: "invalid"}, {AssetID: "asset", Mode: "auto", Start: -1}, {AssetID: "asset", Mode: "auto", Start: 301}, {AssetID: "missing", Mode: "auto"}} {
		if _, err := manager.Start(ctx, request); err == nil {
			t.Fatalf("accepted invalid request: %+v", request)
		}
	}
	session, err := manager.Start(ctx, Request{AssetID: "asset", Mode: "software"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("different media"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Read(ctx, session.ID, "index.m3u8"); !errors.Is(err, ErrChanged) {
		t.Fatalf("changed source served: %v", err)
	}
}

func TestFallbackCannotPublishPreviousAttempt(t *testing.T) {
	manager, _ := testManager(t, func(options *Options) { options.PollInterval = time.Hour })
	tool := filepath.Join(t.TempDir(), "early-failure")
	script := `#!/bin/sh
for arg; do output="$arg"; done
dir=${output%/*}
kind=SOFTWARE
case "$*" in *h264_vaapi*) kind=HARDWARE;; esac
if [ "$kind" = HARDWARE ]; then
 i=0
 while [ "$i" -lt 5000 ]; do : > "$dir/a$i";i=$((i+1));done
fi
printf '%s' "$kind" > "$dir/seg000000.ts"
printf '#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:2.0,\nseg000000.ts\n' > "$output"
if [ "$kind" = HARDWARE ]; then exit 3;fi
exec sleep 300
`
	if err := os.WriteFile(tool, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	manager.options.FFmpeg, manager.options.DisableHardware = tool, false
	manager.caps = &Capabilities{Available: true, Encoders: []Encoder{{ID: "h264_vaapi", Verified: true}}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := manager.Start(ctx, Request{AssetID: "asset", Mode: "hardware"})
	if err != nil {
		t.Fatal(err)
	}
	if session.Backend == "libx264" {
		segment, err := manager.Read(ctx, session.ID, "seg000000.ts")
		if err != nil || string(segment) != "SOFTWARE" {
			t.Fatalf("stale attempt published: %+v %q %v", session, segment, err)
		}
	}
}

func TestIdleAndRestartCleanOnlyOwnedCache(t *testing.T) {
	manager, _ := testManager(t, func(options *Options) { options.IdleTimeout = 40 * time.Millisecond })
	session, err := manager.Start(context.Background(), Request{AssetID: "asset", Mode: "software"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for manager.Snapshot().Active != 0 {
		select {
		case <-deadline:
			t.Fatal("idle process leaked")
		case <-ticker.C:
		}
	}
	if _, err := manager.Read(context.Background(), session.ID, "index.m3u8"); !errors.Is(err, ErrNotFound) {
		t.Fatal("idle session retained")
	}
	dir := t.TempDir()
	keep := filepath.Join(dir, "unrelated.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{Dir: dir}); err == nil {
		t.Fatal("unowned nonempty cache accepted")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("unowned file removed")
	}
}
