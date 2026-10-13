//go:build !windows && !linux && !darwin

// SPDX-License-Identifier: Apache-2.0
package security

import "golang.org/x/sys/unix"

// Other Unix platforms report no portable birth time here, so only device,
// inode, size and modification time bind an identity across restarts.
func birthTimeNS(int, *unix.Stat_t) int64 { return 0 }
