//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package security

import (
	"os"

	"golang.org/x/sys/unix"
)

// unlimitedDescriptors is the soft limit at and above which the process is
// treated as unconstrained.
const unlimitedDescriptors = 1 << 40

func currentDescriptorUsage() DescriptorUsage {
	usage := DescriptorUsage{}
	var limit unix.Rlimit
	if e := unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); e == nil && uint64(limit.Cur) < unlimitedDescriptors {
		usage.Limit, usage.LimitKnown = uint64(limit.Cur), true
	}
	for _, directory := range []string{"/proc/self/fd", "/dev/fd"} {
		f, e := os.Open(directory)
		if e != nil {
			continue
		}
		names, e := f.Readdirnames(-1)
		f.Close()
		if e != nil || len(names) == 0 {
			continue
		}
		// The listing descriptor itself is open while it is read.
		usage.Open, usage.OpenKnown = len(names)-1, true
		break
	}
	return usage
}
