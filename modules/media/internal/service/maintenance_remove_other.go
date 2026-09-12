//go:build !linux

package service

import (
	"os"
	"path/filepath"
)

func safeRemoveEmptyDirectory(root, candidate string, expected, expectedParent os.FileInfo) (bool, os.FileInfo, error) {
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil || resolved != candidate || !pathWithinRoot(root, resolved) {
		return false, nil, nil
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(expected, info) {
		return false, nil, nil
	}
	parent := filepath.Dir(resolved)
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || !os.SameFile(expectedParent, parentInfo) {
		return false, nil, nil
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return false, nil, err
	}
	if len(entries) != 0 {
		return false, nil, nil
	}
	if err := os.Remove(resolved); err != nil {
		return false, nil, err
	}
	parentInfo, err = os.Lstat(parent)
	return true, parentInfo, err
}
