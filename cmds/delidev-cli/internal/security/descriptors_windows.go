//go:build windows

// SPDX-License-Identifier: Apache-2.0
package security

// Windows handles are not bounded by a per-process soft limit comparable to
// RLIMIT_NOFILE, so neither value is reported.
func currentDescriptorUsage() DescriptorUsage { return DescriptorUsage{} }
