package logstore

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	MaxItems     = 1000
	MaxLineBytes = 4096
)

type Item struct {
	Timestamp string
	Level     string
	Message   string
}

type File struct {
	Name string
	Size int64
}

type Snapshot struct {
	Items       []Item
	Files       []File
	Limit       int
	MaxBytes    int64
	BackupCount int
}

type Store struct {
	mu      sync.Mutex
	path    string
	max     int64
	backups int
	file    *os.File
}

func New(path string, maxBytes int64, backupCount int) (*Store, error) {
	if maxBytes <= 0 || backupCount < 0 || backupCount > 10 {
		return nil, errors.New("invalid log retention settings")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &Store{path: path, max: maxBytes, backups: backupCount, file: file}, nil
}

func (store *Store) Write(data []byte) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.file == nil {
		return 0, os.ErrClosed
	}
	info, err := store.file.Stat()
	if err != nil {
		return 0, err
	}
	if info.Size() > 0 && info.Size()+int64(len(data)) > store.max {
		if err := store.rotateLocked(); err != nil {
			return 0, err
		}
	}
	return store.file.Write(data)
}

func (store *Store) rotateLocked() error {
	if err := store.file.Close(); err != nil {
		return err
	}
	store.file = nil
	if store.backups > 0 {
		for index := store.backups; index >= 1; index-- {
			destination := fmt.Sprintf("%s.%d", store.path, index)
			if index == store.backups {
				_ = os.Remove(destination)
			}
			source := store.path
			if index > 1 {
				source = fmt.Sprintf("%s.%d", store.path, index-1)
			}
			if err := os.Rename(source, destination); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	} else if err := os.Remove(store.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(store.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	store.file = file
	return store.file.Chmod(0o600)
}

func (store *Store) Read(limit int) (Snapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	limit = min(MaxItems, max(1, limit))
	items := make([]Item, 0, limit)
	files := make([]File, 0, store.backups+1)
	for index := store.backups; index >= 0; index-- {
		path := store.path
		if index > 0 {
			path = fmt.Sprintf("%s.%d", store.path, index)
		}
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return Snapshot{}, err
		}
		files = append(files, File{Name: filepath.Base(path), Size: info.Size()})
		file, err := os.Open(path)
		if err != nil {
			return Snapshot{}, err
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if len(line) > MaxLineBytes {
				line = line[:MaxLineBytes]
			}
			items = append(items, parseLine(line))
			if len(items) > limit {
				copy(items, items[len(items)-limit:])
				items = items[:limit]
			}
		}
		scanErr := scanner.Err()
		closeErr := file.Close()
		if err := errors.Join(scanErr, closeErr); err != nil {
			return Snapshot{}, err
		}
	}
	return Snapshot{Items: items, Files: files, Limit: limit, MaxBytes: store.max, BackupCount: store.backups}, nil
}

func parseLine(line string) Item {
	item := Item{Level: "INFO", Message: line}
	fields := strings.Fields(line)
	for _, field := range fields {
		switch {
		case strings.HasPrefix(field, "time="):
			item.Timestamp = strings.Trim(strings.TrimPrefix(field, "time="), `"`)
		case strings.HasPrefix(field, "level="):
			item.Level = strings.Trim(strings.TrimPrefix(field, "level="), `"`)
		}
	}
	if len(fields) > 1 && !strings.Contains(line, "time=") {
		item.Timestamp = fields[0]
		if len(fields) > 1 && (fields[1] == "INFO" || fields[1] == "WARN" || fields[1] == "WARNING" || fields[1] == "ERROR") {
			item.Level = strings.ReplaceAll(fields[1], "WARN", "WARNING")
		}
	}
	return item
}

func (store *Store) Clear() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.file == nil {
		return os.ErrClosed
	}
	if err := store.file.Sync(); err != nil {
		return err
	}
	if err := store.file.Truncate(0); err != nil {
		return err
	}
	if _, err := store.file.Seek(0, 2); err != nil {
		return err
	}
	for index := 1; index <= store.backups; index++ {
		if err := os.Remove(fmt.Sprintf("%s.%d", store.path, index)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return store.file.Chmod(0o600)
}

func (store *Store) Close() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.file == nil {
		return nil
	}
	err := store.file.Close()
	store.file = nil
	return err
}
