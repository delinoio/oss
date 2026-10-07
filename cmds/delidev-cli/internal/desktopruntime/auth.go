// SPDX-License-Identifier: Apache-2.0
package desktopruntime

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
)

const authPrefix = "Bearer runtime-v2."

func authCipher(t Target) (cipher.AEAD, error) {
	raw, err := base64.RawURLEncoding.DecodeString(t.Key)
	if err != nil {
		return nil, unavailable()
	}
	mac := hmac.New(sha256.New, raw)
	mac.Write([]byte("delidev-desktop-auth-v2"))
	key := mac.Sum(nil)
	clear(raw)
	block, err := aes.NewCipher(key)
	clear(key)
	if err != nil {
		return nil, unavailable()
	}
	return cipher.NewGCM(block)
}
func authAAD(t Target, r *http.Request) []byte {
	path := r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	return []byte("delidev-desktop-auth-v2\n" + string(t.Generation) + "\n" + string(t.ServerID) + "\n" + r.Method + "\n" + path)
}

// Protect the original bearer even if the checked listener disappears between
// proof and RPC, and the HTTP stack reconnects to a different port occupant.
// The private listener unwraps it before the existing bearer middleware.
func wrapBearer(t Target, r *http.Request) error {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return nil
	}
	if !strings.HasPrefix(auth, "Bearer ") || len(auth) > 256 {
		return unavailable()
	}
	aead, err := authCipher(t)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return unavailable()
	}
	plain := []byte(auth)
	sealed := aead.Seal(nil, nonce, plain, authAAD(t, r))
	clear(plain)
	r.Header.Set("Authorization", authPrefix+string(t.Generation)+"."+base64.RawURLEncoding.EncodeToString(nonce)+"."+base64.RawURLEncoding.EncodeToString(sealed))
	return nil
}
func unwrapBearer(t Target, r *http.Request) (*http.Request, error) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, authPrefix) {
		return r, nil
	}
	if len(r.Header.Values("Authorization")) != 1 || len(auth) > 1024 {
		return nil, unavailable()
	}
	parts := strings.Split(strings.TrimPrefix(auth, authPrefix), ".")
	if len(parts) != 3 || parts[0] != string(t.Generation) {
		return nil, unavailable()
	}
	nonce, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, unavailable()
	}
	sealed, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, unavailable()
	}
	aead, err := authCipher(t)
	if err != nil || len(nonce) != aead.NonceSize() {
		return nil, unavailable()
	}
	plain, err := aead.Open(nil, nonce, sealed, authAAD(t, r))
	if err != nil {
		return nil, unavailable()
	}
	defer clear(plain)
	if !strings.HasPrefix(string(plain), "Bearer ") || len(plain) > 256 {
		return nil, unavailable()
	}
	copy := r.Clone(r.Context())
	copy.Header.Set("Authorization", string(plain))
	return copy, nil
}
