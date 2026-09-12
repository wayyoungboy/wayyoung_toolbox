//go:build !linux && !darwin

package service

import (
	"errors"
	"os"
)

func linkMovieNoReplace(_ *os.Root, _ string, _ *os.Root, _ string) error {
	return errors.New("当前平台不支持无覆盖跨目录硬链接")
}
