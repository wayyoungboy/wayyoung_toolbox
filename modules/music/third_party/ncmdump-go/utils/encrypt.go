package utils

import (
	"crypto/aes"
)

func AesEcbDecrypt(key []byte, src []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	dst := make([]byte, 0, len(src))
	tmp := make([]byte, aes.BlockSize)

	for i := 0; i < len(src); i += aes.BlockSize {
		block.Decrypt(tmp, src[i:i+aes.BlockSize])

		if i == len(src)-aes.BlockSize {
			pad := int(tmp[len(tmp)-1])
			if pad > aes.BlockSize {
				pad = 0
			}
			dst = append(dst, tmp[:aes.BlockSize-pad]...)
		} else {
			dst = append(dst, tmp...)
		}
	}

	return dst, nil
}
