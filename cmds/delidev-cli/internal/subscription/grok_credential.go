// SPDX-License-Identifier: Apache-2.0
package subscription

// GrokAuthFileName is the native credential file below $GROK_HOME.
const GrokAuthFileName = "auth.json"

// GrokLockFileName is the lock file the native CLI keeps beside GrokAuthFileName.
// It holds no credential, but it is retired with the credential file and an
// unremoved one keeps a profile from being reported clean.
const GrokLockFileName = GrokAuthFileName + ".lock"

// GrokRefreshed proves that a native refresh produced a distinct credential for
// the same bound identity: the identity key is unchanged, the expiry moved
// forward and the access key or refresh token changed. It never reveals values.
func GrokRefreshed(before, after GrokBundle) error {
	if before.identity.UserID == "" || after.IdentityKey() != before.IdentityKey() || !after.identity.Expiry.After(before.identity.Expiry) ||
		before.entry.Key == after.entry.Key && before.entry.RefreshToken == after.entry.RefreshToken {
		return GrokBundleInvalid()
	}
	return nil
}

// Equal reports whether two bundles hold byte-identical credential material.
func (b GrokBundle) Equal(other GrokBundle) bool {
	return b.entry.Key == other.entry.Key && b.entry.RefreshToken == other.entry.RefreshToken &&
		b.IdentityKey() == other.IdentityKey() && b.identity.Expiry.Equal(other.identity.Expiry)
}
