//go:build !linux && !darwin

package catalog

import "os"

func removeCapturedFile(_ *os.Root, _, _ string) error { return ErrUnavailable }
