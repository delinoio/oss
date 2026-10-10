//go:build windows

// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"syscall"
	"testing"
)

func TestWindowsListenerAddressInUseKeepsExactNativeClassification(t *testing.T) {
	for _, err := range []error{windows.WSAEADDRINUSE, fmt.Errorf("wrapped: %w", windows.WSAEADDRINUSE), syscall.EADDRINUSE} {
		if !listenerAddressInUse(err) {
			t.Fatal("original occupied socket lost classification")
		}
	}
	for _, err := range []error{nil, windows.WSAEACCES, windows.WSAEINVAL, errors.New("address already in use")} {
		if listenerAddressInUse(err) {
			t.Fatal("unproven bind error acquired owner authority")
		}
	}
}
