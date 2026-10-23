package playback

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type Encoder struct {
	ID       string `json:"id"`
	Verified bool   `json:"verified"`
	Device   string `json:"-"`
	Reason   string `json:"reason,omitempty"`
}
type Capabilities struct {
	Available bool      `json:"available"`
	Encoders  []Encoder `json:"encoders"`
	Decode    string    `json:"decode"`
	Snapshot  Snapshot  `json:"resources"`
}

func (m *Manager) Capabilities(ctx context.Context) Capabilities {
	m.capsMu.Lock()
	defer m.capsMu.Unlock()
	if m.caps != nil {
		value := *m.caps
		value.Snapshot = m.Snapshot()
		return value
	}
	value := Capabilities{Available: m.options.FFmpeg != "" && m.options.FFprobe != "", Encoders: []Encoder{}, Decode: "software", Snapshot: m.Snapshot()}
	if value.Available && !m.options.DisableHardware {
		probeCtx, finish, err := m.beginProbe(ctx)
		if err != nil {
			return value
		}
		defer finish()
		ctx, cancel := context.WithTimeout(probeCtx, 15*time.Second)
		defer cancel()
		candidates := []Encoder{}
		if runtime.GOOS == "linux" {
			if _, err := os.Stat("/dev/dri/renderD128"); err == nil {
				candidates = append(candidates, Encoder{ID: "h264_vaapi", Device: "/dev/dri/renderD128"})
			}
			if _, err := os.Stat("/dev/nvidia0"); err == nil {
				candidates = append(candidates, Encoder{ID: "h264_nvenc"})
			}
		}
		if runtime.GOOS == "darwin" {
			candidates = append(candidates, Encoder{ID: "h264_videotoolbox"})
		}
		for _, candidate := range candidates {
			candidate.Verified = m.testEncoder(ctx, candidate)
			if !candidate.Verified {
				candidate.Reason = "actual_encode_failed"
			}
			value.Encoders = append(value.Encoders, candidate)
		}
	}
	if ctx.Err() == nil {
		saved := value
		m.caps = &saved
	}
	return value
}
func (m *Manager) testEncoder(ctx context.Context, encoder Encoder) bool {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return false
	}
	dir := "probe-" + hex.EncodeToString(random[:])
	if err := m.root.Mkdir(dir, 0700); err != nil {
		return false
	}
	if err := m.root.WriteFile(dir+"/.owner", []byte(cacheMarker), 0600); err != nil {
		return false
	}
	cache, err := m.root.Open(dir)
	if err != nil {
		return false
	}
	defer cache.Close()
	identity, err := cache.Stat()
	if err != nil {
		return false
	}
	defer func() {
		if current, err := m.root.Stat(dir); err == nil && os.SameFile(identity, current) {
			_ = m.root.RemoveAll(dir)
		}
	}()
	output := cacheDescriptorPath() + "/test.mp4"
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}
	if encoder.ID == "h264_vaapi" {
		args = append(args, "-vaapi_device", encoder.Device)
	}
	args = append(args, "-f", "lavfi", "-i", "color=size=128x72:rate=10", "-frames:v", "3", "-an")
	if encoder.ID == "h264_vaapi" {
		args = append(args, "-vf", "format=nv12,hwupload")
	}
	args = append(args, "-c:v", encoder.ID)
	if encoder.ID == "h264_videotoolbox" {
		args = append(args, "-allow_sw", "0")
	}
	args = append(args, "-f", "mp4", output)
	builder := m.options.commandForTest
	if builder == nil {
		builder = exec.CommandContext
	}
	command := builder(ctx, m.options.FFmpeg, args...)
	command.ExtraFiles = []*os.File{nil, cache}
	prepareProcess(command)
	command.Stderr = &discardOverflow{limit: 4096}
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil {
		return false
	}
	file, err := m.root.Open(dir + "/test.mp4")
	if err != nil {
		return false
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || info.Size() == 0 || info.Size() > 1<<20 {
		return false
	}
	raw, err := runProbe(ctx, m.options.FFprobe, Source{File: file, Name: "test.mp4"}, []string{"-show_entries", "format=duration:stream=codec_type,codec_name,width,height"})
	if err != nil {
		return false
	}
	info, err := parseInfo(raw)
	return err == nil && strings.EqualFold(info.VideoCodec, "h264")
}
