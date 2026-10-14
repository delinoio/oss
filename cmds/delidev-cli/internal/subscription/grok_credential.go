// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"io"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// GrokAuthFileName is the native credential file below $GROK_HOME.
const GrokAuthFileName = "auth.json"

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

// WriteGrokAuthFile materializes the bundle as $GROK_HOME/auth.json for the
// native child. The home must be a private directory; the file is published
// atomically with owner-only permission, and remnants of an interrupted earlier
// write are retired first. On failure the target is retired so no partial
// credential file is left behind.
func WriteGrokAuthFile(grokHome string, bundle GrokBundle) error {
	if err := security.CheckPrivateDir(grokHome); err != nil {
		return GrokBundleInvalid()
	}
	raw, err := bundle.AuthFile()
	if err != nil {
		return err
	}
	defer clear(raw)
	if err := WriteCredentialFile(filepath.Join(grokHome, GrokAuthFileName), raw); err != nil {
		_ = RetireGrokAuthFile(grokHome)
		return GrokBundleInvalid()
	}
	return nil
}

// ReadGrokAuthFile reads and validates the native $GROK_HOME/auth.json after a
// login or refresh. The entry must be a regular, bounded, non-linked file.
func ReadGrokAuthFile(grokHome string) (GrokBundle, error) {
	if err := security.CheckPrivateDir(grokHome); err != nil {
		return GrokBundle{}, GrokBundleInvalid()
	}
	path := filepath.Join(grokHome, GrokAuthFileName)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxGrokAuthFile {
		return GrokBundle{}, GrokBundleInvalid()
	}
	file, err := os.Open(path)
	if err != nil {
		return GrokBundle{}, GrokBundleInvalid()
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return GrokBundle{}, GrokBundleInvalid()
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaxGrokAuthFile+1))
	defer clear(raw)
	if err != nil {
		return GrokBundle{}, GrokBundleInvalid()
	}
	return ParseGrokAuthFile(raw)
}

// GrokAuthFilePresent reports whether the credential file or an interrupted
// write's target-attributed temporary remains in grokHome.
func GrokAuthFilePresent(grokHome string) (bool, error) {
	temporaries, _, err := credentialTemporaries(grokHome, GrokAuthFileName)
	if err != nil {
		return true, err
	}
	if len(temporaries) != 0 {
		return true, nil
	}
	info, err := os.Lstat(filepath.Join(grokHome, GrokAuthFileName))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return true, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupFilesystem}
	}
	if !info.Mode().IsRegular() {
		return true, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupNotRegular}
	}
	return true, nil
}

// RetireGrokAuthFile removes only the credential file and its attributed
// temporaries. The caller has already joined the native process and its
// descendants; the native lock and every other file are left to runtime cleanup.
func RetireGrokAuthFile(grokHome string) error {
	return retireCredentialFile(grokHome, GrokAuthFileName, security.SyncParent)
}

// SealGrokFinish reads the native credential file and seals it as the Finish
// bundle for the next generation. When expected is non-zero the file must still
// belong to the original bound identity.
func SealGrokFinish(grokHome string, server, account, generation domain.ID, expected GrokBundle) ([]byte, GrokBundle, error) {
	bundle, err := ReadGrokAuthFile(grokHome)
	if err != nil {
		return nil, GrokBundle{}, err
	}
	if expected.identity.UserID != "" && bundle.IdentityKey() != expected.IdentityKey() {
		bundle.Clear()
		return nil, GrokBundle{}, GrokBundleInvalid()
	}
	sealed, err := SealGrokBundle(server, account, generation, bundle)
	if err != nil {
		bundle.Clear()
		return nil, GrokBundle{}, err
	}
	return sealed, bundle, nil
}
