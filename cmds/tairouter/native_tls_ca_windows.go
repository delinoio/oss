// SPDX-License-Identifier: Apache-2.0
package main

import (
	"errors"
	"golang.org/x/sys/windows"
	"unsafe"
)

func nativePlatformKeyProtection(platform string) (NativeKeyProtection, error) {
	if platform != "windows" {
		return nil, errors.New("native platform key protection is unavailable; no plaintext fallback")
	}
	return nativeDPAPIProtection{}, nil
}

type nativeDPAPIProtection struct{}

func (nativeDPAPIProtection) Protect(b []byte) ([]byte, error)   { return nativeDPAPI(b, true) }
func (nativeDPAPIProtection) Unprotect(b []byte) ([]byte, error) { return nativeDPAPI(b, false) }
func nativeDPAPI(b []byte, protect bool) ([]byte, error) {
	if len(b) == 0 || len(b) > MaxConfigBytes {
		return nil, errors.New("invalid protected native key size")
	}
	input := windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	var output windows.DataBlob
	var err error
	// UI forbidden, with no LOCAL_MACHINE flag: only the current Windows user can decrypt.
	if protect {
		err = windows.CryptProtectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output)
	} else {
		err = windows.CryptUnprotectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output)
	}
	if err != nil {
		return nil, errors.New("Windows current-user DPAPI failed")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	view := unsafe.Slice(output.Data, int(output.Size))
	result := append([]byte(nil), view...)
	clear(view)
	return result, nil
}

// MoveFileEx WRITE_THROUGH provides the publication durability Windows directories lack.
func nativeSyncDirectory(path string) error { return nil }

func nativePublishFile(from, to string) error {
	source, e := windows.UTF16PtrFromString(from)
	if e != nil {
		return e
	}
	target, e := windows.UTF16PtrFromString(to)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(source, target, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
