// SPDX-License-Identifier: Apache-2.0
package main

import (
	"errors"
	keyring "github.com/zalando/go-keyring"
)

// NativeCredentials uses Keychain, Secret Service or Windows Credential Manager.
// This adapter deliberately has no file-backed fallback.
type NativeCredentials struct{}

func (NativeCredentials) Set(id, value string) error {
	if keyring.Set("tairouter", id, value) != nil {
		return ErrUnavailable
	}
	return nil
}
func (NativeCredentials) Get(id string) (string, error) {
	v, e := keyring.Get("tairouter", id)
	if e != nil {
		return "", ErrUnavailable
	}
	return v, nil
}
func (NativeCredentials) Delete(id string) error {
	e := keyring.Delete("tairouter", id)
	if e != nil && !errors.Is(e, keyring.ErrNotFound) {
		return ErrUnavailable
	}
	return nil
}
