//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"syscall"
	"testing"
)

// lowerDescriptorSoftLimit sets the soft RLIMIT_NOFILE of this process, skipping
// the test when the host does not allow it.
func lowerDescriptorSoftLimit(t *testing.T, soft uint64) {
	t.Helper()
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	if uint64(limit.Max) < soft {
		t.Skip("hard descriptor limit is below the fixture limit")
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &syscall.Rlimit{Cur: soft, Max: limit.Max}); err != nil {
		t.Skip("cannot lower the descriptor soft limit", err)
	}
}
