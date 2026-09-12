//go:build windows

package browsercookies

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func windowsChromeDecryptor(localStatePath string) (func([]byte) (string, error), error) {
	data, err := os.ReadFile(localStatePath)
	if err != nil {
		return nil, err
	}
	var state struct {
		OSCrypt struct {
			EncryptedKey string `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.OSCrypt.EncryptedKey == "" {
		return nil, errors.New("chromium local state has no encrypted key")
	}
	wrappedKey, err := base64.StdEncoding.DecodeString(state.OSCrypt.EncryptedKey)
	if err != nil {
		return nil, err
	}
	wrappedKey = bytes.TrimPrefix(wrappedKey, []byte("DPAPI"))
	key, err := dpapiDecrypt(wrappedKey)
	if err != nil {
		return nil, err
	}
	return func(encrypted []byte) (string, error) {
		if bytes.HasPrefix(encrypted, []byte("v10")) || bytes.HasPrefix(encrypted, []byte("v11")) {
			return chromeGCMDecrypt(key, encrypted)
		}
		plain, err := dpapiDecrypt(encrypted)
		if err != nil {
			return "", err
		}
		return string(plain), nil
	}, nil
}

func chromeGCMDecrypt(key []byte, encrypted []byte) (string, error) {
	if len(encrypted) < 3+12+16 {
		return "", errors.New("encrypted cookie is too short")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := encrypted[3 : 3+gcm.NonceSize()]
	ciphertext := encrypted[3+gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func dpapiDecrypt(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty DPAPI payload")
	}
	in := windows.DataBlob{
		Size: uint32(len(data)),
		Data: &data[0],
	}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	plain := unsafe.Slice(out.Data, out.Size)
	return append([]byte(nil), plain...), nil
}
