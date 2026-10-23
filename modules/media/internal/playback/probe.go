package playback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Source struct {
	File *os.File
	Name string
}
type OpenSource func(context.Context, string) (Source, error)
type Info struct {
	Duration    float64 `json:"duration"`
	Container   string  `json:"container"`
	VideoCodec  string  `json:"video_codec"`
	AudioCodec  string  `json:"audio_codec"`
	PixelFormat string  `json:"pixel_format"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	Bitrate     int64   `json:"bitrate"`
}
type probeOutput struct {
	Format struct {
		Duration string `json:"duration"`
		Name     string `json:"format_name"`
		Bitrate  string `json:"bit_rate"`
	} `json:"format"`
	Streams []struct {
		Type        string `json:"codec_type"`
		Codec       string `json:"codec_name"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		PixelFormat string `json:"pix_fmt"`
	} `json:"streams"`
	Frames []struct {
		Time string `json:"best_effort_timestamp_time"`
	} `json:"frames"`
}

func inputFormat(name string) (string, error) {
	formats := map[string]string{".mp4": "mov", ".m4v": "mov", ".mov": "mov", ".mkv": "matroska", ".webm": "matroska", ".avi": "avi", ".ts": "mpegts", ".m2ts": "mpegts", ".mts": "mpegts", ".mpg": "mpeg", ".mpeg": "mpeg", ".wmv": "asf", ".asf": "asf", ".flv": "flv", ".ogv": "ogg", ".rm": "rm", ".rmvb": "rm"}
	format := formats[strings.ToLower(filepath.Ext(name))]
	if format == "" {
		return "", ErrUnsupported
	}
	return format, nil
}
func descriptorPath() string {
	if runtime.GOOS == "linux" {
		return "/proc/self/fd/3"
	}
	return "/dev/fd/3"
}
func sourceArguments(name string) ([]string, error) {
	format, err := inputFormat(name)
	if err != nil {
		return nil, err
	}
	// No auto-detection of playlists, concat scripts or network protocols. The
	// only input is an already-authorized descriptor; MOV external refs stay off.
	args := []string{"-max_alloc", "268435456", "-f", format, "-protocol_whitelist", "file,pipe"}
	if format == "mov" {
		args = append(args, "-enable_drefs", "0", "-use_absolute_path", "0")
	}
	return args, nil
}

type limitedOutput struct {
	bytes.Buffer
	limit  int
	cancel context.CancelFunc
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		b.cancel()
		return 0, ErrInvalid
	}
	return b.Buffer.Write(p)
}

type discardOverflow struct {
	bytes.Buffer
	limit int
}

func (b *discardOverflow) Write(p []byte) (int, error) {
	n := len(p)
	if left := b.limit - b.Len(); left > 0 {
		if len(p) > left {
			p = p[:left]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func runProbe(ctx context.Context, tool string, source Source, extra []string) (probeOutput, error) {
	var parsed probeOutput
	input, err := sourceArguments(source.Name)
	if err != nil {
		return parsed, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := append([]string{"-v", "error"}, input...)
	args = append(args, extra...)
	args = append(args, "-of", "json", "-i", descriptorPath())
	command := exec.CommandContext(ctx, tool, args...)
	prepareProcess(command)
	command.ExtraFiles = []*os.File{source.File}
	command.WaitDelay = time.Second
	output := &limitedOutput{limit: 1 << 20, cancel: cancel}
	command.Stdout = output
	command.Stderr = &discardOverflow{limit: 4096}
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return parsed, ctx.Err()
		}
		return parsed, ErrProbe
	}
	if err := json.Unmarshal(output.Bytes(), &parsed); err != nil {
		return parsed, ErrProbe
	}
	return parsed, nil
}
func parseInfo(raw probeOutput) (Info, error) {
	duration, err := strconv.ParseFloat(raw.Format.Duration, 64)
	if err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 || duration > 7*24*3600 {
		return Info{}, ErrUnsupported
	}
	info := Info{Duration: duration, Container: raw.Format.Name}
	info.Bitrate, _ = strconv.ParseInt(raw.Format.Bitrate, 10, 64)
	for _, stream := range raw.Streams {
		if stream.Type == "video" && info.VideoCodec == "" {
			info.VideoCodec = stream.Codec
			info.Width = stream.Width
			info.Height = stream.Height
			info.PixelFormat = stream.PixelFormat
		}
		if stream.Type == "audio" && info.AudioCodec == "" {
			info.AudioCodec = stream.Codec
		}
	}
	if info.VideoCodec == "" || info.Width < 1 || info.Height < 1 || info.Width > 8192 || info.Height > 8192 {
		return Info{}, ErrUnsupported
	}
	return info, nil
}
func (m *Manager) Probe(ctx context.Context, id string) (Info, error) {
	if m.options.Open == nil || m.options.FFprobe == "" {
		return Info{}, ErrUnavailable
	}
	ctx, finish, err := m.beginProbe(ctx)
	if err != nil {
		return Info{}, err
	}
	defer finish()
	source, err := m.options.Open(ctx, id)
	if err != nil {
		return Info{}, err
	}
	defer source.File.Close()
	parsed, err := runProbe(ctx, m.options.FFprobe, source, []string{"-show_entries", "format=duration,format_name,bit_rate:stream=codec_type,codec_name,width,height,pix_fmt"})
	if err != nil {
		return Info{}, err
	}
	return parseInfo(parsed)
}
func (m *Manager) keyframe(ctx context.Context, id string, target float64) (float64, error) {
	if target == 0 {
		return 0, nil
	}
	ctx, finish, err := m.beginProbe(ctx)
	if err != nil {
		return 0, err
	}
	defer finish()
	source, err := m.options.Open(ctx, id)
	if err != nil {
		return 0, err
	}
	defer source.File.Close()
	parsed, err := runProbe(ctx, m.options.FFprobe, source, []string{"-select_streams", "v:0", "-skip_frame", "nokey", "-read_intervals", fmt.Sprintf("%.6f%%+10", target), "-show_entries", "frame=best_effort_timestamp_time"})
	if err != nil {
		return 0, err
	}
	for _, frame := range parsed.Frames {
		value, err := strconv.ParseFloat(frame.Time, 64)
		if err == nil && value >= 0 && value <= target {
			return value, nil
		}
	}
	return 0, ErrUnsupported
}

func (m *Manager) beginProbe(ctx context.Context) (context.Context, func(), error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, nil, ErrUnavailable
	}
	m.workers.Add(1)
	m.mu.Unlock()
	child, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	finish := func() { stop(); cancel(); m.workers.Done() }
	select {
	case m.probeSlots <- struct{}{}:
		return child, func() { <-m.probeSlots; finish() }, nil
	case <-child.Done():
		finish()
		return nil, nil, child.Err()
	}
}
func validPosition(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}
func sameSource(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

var (
	ErrInvalid     = errors.New("invalid playback request")
	ErrUnavailable = errors.New("playback tools unavailable")
	ErrUnsupported = errors.New("unsupported media or playback mode")
	ErrProbe       = errors.New("media probe failed")
	ErrBusy        = errors.New("playback capacity reached")
	ErrNotFound    = errors.New("playback session not found")
	ErrChanged     = errors.New("media changed during playback")
	ErrProcess     = errors.New("media conversion failed")
	ErrCacheLimit  = errors.New("playback cache limit reached")
)

var _ io.Writer = (*discardOverflow)(nil)
