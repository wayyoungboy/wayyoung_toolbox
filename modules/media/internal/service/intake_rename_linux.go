package service

import (
	"golang.org/x/sys/unix"
	"os"
)

func renameIntakeAtNoReplace(source *os.File, old string, destination *os.File, next string) error {
	return unix.Renameat2(int(source.Fd()), old, int(destination.Fd()), next, unix.RENAME_NOREPLACE)
}
