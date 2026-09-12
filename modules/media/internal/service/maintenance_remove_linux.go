//go:build linux

package service

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// safeRemoveEmptyDirectory walks from an already canonical root with openat(2),
// refusing symlinks at every level. The final unlink is relative to the verified
// parent directory descriptor so a renamed ancestor cannot redirect deletion.
func safeRemoveEmptyDirectory(root, candidate string, expected os.FileInfo) (bool, error) {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false, nil
	}
	parts := strings.Split(relative, string(filepath.Separator))
	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return false, err
	}
	defer unix.Close(rootFD)
	parentFD := rootFD
	for _, part := range parts[:len(parts)-1] {
		nextFD, openErr := unix.Openat(parentFD, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if parentFD != rootFD {
			_ = unix.Close(parentFD)
		}
		if openErr != nil {
			return false, nil
		}
		parentFD = nextFD
	}
	if parentFD != rootFD {
		defer unix.Close(parentFD)
	}
	name := parts[len(parts)-1]
	childFD, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return false, nil
	}
	child := os.NewFile(uintptr(childFD), candidate)
	if child == nil {
		_ = unix.Close(childFD)
		return false, errors.New("无法检查空目录")
	}
	defer child.Close()
	currentInfo, err := child.Stat()
	if err != nil || !os.SameFile(expected, currentInfo) {
		return false, err
	}
	var opened unix.Stat_t
	if err := unix.Fstat(childFD, &opened); err != nil {
		return false, err
	}
	if names, readErr := child.Readdirnames(1); len(names) != 0 || (readErr != nil && !errors.Is(readErr, io.EOF)) {
		return false, readErr
	}
	var current unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &current, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return false, nil
	}
	if current.Dev != opened.Dev || current.Ino != opened.Ino || current.Mode&unix.S_IFMT != unix.S_IFDIR {
		return false, nil
	}
	if err := unix.Unlinkat(parentFD, name, unix.AT_REMOVEDIR); err != nil {
		return false, err
	}
	return true, nil
}
