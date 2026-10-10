// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package cli

import (
	"errors"
	"fmt"
	"syscall"
	"testing"
)

func TestUnixListenerAddressInUseKeepsExactNativeClassification(t *testing.T) {
	for _, err := range []error{syscall.EADDRINUSE, fmt.Errorf("wrapped: %w", syscall.EADDRINUSE)} {
		if !listenerAddressInUse(err) {
			t.Fatal("original occupied socket lost classification")
		}
	}
	for _, err := range []error{nil, syscall.EACCES, syscall.EINVAL, errors.New("address already in use")} {
		if listenerAddressInUse(err) {
			t.Fatal("unproven bind error acquired owner authority")
		}
	}
}
