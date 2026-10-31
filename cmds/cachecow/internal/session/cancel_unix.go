// SPDX-License-Identifier: Apache-2.0
//go:build linux || darwin

package session

import "syscall"

func cancelOwner(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
