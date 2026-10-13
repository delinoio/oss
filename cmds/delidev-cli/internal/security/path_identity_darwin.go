//go:build darwin

// SPDX-License-Identifier: Apache-2.0
package security

import "golang.org/x/sys/unix"

func birthTimeNS(_ int, st *unix.Stat_t) int64 { return st.Btim.Nano() }
