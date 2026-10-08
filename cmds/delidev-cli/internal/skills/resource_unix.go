// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package skills

import "syscall"

// Nonblocking opens prevent replaced special files from hanging package reads.
func resourceOpenFlags() int { return syscall.O_NONBLOCK }
