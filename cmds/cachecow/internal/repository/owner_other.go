//go:build !linux && !darwin && !windows

// SPDX-License-Identifier: Apache-2.0
package repository

import "errors"

func CheckOwner(string) error {
	return errors.New("current-user ownership verification is unavailable on this platform")
}
