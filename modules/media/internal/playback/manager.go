package playback

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Options struct {
	Dir             string
	FFmpeg          string
	FFprobe         string
	Open            OpenSource
	DisableHardware bool
	MaxSessions     int
	MaxBytes        int64
	IdleTimeout     time.Duration
	PollInterval    time.Duration
	// NoThrottle is only used for isolated short integration fixtures.
	NoThrottle     bool
	commandForTest func(context.Context, string, ...string) *exec.Cmd
}
type Request struct {
	AssetID string  `json:"asset_id"`
	Mode    string  `json:"mode"`
	Quality string  `json:"quality"`
	Start   float64 `json:"start"`
}
type Session struct {
	ID       string  `json:"id"`
	AssetID  string  `json:"asset_id"`
	Mode     string  `json:"mode"`
	Backend  string  `json:"backend"`
	Quality  string  `json:"quality"`
	Offset   float64 `json:"offset"`
	Seek     float64 `json:"seek"`
	Duration float64 `json:"duration"`
	Fallback bool    `json:"fallback"`
	Paused   bool    `json:"paused"`
	State    string  `json:"state"`
}
type sessionState struct {
	view       Session
	dir        string
	sourceInfo os.FileInfo
	cacheInfo  os.FileInfo
	lastAccess time.Time
	cancel     context.CancelFunc
	done       chan struct{}
	command    *exec.Cmd
	cause      error
	completed  bool
}
type Snapshot struct {
	Active      int   `json:"active"`
	CacheBytes  int64 `json:"cache_bytes"`
	MaxSessions int   `json:"max_sessions"`
	MaxBytes    int64 `json:"max_bytes"`
}
type Manager struct {
	options     Options
	mu          sync.Mutex
	sessions    map[string]*sessionState
	root        *os.Root
	ctx         context.Context
	cancel      context.CancelFunc
	closed      bool
	monitorDone chan struct{}
	probeSlots  chan struct{}
	capsMu      sync.Mutex
	caps        *Capabilities
	workers     sync.WaitGroup
	closeMu     sync.Mutex
	rootClosed  bool
}

const cacheMarker = "wayyoung-playback-cache-v1\n"

var sessionName = regexp.MustCompile(`^(session|probe)-[a-f0-9]{32}$`)
var mediaName = regexp.MustCompile(`^(index\.m3u8|seg[0-9]{6}\.ts)$`)

