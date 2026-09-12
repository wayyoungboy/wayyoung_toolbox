//go:build linux || darwin

package service

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Hold both parent directories through Linkat, so separate selected roots do
// not require widening filesystem access to their common ancestor. Linkat
// without AT_SYMLINK_FOLLOW never follows the leaf and never replaces a target.
func linkMovieNoReplace(source *os.Root, old string, destination *os.Root, next string) error {
	if err := rejectIntakeLinks(source, old, false); err != nil {
		return err
	}
	if err := rejectIntakeLinks(destination, filepath.Dir(next), false); err != nil {
		return err
	}
	src, err := source.Open(filepath.Dir(old))
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := destination.Open(filepath.Dir(next))
	if err != nil {
		return err
	}
	defer dst.Close()
	return unix.Linkat(int(src.Fd()), filepath.Base(old), int(dst.Fd()), filepath.Base(next), 0)
}
