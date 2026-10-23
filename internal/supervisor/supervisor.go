package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	backupmodule "fnos-backup-center/module"
	mediamodule "github.com/wayyoungboy/fnos-media-acquisition-center/module"
	musicmodule "github.com/wayyoungboy/fnos-music-converter/module"
	ha500module "github.com/wayyoungboy/ha500-fnos-toolbox/module"
	integritymodule "github.com/wayyoungboy/wayyoung_toolbox/internal/integrity"
)

type Definition struct {
	ID            string
	Name          string
	Version       string
	Description   string
	Prefix        string
	PublicDir     string
	BundleDir     string
	RequiresHA500 bool
}

type Info struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Version       string    `json:"version"`
	Description   string    `json:"description"`
	Enabled       bool      `json:"enabled"`
	Running       bool      `json:"running"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	RequiresHA500 bool      `json:"requires_ha500_confirmation"`
}

type persistentState struct {
	Enabled map[string]bool `json:"enabled"`
}

type inProcessModule interface {
	Handler() http.Handler
	Close(context.Context) error
}

type runtime struct {
	module    inProcessModule
	startedAt time.Time
}

type Manager struct {
	mu        sync.RWMutex
	appDir    string
	dataDir   string
	statePath string
	defs      map[string]Definition
	state     persistentState
	runtimes  map[string]*runtime
	lastError map[string]string
	ctx       context.Context
}

func DefaultDefinitions(appDir string) []Definition {
	modulePath := func(parts ...string) string {
		all := append([]string{appDir, "modules"}, parts...)
		return filepath.Join(all...)
	}
	prefix := "/app/wayyoung_toolbox/modules/"
	return []Definition{
		{
			ID: "backup", Name: "飞牛备份中心", Version: "0.0.6",
			Description: "本地目录增量备份、版本记录与恢复。",
			Prefix:      prefix + "backup", PublicDir: modulePath("backup", "public"),
		},
		{
			ID: "ha500", Name: "HA500 硬件工具", Version: "0.2.0",
			Description: "硬盘灯、独立风扇策略和硬件诊断。",
			Prefix:      prefix + "ha500", RequiresHA500: true,
		},
		{
			ID: "integrity", Name: "文件完整性检查", Version: "0.1.1",
			Description: "按路径检查文件可读性与常见格式结构，生成分类报告。",
			Prefix:      prefix + "integrity", PublicDir: modulePath("integrity", "public"),
		},
		{
			ID: "media", Name: "个人媒体中心", Version: "0.1.2",
			Description: "馆藏、播放、观看记录与 RSS、下载入库、JAVSP 和媒体维护。",
			Prefix:      prefix + "media", PublicDir: modulePath("media", "public"), BundleDir: modulePath("media", "javsp"),
		},
		{
			ID: "music", Name: "音乐转换中心", Version: "0.0.4",
			Description: "NCM 还原、MP3 迁移和转换记录。",
			Prefix:      prefix + "music", PublicDir: modulePath("music", "public"),
		},
	}
}

func New(appDir, dataDir string, definitions []Definition) (*Manager, error) {
	if !filepath.IsAbs(appDir) || !filepath.IsAbs(dataDir) || appDir == "/" || dataDir == "/" {
		return nil, errors.New("application and data directories must be absolute non-root paths")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	defs := make(map[string]Definition, len(definitions))
	state := persistentState{Enabled: make(map[string]bool, len(definitions))}
	for _, definition := range definitions {
		if definition.ID == "" || defs[definition.ID].ID != "" {
			return nil, fmt.Errorf("invalid or duplicate module id %q", definition.ID)
		}
		defs[definition.ID] = definition
		state.Enabled[definition.ID] = false
	}
	manager := &Manager{
		appDir: appDir, dataDir: dataDir, statePath: filepath.Join(dataDir, "modules.json"),
		defs: defs, state: state, runtimes: make(map[string]*runtime), lastError: make(map[string]string),
	}
	if err := manager.load(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (manager *Manager) load() error {
	data, err := os.ReadFile(manager.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var loaded persistentState
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("read module state: %w", err)
	}
	for id := range manager.defs {
		manager.state.Enabled[id] = loaded.Enabled[id]
	}
	return nil
}

func (manager *Manager) saveLocked() error {
	data, err := json.MarshalIndent(manager.state, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(manager.dataDir, ".modules-")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(append(data, '\n'))
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, manager.statePath)
}

func (manager *Manager) Start(ctx context.Context) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.ctx = ctx
	for id, enabled := range manager.state.Enabled {
		if !enabled {
			continue
		}
		definition := manager.defs[id]
		if definition.RequiresHA500 {
			marker := filepath.Join(manager.moduleDataDir(id), "ha500-model-confirmed")
			if _, err := os.Stat(marker); err != nil {
				manager.lastError[id] = "HA500 型号确认记录缺失，模块没有启用"
				continue
			}
		}
		if err := manager.startLocked(id); err != nil {
			manager.lastError[id] = err.Error()
		}
	}
}

func (manager *Manager) Close() {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for id := range manager.runtimes {
		_ = manager.stopLocked(id)
	}
}

func (manager *Manager) List() []Info {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	result := make([]Info, 0, len(manager.defs))
	for id, definition := range manager.defs {
		info := Info{
			ID: id, Name: definition.Name, Version: definition.Version, Description: definition.Description,
			Enabled: manager.state.Enabled[id], LastError: manager.lastError[id], RequiresHA500: definition.RequiresHA500,
		}
		if current := manager.runtimes[id]; current != nil {
			info.Running = true
			info.StartedAt = current.startedAt
		}
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (manager *Manager) SetEnabled(id string, enabled bool, confirmation string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	definition, ok := manager.defs[id]
	if !ok {
		return os.ErrNotExist
	}
	if enabled == manager.state.Enabled[id] {
		if !enabled || manager.runtimes[id] != nil {
			return nil
		}
	}
	if enabled {
		writeHA500Marker := false
		if definition.RequiresHA500 {
			marker := filepath.Join(manager.moduleDataDir(id), "ha500-model-confirmed")
			if _, err := os.Stat(marker); err != nil {
				if confirmation != "HA500" {
					return errors.New("启用硬件模块前必须输入 HA500 确认型号")
				}
				writeHA500Marker = true
			}
		}
		if err := manager.startLocked(id); err != nil {
			manager.lastError[id] = err.Error()
			return err
		}
		if writeHA500Marker {
			marker := filepath.Join(manager.moduleDataDir(id), "ha500-model-confirmed")
			if err := os.WriteFile(marker, []byte("confirmed\n"), 0o600); err != nil {
				_ = manager.stopLocked(id)
				return err
			}
		}
		manager.state.Enabled[id] = true
		manager.lastError[id] = ""
		if err := manager.saveLocked(); err != nil {
			_ = manager.stopLocked(id)
			manager.state.Enabled[id] = false
			return err
		}
		return nil
	}
	if err := manager.stopLocked(id); err != nil {
		manager.lastError[id] = err.Error()
		return err
	}
	manager.state.Enabled[id] = false
	manager.lastError[id] = ""
	return manager.saveLocked()
}

func (manager *Manager) startLocked(id string) error {
	if manager.runtimes[id] != nil {
		return nil
	}
	definition := manager.defs[id]
	dataDir := manager.moduleDataDir(id)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	ctx := manager.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	var instance inProcessModule
	var err error
	switch id {
	case "backup":
		var roots []string
		roots, err = backupRoots(os.Getenv("TRIM_DATA_SHARE_PATHS"), os.Getenv("TRIM_DATA_ACCESSIBLE_PATHS"))
		if err == nil {
			instance, err = backupmodule.New(ctx, backupmodule.Options{
				DataDir: dataDir, PublicDir: definition.PublicDir, Prefix: definition.Prefix, LocalRoots: roots,
			})
		}
	case "ha500":
		manager.loadHA500Driver()
		instance, err = ha500module.New(ctx, ha500module.Options{
			SysRoot: "/sys", CPUInfoPath: "/proc/cpuinfo", Prefix: definition.Prefix,
			SettingsPath: filepath.Join(dataDir, "settings.json"), LogPath: filepath.Join(dataDir, "ha500_toolbox.log"),
			Version: definition.Version,
		})
	case "integrity":
		instance, err = integritymodule.New(integritymodule.Options{
			DataDir: dataDir, PublicDir: definition.PublicDir, Prefix: definition.Prefix,
			BrowseRoots: storageRoots(os.Getenv("INTEGRITY_ROOTS")),
		})
	case "media":
		instance, err = mediamodule.New(ctx, mediamodule.Options{
			DataDir: dataDir, PublicDir: definition.PublicDir, BundleDir: definition.BundleDir, Prefix: definition.Prefix,
		})
	case "music":
		instance, err = musicmodule.New(musicmodule.Options{
			DataDir: dataDir, PublicDir: definition.PublicDir, Prefix: definition.Prefix,
			BrowseRoots: musicBrowseRoots(os.Getenv("BROWSE_ROOTS")),
		})
	default:
		return os.ErrNotExist
	}
	if err != nil {
		return err
	}
	manager.runtimes[id] = &runtime{module: instance, startedAt: time.Now()}
	return nil
}

func (manager *Manager) stopLocked(id string) error {
	current := manager.runtimes[id]
	if current == nil {
		return nil
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	err := current.module.Close(shutdown)
	cancel()
	if err != nil {
		if manager.defs[id].RequiresHA500 {
			manager.ha500Failsafe()
		}
		return err
	}
	delete(manager.runtimes, id)
	return nil
}

func (manager *Manager) Route(id string) (Definition, http.Handler, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	definition, ok := manager.defs[id]
	if !ok {
		return Definition{}, nil, os.ErrNotExist
	}
	current := manager.runtimes[id]
	if !manager.state.Enabled[id] || current == nil {
		return definition, nil, errors.New("该模块当前未启用")
	}
	return definition, current.module.Handler(), nil
}

func (manager *Manager) moduleDataDir(id string) string {
	return filepath.Join(manager.dataDir, "modules", id)
}

func backupRoots(shared, accessible string) ([]string, error) {
	var roots []string
	seen := make(map[string]bool)
	add := func(path string) {
		if !seen[path] {
			roots = append(roots, path)
			seen[path] = true
		}
	}
	for _, path := range filepath.SplitList(shared) {
		if strings.TrimSpace(path) == "" {
			continue
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, fmt.Errorf("application shared directory unavailable: %w", err)
		}
		add(canonical)
	}
	for _, path := range filepath.SplitList(accessible) {
		if strings.TrimSpace(path) == "" {
			continue
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil || !filepath.IsAbs(canonical) || canonical == "/" {
			continue
		}
		info, err := os.Stat(canonical)
		if err != nil || !info.IsDir() {
			continue
		}
		add(canonical)
	}
	return roots, nil
}

func musicBrowseRoots(configured string) []string {
	if configured != "" {
		return existingDirectories(filepath.SplitList(configured))
	}
	matches, _ := filepath.Glob("/vol*")
	return existingDirectories(matches)
}

func storageRoots(configured string) []string {
	if configured != "" {
		return existingDirectories(filepath.SplitList(configured))
	}
	matches, _ := filepath.Glob("/vol*")
	return existingDirectories(matches)
}

func existingDirectories(paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		clean := filepath.Clean(path)
		if info, err := os.Stat(clean); err == nil && info.IsDir() {
			result = append(result, clean)
		}
	}
	return result
}

func (manager *Manager) loadHA500Driver() {
	for _, path := range []string{"/usr/sbin/modprobe", "/sbin/modprobe", "/usr/bin/modprobe", "/bin/modprobe"} {
		if info, err := os.Stat(path); err == nil && info.Mode()&0o111 != 0 {
			_ = exec.Command(path, "it87", "force_id=0x8620").Run()
			return
		}
	}
}

func (manager *Manager) ha500Failsafe() {
	dataDir := manager.moduleDataDir("ha500")
	_ = ha500module.ForceConfiguredFansFull("/sys", filepath.Join(dataDir, "settings.json"))
}
