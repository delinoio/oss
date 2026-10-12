// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package security

// CaptureNativeIdentity dispatches to the platform's no-follow metadata path.
func CaptureNativeIdentity(path string) (NativeIdentity, error) {
	return captureUnixNativeIdentity(path)
}
