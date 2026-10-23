//go:build darwin

package catalog

import "golang.org/x/sys/unix"

// Local test platform only. Destination is inside a verified empty, service-
// owned 0700 directory; production fnOS uses Linux RENAME_NOREPLACE.
func captureRename(from int, name string, to int, destination string) error {
	return unix.Renameat(from, name, to, destination)
}
