// SPDX-License-Identifier: Apache-2.0
package security

// DescriptorUsage describes the process descriptor limit and current usage.
// Either side may be unknown on a platform that reports no such value.
type DescriptorUsage struct {
	// Limit is the soft open-file limit. Valid only when LimitKnown.
	Limit      uint64
	LimitKnown bool
	// Open is the number of descriptors currently open. Valid only when OpenKnown.
	Open      int
	OpenKnown bool
}

// CurrentDescriptorUsage reads the soft RLIMIT_NOFILE and counts the descriptors
// this process has open. It never fails: an unavailable value is reported as
// unknown so callers fall back to a conservative budget.
func CurrentDescriptorUsage() DescriptorUsage { return currentDescriptorUsage() }
