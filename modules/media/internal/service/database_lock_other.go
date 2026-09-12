//go:build !linux && !darwin

package service

import (
	"fmt"
	"os"
)

func lockDatabaseFile(string) (*os.File, error) {
	return nil, fmt.Errorf("当前系统不支持应用数据目录独占锁")
}
