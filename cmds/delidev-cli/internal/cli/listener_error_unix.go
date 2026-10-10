// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package cli

import (
	"errors"
	"syscall"
)

// Classify only address-in-use proof; other bind errors grant no owner evidence.
func listenerAddressInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}
