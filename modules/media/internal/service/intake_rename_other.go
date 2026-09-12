//go:build !linux && !darwin

package service

import (
	"errors"
	"os"
)

func renameIntakeAtNoReplace(_ *os.File, _ string, _ *os.File, _ string) error {
	return errors.New("当前平台不支持无覆盖原子搬运")
}
