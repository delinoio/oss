// SPDX-License-Identifier: Apache-2.0
//go:build !windows && !darwin

package workspace

func removeVerifiedFinalRoot(path, quarantine string, expectedIdentity string, beforeUnlink, afterIdentityCheck func() error) error {
	return removeVerifiedFinalRootStandard(path, quarantine, expectedIdentity, beforeUnlink, afterIdentityCheck)
}
