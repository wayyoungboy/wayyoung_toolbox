package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/control"
)

type Settings struct {
	BayPortMap   [5]int            `json:"bay_port_map"`
	FanPolicies  control.PolicySet `json:"fan_policies"`
	VerifiedFans map[int]bool      `json:"verified_fans,omitempty"`
}

type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (store *Store) Load() (Settings, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	data, err := os.ReadFile(store.path)
	if err != nil {
		return Settings{}, err
	}
	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return Settings{}, fmt.Errorf("decode settings: %w", err)
	}
	if _, err := control.ValidateBayPortMap(settings.BayPortMap[:]); err != nil {
		return Settings{}, fmt.Errorf("invalid saved bay map: %w", err)
	}
	return settings, nil
}

func (store *Store) LoadOrDefault() (Settings, error) {
	settings, err := store.Load()
	if errors.Is(err, os.ErrNotExist) {
		return Settings{BayPortMap: [5]int{1, 2, 3, 4, 5}, FanPolicies: make(control.PolicySet), VerifiedFans: make(map[int]bool)}, nil
	}
	return settings, err
}

func (store *Store) Save(settings Settings) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, err := control.ValidateBayPortMap(settings.BayPortMap[:]); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	directory := filepath.Dir(store.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".settings-*")
	if err != nil {
		return fmt.Errorf("create temporary settings: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, store.path); err != nil {
		return fmt.Errorf("replace settings: %w", err)
	}
	committed = true
	directoryHandle, err := os.Open(directory)
	if err == nil {
		err = errors.Join(directoryHandle.Sync(), directoryHandle.Close())
	}
	return err
}