func New(options Options) (*Manager, error) {
	if options.Dir == "" {
		return nil, ErrInvalid
	}
	if options.MaxSessions <= 0 {
		options.MaxSessions = 2
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = 512 << 20
	}
	if options.IdleTimeout <= 0 {
		options.IdleTimeout = 90 * time.Second
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 500 * time.Millisecond
	}
	if options.FFmpeg == "" {
		options.FFmpeg, _ = exec.LookPath("ffmpeg")
	}
	if options.FFprobe == "" {
		options.FFprobe, _ = exec.LookPath("ffprobe")
	}
	if err := os.MkdirAll(options.Dir, 0700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(options.Dir); err != nil || !ownedDirectory(info) || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalid
	}
	root, err := os.OpenRoot(options.Dir)
	if err != nil {
		return nil, err
	}
	valid := false
	defer func() {
		if !valid {
			root.Close()
		}
	}()
	marker, err := root.ReadFile(".owner")
	if errors.Is(err, os.ErrNotExist) {
		entries, readErr := os.ReadDir(options.Dir)
		if readErr != nil {
			return nil, readErr
		}
		if len(entries) > 0 {
			return nil, ErrInvalid
		}
		if err := root.WriteFile(".owner", []byte(cacheMarker), 0600); err != nil {
			return nil, err
		}
	} else if err != nil || string(marker) != cacheMarker {
		return nil, ErrInvalid
	}
	entries, err := os.ReadDir(options.Dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if sessionName.MatchString(entry.Name()) && entry.IsDir() {
			owner, err := root.ReadFile(entry.Name() + "/.owner")
			if err == nil && string(owner) == cacheMarker {
				if err := root.RemoveAll(entry.Name()); err != nil {
					return nil, err
				}
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{options: options, root: root, ctx: ctx, cancel: cancel, sessions: map[string]*sessionState{}, probeSlots: make(chan struct{}, 2), monitorDone: make(chan struct{})}
	valid = true
	go manager.monitor()
	return manager, nil
}

func (m *Manager) Start(ctx context.Context, request Request) (Session, error) {
	if request.Mode == "" {
		request.Mode = "auto"
	}
	if request.Quality == "" {
		request.Quality = "1080p"
	}
	if request.AssetID == "" || len(request.AssetID) > 100 || !validPosition(request.Start) {
		return Session{}, ErrInvalid
	}
	switch request.Mode {
	case "auto", "software", "hardware", "remux":
	default:
		return Session{}, ErrInvalid
	}
	if request.Quality != "1080p" && request.Quality != "720p" && request.Quality != "480p" {
		return Session{}, ErrInvalid
	}
	m.mu.Lock()
	busy := m.closed || len(m.sessions) >= m.options.MaxSessions
	m.mu.Unlock()
	if busy {
		return Session{}, ErrBusy
	}
	if m.options.FFmpeg == "" || m.options.FFprobe == "" || m.options.Open == nil {
		return Session{}, ErrUnavailable
	}
	info, err := m.Probe(ctx, request.AssetID)
	if err != nil {
		return Session{}, err
	}
	if request.Start >= info.Duration {
		return Session{}, ErrInvalid
	}
	mode, backend, offset := "transcode", "libx264", request.Start
	copyAllowed := info.VideoCodec == "h264" && (info.PixelFormat == "" || info.PixelFormat == "yuv420p") && (info.Bitrate <= 16000000 || info.Bitrate == 0) && info.Height <= qualityHeight(request.Quality)
	if request.Mode == "remux" && !copyAllowed {
		return Session{}, ErrUnsupported
	}
	if (request.Mode == "auto" || request.Mode == "remux") && copyAllowed {
		offset, err = m.keyframe(ctx, request.AssetID, request.Start)
		if err == nil {
			mode, backend = "remux", "copy"
		} else if request.Mode == "remux" {
			return Session{}, err
		} else {
			offset = request.Start
		}
	}
	device := ""
	if mode == "transcode" && request.Mode != "software" && !m.options.DisableHardware {
		capabilities := m.Capabilities(ctx)
		for _, candidate := range capabilities.Encoders {
			if candidate.Verified {
				backend, device = candidate.ID, candidate.Device
				break
			}
		}
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Session{}, err
	}
	id := hex.EncodeToString(random[:])
	dir := "session-" + id
	sessionCtx, cancel := context.WithCancel(m.ctx)
	state := &sessionState{view: Session{ID: id, AssetID: request.AssetID, Mode: mode, Backend: backend, Quality: request.Quality, Offset: offset, Seek: request.Start - offset, Duration: info.Duration, State: "starting"}, dir: dir, lastAccess: time.Now(), cancel: cancel, done: make(chan struct{})}
	m.mu.Lock()
	if m.closed || len(m.sessions) >= m.options.MaxSessions {
		m.mu.Unlock()
		cancel()
		return Session{}, ErrBusy
	}
	if err := m.root.Mkdir(dir, 0700); err != nil {
		m.mu.Unlock()
		cancel()
		return Session{}, err
	}
	if err := m.root.WriteFile(dir+"/.owner", []byte(cacheMarker), 0600); err != nil {
		m.root.RemoveAll(dir)
		m.mu.Unlock()
		cancel()
		return Session{}, err
	}
	state.cacheInfo, err = m.root.Stat(dir)
	if err != nil {
		m.root.RemoveAll(dir)
		m.mu.Unlock()
		cancel()
		return Session{}, err
	}
	m.sessions[id] = state
	m.mu.Unlock()
	go m.convert(sessionCtx, state, device)
	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(25 * time.Second)
	defer timeout.Stop()
	done := state.done
	for {
		select {
		case <-ctx.Done():
			m.Stop(context.Background(), id)
			return Session{}, ctx.Err()
		case <-timeout.C:
			m.Stop(context.Background(), id)
			return Session{}, ErrProcess
		case <-done:
			m.mu.Lock()
			cause := state.cause
			m.mu.Unlock()
			if cause != nil {
				m.Stop(context.Background(), id)
				return Session{}, cause
			}
			done = nil
		case <-ticker.C:
		}
		// Publishing an attempt and clearing it for fallback share one lock.
		// A manifest read before that boundary must never publish the next backend.
		m.mu.Lock()
		data, readErr := m.root.ReadFile(dir + "/index.m3u8")
		if readErr == nil && len(data) > 0 && strings.Contains(string(data), "#EXTINF:") {
			if m.sessions[id] != state || state.cause != nil || m.closed {
				cause := state.cause
				if cause == nil {
					cause = ErrProcess
				}
				m.mu.Unlock()
				m.Stop(context.Background(), id)
				return Session{}, cause
			}
			state.view.State = "ready"
			state.lastAccess = time.Now()
			view := state.view
			m.mu.Unlock()
			return view, nil
		}
		m.mu.Unlock()
	}
}

func qualityHeight(quality string) int {
	if quality == "480p" {
		return 480
	}
	if quality == "720p" {
		return 720
	}
	return 1080
}
func (m *Manager) commandArgs(view Session, name, device string) ([]string, error) {
	input, err := sourceArguments(name)
	if err != nil {
		return nil, err
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-threads", "2", "-filter_threads", "2"}
	if device != "" && view.Backend == "h264_vaapi" {
		args = append(args, "-vaapi_device", device)
	}
	if !m.options.NoThrottle {
		args = append(args, "-re")
	}
	args = append(args, "-ss", strconv.FormatFloat(view.Offset, 'f', 6, 64))
	args = append(args, input...)
	args = append(args, "-i", descriptorPath(), "-map", "0:v:0", "-map", "0:a:0?", "-sn", "-dn", "-map_metadata", "-1")
	if view.Backend == "copy" {
		args = append(args, "-c:v", "copy")
	} else {
		height := qualityHeight(view.Quality)
		width := height * 16 / 9
		filter := fmt.Sprintf("scale=w='min(%d,iw)':h='min(%d,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2", width, height)
		if view.Backend == "h264_vaapi" {
			filter += ",format=nv12,hwupload"
		} else {
			filter += ",format=yuv420p"
		}
		args = append(args, "-vf", filter, "-c:v", view.Backend, "-b:v", "3M", "-maxrate", "4M", "-bufsize", "8M", "-force_key_frames", "expr:gte(t,n_forced*2)")
		if view.Backend == "libx264" {
			args = append(args, "-preset", "veryfast")
		}
		if view.Backend == "h264_videotoolbox" {
			args = append(args, "-allow_sw", "0")
		}
	}
	output := cacheDescriptorPath()
	args = append(args, "-c:a", "aac", "-b:a", "128k", "-ac", "2", "-ar", "48000", "-max_muxing_queue_size", "256", "-f", "hls", "-hls_time", "2", "-hls_list_size", "12", "-hls_delete_threshold", "2", "-hls_flags", "delete_segments+temp_file+independent_segments", "-hls_segment_filename", output+"/seg%06d.ts", output+"/index.m3u8")
	return args, nil
}

func (m *Manager) convert(ctx context.Context, state *sessionState, device string) {
	defer close(state.done)
	attempt := func() error {
		m.mu.Lock()
		view := state.view
		m.mu.Unlock()
		source, err := m.options.Open(ctx, view.AssetID)
		if err != nil {
			return err
		}
		defer source.File.Close()
		info, err := source.File.Stat()
		if err != nil {
			return err
		}
		cache, err := m.openCache(state)
		if err != nil {
			return err
		}
		defer cache.Close()
		args, err := m.commandArgs(view, source.Name, device)
		if err != nil {
			return err
		}
		builder := m.options.commandForTest
		if builder == nil {
			builder = exec.CommandContext
		}
		command := builder(ctx, m.options.FFmpeg, args...)
		command.ExtraFiles = []*os.File{source.File, cache}
		command.WaitDelay = time.Second
		prepareProcess(command)
		command.Stderr = &discardOverflow{limit: 4096}
		m.mu.Lock()
		if state.sourceInfo != nil && !sameSource(state.sourceInfo, info) {
			m.mu.Unlock()
			return ErrChanged
		}
		state.sourceInfo = info
		if err := command.Start(); err != nil {
			m.mu.Unlock()
			return err
		}
		state.command = command
		if state.view.Paused {
			_ = pauseProcess(command.Process, true)
		}
		m.mu.Unlock()
		err = command.Wait()
		m.mu.Lock()
		state.command = nil
		m.mu.Unlock()
		return err
	}
	err := attempt()
	m.mu.Lock()
	fallback := err != nil && ctx.Err() == nil && state.view.State == "starting" && state.view.Backend != "copy" && state.view.Backend != "libx264" && !errors.Is(err, ErrChanged)
	if fallback {
		state.view.Backend = "libx264"
		state.view.Fallback = true
		state.command = nil
		cache, cacheErr := m.openCache(state)
		if cacheErr != nil {
			err = cacheErr
		} else {
			entries, readErr := cache.ReadDir(-1)
			cache.Close()
			cacheErr = readErr
			for _, entry := range entries {
				if mediaName.MatchString(strings.TrimSuffix(entry.Name(), ".tmp")) {
					if removeErr := m.root.Remove(state.dir + "/" + entry.Name()); removeErr != nil {
						cacheErr = removeErr
					}
				}
			}
		}
		m.mu.Unlock()
		if cacheErr == nil {
			err = attempt()
		} else {
			err = cacheErr
		}
	} else {
		m.mu.Unlock()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state.completed = true
	if ctx.Err() != nil {
		if state.cause == nil {
			state.cause = ctx.Err()
		}
		state.view.State = "stopped"
	} else if err != nil {
		state.cause = ErrProcess
		if errors.Is(err, ErrChanged) {
			state.cause = ErrChanged
		}
		state.view.State = "failed"
	} else {
		state.view.State = "completed"
	}
}

func (m *Manager) openCache(state *sessionState) (*os.File, error) {
	file, err := m.root.Open(state.dir)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || state.cacheInfo == nil || !os.SameFile(state.cacheInfo, info) {
		file.Close()
		return nil, ErrChanged
	}
	return file, nil
}

func (m *Manager) validate(ctx context.Context, state *sessionState) error {
	source, err := m.options.Open(ctx, state.view.AssetID)
	if err != nil {
		return err
	}
	defer source.File.Close()
	info, err := source.File.Stat()
	if err != nil {
		return err
	}
	m.mu.Lock()
	saved := state.sourceInfo
	m.mu.Unlock()
	if saved == nil || !sameSource(saved, info) {
		return ErrChanged
	}
	return nil
}
func (m *Manager) Read(ctx context.Context, id, name string) ([]byte, error) {
	if !mediaName.MatchString(name) {
		return nil, ErrInvalid
	}
	m.mu.Lock()
	state := m.sessions[id]
	if state != nil {
		state.lastAccess = time.Now()
	}
	m.mu.Unlock()
	if state == nil {
		return nil, ErrNotFound
	}
	if err := m.validate(ctx, state); err != nil {
		m.Stop(context.Background(), id)
		return nil, ErrChanged
	}
	m.mu.Lock()
	cause := state.cause
	m.mu.Unlock()
	if cause != nil {
		return nil, cause
	}
	cache, err := m.openCache(state)
	if err != nil {
		m.Stop(context.Background(), id)
		return nil, err
	}
	cache.Close()
	file, err := m.root.Open(state.dir + "/" + name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() > 32<<20 {
		return nil, ErrInvalid
	}
	data := make([]byte, stat.Size())
	if _, err := file.ReadAt(data, 0); err != nil {
		return nil, err
	}
	return data, nil
}
func (m *Manager) Status(ctx context.Context, id string) (Session, error) {
	m.mu.Lock()
	state := m.sessions[id]
	if state != nil {
		state.lastAccess = time.Now()
	}
	m.mu.Unlock()
	if state == nil {
		return Session{}, ErrNotFound
	}
	if err := m.validate(ctx, state); err != nil {
		m.Stop(context.Background(), id)
		return Session{}, ErrChanged
	}
	cache, err := m.openCache(state)
	if err != nil {
		m.Stop(context.Background(), id)
		return Session{}, ErrChanged
	}
	cache.Close()
	m.mu.Lock()
	defer m.mu.Unlock()
	return state.view, nil
}
func (m *Manager) Pulse(ctx context.Context, id string, paused bool) (Session, error) {
	if _, err := m.Status(ctx, id); err != nil {
		return Session{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.sessions[id]
	if state == nil {
		return Session{}, ErrNotFound
	}
	if state.view.Paused != paused && state.command != nil && state.command.Process != nil {
		if err := pauseProcess(state.command.Process, paused); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return Session{}, err
		}
	}
	state.view.Paused = paused
	return state.view, nil
}
func (m *Manager) Stop(ctx context.Context, id string) error {
	m.mu.Lock()
	state := m.sessions[id]
	if state == nil {
		m.mu.Unlock()
		return nil
	}
	state.cancel()
	m.mu.Unlock()
	select {
	case <-state.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[id] != state {
		return nil
	}
	delete(m.sessions, id)
	cache, err := m.openCache(state)
	if err != nil {
		return ErrChanged
	}
	cache.Close()
	return m.root.RemoveAll(state.dir)
}
func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := Snapshot{Active: len(m.sessions), MaxSessions: m.options.MaxSessions, MaxBytes: m.options.MaxBytes}
	for _, state := range m.sessions {
		cache, err := m.openCache(state)
		if err != nil {
			continue
		}
		entries, _ := cache.ReadDir(-1)
		cache.Close()
		for _, entry := range entries {
			if info, err := entry.Info(); err == nil {
				result.CacheBytes += info.Size()
			}
		}
	}
	return result
}
func (m *Manager) monitor() {
	defer close(m.monitorDone)
	ticker := time.NewTicker(m.options.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
		}
		snapshot := m.Snapshot()
		m.mu.Lock()
		ids := []string{}
		for id, state := range m.sessions {
			idleLimit := m.options.IdleTimeout
			if state.view.State == "starting" && idleLimit < 30*time.Second {
				idleLimit = 30 * time.Second
			}
			if time.Since(state.lastAccess) > idleLimit || snapshot.CacheBytes > m.options.MaxBytes {
				if snapshot.CacheBytes > m.options.MaxBytes {
					state.cause = ErrCacheLimit
				}
				ids = append(ids, id)
			}
		}
		m.mu.Unlock()
		for _, id := range ids {
			_ = m.Stop(m.ctx, id)
		}
	}
}
func (m *Manager) Close(ctx context.Context) error {
	m.closeMu.Lock()
	defer m.closeMu.Unlock()
	if m.rootClosed {
		return nil
	}
	m.mu.Lock()
	m.closed = true
	ids := []string{}
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	m.cancel()
	var cleanupErr error
	for _, id := range ids {
		if err := m.Stop(ctx, id); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	done := make(chan struct{})
	go func() { m.workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return errors.Join(cleanupErr, ctx.Err())
	}
	select {
	case <-m.monitorDone:
		m.rootClosed = true
		return errors.Join(cleanupErr, m.root.Close())
	case <-ctx.Done():
		return ctx.Err()
	}
}
