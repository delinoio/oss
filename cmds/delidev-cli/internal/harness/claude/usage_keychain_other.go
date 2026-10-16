//go:build !darwin

// SPDX-License-Identifier: Apache-2.0
package claude

// SystemUsageKeychain has no native secure-storage adapter outside macOS. The
// profile credential file is the only supported storage on Linux and Windows.
func SystemUsageKeychain() UsageKeychain { return nil }
