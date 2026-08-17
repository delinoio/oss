package idgen

import (
	"crypto/rand"
	"encoding/base64"
)

const OpaqueIDBytes = 32

func Opaque() (string, error) {
	value := make([]byte, OpaqueIDBytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
