// SPDX-License-Identifier: Apache-2.0
//go:build windows

package cli

import (
	"errors"
	"golang.org/x/sys/windows"
	"syscall"
)

// Classify only address-in-use proof; other bind errors grant no owner evidence.
func listenerAddressInUse(err error) bool {
	return errors.Is(err, windows.WSAEADDRINUSE) || errors.Is(err, syscall.EADDRINUSE)
}
