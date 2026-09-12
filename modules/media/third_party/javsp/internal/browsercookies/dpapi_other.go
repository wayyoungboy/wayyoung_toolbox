//go:build !windows

package browsercookies

import "errors"

func windowsChromeDecryptor(string) (func([]byte) (string, error), error) {
	return nil, errors.New("windows cookie decryptor is unavailable on this platform")
}
