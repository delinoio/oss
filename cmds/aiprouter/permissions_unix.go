//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package main

import "os"

func (NativeProtector) Protect(path string, dir bool) error {
	mode := os.FileMode(0600)
	if dir {
		mode = 0700
	}
	return os.Chmod(path, mode)
}
