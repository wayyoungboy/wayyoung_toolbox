//go:build linux || darwin

package catalog

import (
	"fmt"
	"os"
	"syscall"
)

func directoryIdentity(info os.FileInfo) (string, error) {
	value, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ErrUnavailable
	}
	return fmt.Sprintf("%d:%d", value.Dev, value.Ino), nil
}
