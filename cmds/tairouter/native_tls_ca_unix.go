//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package main

import (
	"errors"
	"os"
	"runtime"
)

func nativePlatformKeyProtection(platform string) (NativeKeyProtection, error) {
	if platform != "darwin" || runtime.GOOS != "darwin" {
		return nil, errors.New("native platform key protection is unavailable; no plaintext fallback")
	}
	return nativePermissionKeyProtection{}, nil
}
func nativeSyncDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

func nativePublishFile(from, to string) error { return os.Rename(from, to) }
