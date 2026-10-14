// SPDX-License-Identifier: Apache-2.0
package codex

import "strings"

// Access-only quota fixture modes. They create the same native-style helper
// aliases as the managed Thread helper modes right after initialization.
// The fixture process dispatch in client_test.go references these on every
// platform, so they stay outside the Unix-only quota helper tests.
const (
	quotaHelperPrefix       = "quota-helpers-"
	quotaHelperReady        = quotaHelperPrefix + "ready"
	quotaHelperReadFail     = quotaHelperPrefix + "readfail"
	quotaHelperForeignStore = quotaHelperPrefix + "foreign-store"
	quotaHelperStoreTamper  = quotaHelperForeignStore + "-tamper"
	quotaHelperEOFTamper    = quotaHelperPrefix + "eof-tamper"
	quotaHelperEOFRename    = quotaHelperPrefix + "eof-rename"
	quotaHelperEOFHardLink  = quotaHelperPrefix + "eof-hardlink"
	quotaHelperEOFForeign   = quotaHelperPrefix + "eof-foreign"
	quotaHelperReadyHang    = quotaHelperReady + "-hang"
)

func isHelperFixtureMode(mode string) bool {
	return strings.HasPrefix(mode, helperFixturePrefix) || strings.HasPrefix(mode, quotaHelperPrefix)
}
