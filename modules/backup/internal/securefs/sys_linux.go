package securefs

import "golang.org/x/sys/unix"

func openAbsoluteRoot(name string) (int, error) {
	return unix.Openat2(unix.AT_FDCWD, name, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW,
		Resolve: unix.RESOLVE_NO_SYMLINKS,
	})
}

func openBeneath(fd int, name string, flags int, mode uint32) (int, error) {
	return unix.Openat2(fd, name, &unix.OpenHow{
		Flags: uint64(flags), Mode: uint64(mode),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV,
	})
}

func renameNew(oldFD int, old string, newFD int, name string) error {
	return unix.Renameat2(oldFD, old, newFD, name, unix.RENAME_NOREPLACE)
}
