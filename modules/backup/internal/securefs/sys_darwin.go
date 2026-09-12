package securefs

import (
	"golang.org/x/sys/unix"
	"strings"
)

// macOS supports local development/tests, not production mount validation.
func openAbsoluteRoot(name string) (int, error) {
	return unix.Open(name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
}

func openBeneath(fd int, name string, flags int, mode uint32) (int, error) {
	parts := strings.Split(name, "/")
	current, err := unix.Dup(fd)
	if err != nil {
		return -1, err
	}
	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat(current, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(current)
		if err != nil {
			return -1, err
		}
		current = next
	}
	defer unix.Close(current)
	return unix.Openat(current, parts[len(parts)-1], flags, mode)
}

func renameNew(oldFD int, old string, newFD int, name string) error {
	return unix.RenameatxNp(oldFD, old, newFD, name, unix.RENAME_EXCL)
}
