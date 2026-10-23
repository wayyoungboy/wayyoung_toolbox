//go:build !linux && !darwin

package catalog

import "os"

// Unsupported platforms fail closed rather than silently weakening root checks.
func directoryIdentity(os.FileInfo) (string, error) { return "", ErrUnavailable }
