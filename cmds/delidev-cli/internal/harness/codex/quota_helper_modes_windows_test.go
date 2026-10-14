//go:build windows

// SPDX-License-Identifier: Apache-2.0
package codex

// Windows creates no Unix helper aliases, so no quota helper test runs there and
// the fixture process has no helper entries to mutate at stdin EOF.
func mutateQuotaHelperFixtureAtEOF(string) {}
