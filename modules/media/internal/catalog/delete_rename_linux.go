//go:build linux

package catalog

import "golang.org/x/sys/unix"

func captureRename(from int, name string, to int, destination string) error {
	return unix.Renameat2(from, name, to, destination, unix.RENAME_NOREPLACE)
}
