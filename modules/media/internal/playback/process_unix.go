//go:build linux || darwin

package playback

import (
	"os"
	"syscall"
)

func ownedDirectory(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.IsDir() && int(stat.Uid) == os.Geteuid() && info.Mode().Perm()&0077 == 0
}

func pauseProcess(process *os.Process, paused bool) error {
	signal := syscall.SIGCONT
	if paused {
		signal = syscall.SIGSTOP
	}
	return syscall.Kill(-process.Pid, signal)
}
