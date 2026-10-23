//go:build playback_integration

package playback

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestActualHardwareEncodingProbe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	manager, err := New(Options{Dir: filepath.Join(t.TempDir(), "cache")})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	result := manager.Capabilities(ctx)
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ACTUAL_CAPABILITIES %s", encoded)
	if !result.Available {
		t.Fatal("FFmpeg tools unavailable")
	}
}

func TestActualFFmpegTranscodeSeekAndRemux(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	dir := t.TempDir()
	input := filepath.Join(dir, "TEST-101.mkv")
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100", "-t", "8", "-c:v", "mpeg4", "-c:a", "mp2", input)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sample: %v %s", err, output)
	}
	verifyHardware := os.Getenv("PLAYBACK_VERIFY_HARDWARE") == "1"
	options := Options{Dir: filepath.Join(dir, "cache"), DisableHardware: !verifyHardware, NoThrottle: true, Open: func(_ context.Context, id string) (Source, error) {
		f, err := os.Open(input)
		return Source{File: f, Name: "TEST-101.mkv"}, err
	}}
	if runtime.GOOS == "darwin" {
		options.commandForTest = testCacheCommand
	}
	manager, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	info, err := manager.Probe(ctx, "sample")
	if err != nil || info.VideoCodec != "mpeg4" {
		t.Fatalf("actual probe: %+v %v", info, err)
	}
	mode, backend := "software", "libx264"
	if verifyHardware {
		mode = "hardware"
		for _, encoder := range manager.Capabilities(ctx).Encoders {
			if encoder.Verified {
				backend = encoder.ID
				break
			}
		}
		if backend == "libx264" {
			t.Fatal("hardware session explicitly requested but no actual encoder probe passed")
		}
	}
	session, err := manager.Start(ctx, Request{AssetID: "sample", Mode: mode, Start: 2})
	if err != nil {
		t.Fatal(err)
	}
	if session.Offset != 2 || session.Backend != backend || session.Fallback {
		t.Fatalf("seek: %+v", session)
	}
	playlist, err := manager.Read(ctx, session.ID, "index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	var name string
	for _, line := range strings.Split(string(playlist), "\n") {
		if strings.HasSuffix(line, ".ts") {
			name = line
			break
		}
	}
	data, err := manager.Read(ctx, session.ID, name)
	if err != nil || len(data) == 0 {
		t.Fatalf("segment: %v", err)
	}
	segment := filepath.Join(dir, "sample.ts")
	if err := os.WriteFile(segment, data, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(segment)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := runProbe(ctx, manager.options.FFprobe, Source{File: file, Name: "sample.ts"}, []string{"-show_entries", "format=duration:stream=codec_type,codec_name,width,height"})
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := parseInfo(raw)
	if err != nil || decoded.VideoCodec != "h264" || decoded.AudioCodec != "aac" {
		t.Fatalf("actual output: %+v %v", decoded, err)
	}
	if err := manager.Stop(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if manager.Snapshot().Active != 0 || manager.Snapshot().CacheBytes != 0 {
		t.Fatal("actual conversion leaked")
	}
	// A separate H.264 MKV proves that remux is not mislabeled re-encoding.
	h264 := filepath.Join(dir, "TEST-102.mkv")
	command := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-i", input, "-c:v", "libx264", "-g", "48", "-c:a", "aac", h264)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("H.264 sample: %v %s", err, output)
	}
	manager.options.Open = func(_ context.Context, id string) (Source, error) {
		f, err := os.Open(h264)
		return Source{File: f, Name: "TEST-102.mkv"}, err
	}
	remux, err := manager.Start(ctx, Request{AssetID: "h264", Mode: "remux", Start: 4.7})
	if err != nil {
		t.Fatal(err)
	}
	if remux.Backend != "copy" || remux.Offset > 4.7 || remux.Offset < 3.9 {
		t.Fatalf("actual keyframe: %+v", remux)
	}
}
