//go:build windows

// SPDX-License-Identifier: Apache-2.0
package worker

import "testing"

// Windows has no RLIMIT_NOFILE; the subprocess scenario never runs there.
func lowerDescriptorSoftLimit(t *testing.T, _ uint64) {
	t.Helper()
	t.Skip("RLIMIT_NOFILE does not exist on Windows")
}
