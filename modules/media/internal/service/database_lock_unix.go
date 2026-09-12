//go:build linux || darwin

package service

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

func lockDatabaseFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("应用数据目录已由另一个实例使用：%w", err)
	}
	return file, nil
}
