//go:build !darwin

// SPDX-License-Identifier: Apache-2.0
package cli

// Windows Job ownership and Linux parent-death registration are native-owned.
// The private pipe additionally closes when the original parent exits.
func watchDesktopParent(parent int, stop func()) (func(), error) { return func() {}, nil }
