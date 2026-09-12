//go:build !linux

package service

import (
	"os"
	"path/filepath"
)

func safeRemoveEmptyDirectory(root, candidate string, expected os.FileInfo) (bool, error) {
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil || resolved != candidate || !pathWithinRoot(root, resolved) {
		return false, nil
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(expected, info) {
		return false, nil
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return false, err
	}
	if len(entries) != 0 {
		return false, nil
	}
	if err := os.Remove(resolved); err != nil {
		return false, err
	}
	return true, nil
}
