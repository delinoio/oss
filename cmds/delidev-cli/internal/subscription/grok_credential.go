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

// CheckGrokHome requires grokHome to be a real, private, non-linked directory.
// Every credential read or write below it, and a bundle-less login that lets the
// native child create auth.json itself, depends on this check.
func CheckGrokHome(grokHome string) error {
	if err := security.CheckPrivateDir(grokHome); err != nil {
		return GrokBundleInvalid()
	}
	return nil
}

// GrokHome is a Grok home bound to the directory it named when the credential
// was materialized. Every later credential operation is path based, so a home
// that was renamed away or replaced would make a path lookup find nothing (an
// absent parent reads as clean) or find someone else's directory. Verify proves
// the path still names the original directory, and a caller brackets
// retirement and the final absence check with it.
type GrokHome struct {
	path     string
	identity os.FileInfo
}

// BindGrokHome validates grokHome (see CheckGrokHome) and records the identity
// of the directory it names.
func BindGrokHome(grokHome string) (GrokHome, error) {
	if err := CheckGrokHome(grokHome); err != nil {
		return GrokHome{}, err
	}
	identity, err := os.Lstat(grokHome)
	if err != nil {
		return GrokHome{}, GrokBundleInvalid()
	}
	// On Windows the file identity is read lazily from the path on first use, so
	// it is forced now; later a replaced path could otherwise be recorded as the
	// original.
	if !os.SameFile(identity, identity) {
		return GrokHome{}, GrokBundleInvalid()
	}
	return GrokHome{path: grokHome, identity: identity}, nil
}

// Path is the path the home was bound at.
func (h GrokHome) Path() string { return h.path }

// Verify reports an identity failure unless the bound path is still the same
// private, non-linked directory it named at binding. The directory object stays
// alive while it exists, so an equal identity cannot belong to a successor.
func (h GrokHome) Verify() error {
	if h.identity == nil {
		return &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupIdentity}
	}
	if err := CheckGrokHome(h.path); err != nil {
		return &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupFilesystem}
	}
	current, err := os.Lstat(h.path)
	if err != nil {
		return &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupFilesystem}
	}
	if !os.SameFile(h.identity, current) {
		return &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupIdentity}
	}
	return nil
}

// WriteGrokAuthFile materializes the bundle as $GROK_HOME/auth.json for the
// native child. The home must be a private directory; the file is published
// atomically with owner-only permission, and remnants of an interrupted earlier
// write are retired first. On failure the target is retired so no partial
// credential file is left behind.
func WriteGrokAuthFile(grokHome string, bundle GrokBundle) error {
	if err := CheckGrokHome(grokHome); err != nil {
		return err
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
	if err := CheckGrokHome(grokHome); err != nil {
		return GrokBundle{}, err
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
	// A second directory entry would keep the plaintext credential reachable
	// after the file is retired, so a linked file is never sealed.
	if err != nil || !os.SameFile(info, opened) || !grokSingleLinkFile(file, opened) {
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
	// Removing one name of a multiply linked file does not remove the
	// credential, so it is never reported as clean.
	if !grokSingleLinkPath(filepath.Join(grokHome, GrokAuthFileName), info) {
		return true, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupIdentity}
	}
	return true, nil
}

// RetireGrokAuthFile removes only the credential file and its attributed
// temporaries. The caller has already joined the native process and its
// descendants; the native lock and every other file are left to runtime cleanup.
// A credential file or temporary with another directory entry is still unlinked
// here, but retirement then reports an unconfirmed cleanup because the other
// entry keeps the plaintext credential reachable.
func RetireGrokAuthFile(grokHome string) error {
	linked, probeErr := grokCredentialLinked(grokHome)
	if err := retireCredentialFile(grokHome, GrokAuthFileName, security.SyncParent); err != nil {
		return err
	}
	if probeErr != nil {
		return probeErr
	}
	if linked {
		return &RuntimeCleanupError{Stage: CleanupAuthFile, Reason: CleanupIdentity}
	}
	return nil
}

// grokCredentialLinked reports whether the credential file or one of its
// attributed temporaries has more than one directory entry.
func grokCredentialLinked(grokHome string) (bool, error) {
	temporaries, real, err := credentialTemporaries(grokHome, GrokAuthFileName)
	if err != nil || !real {
		return false, err
	}
	linked := false
	for _, candidate := range temporaries {
		if info, err := os.Lstat(candidate); err == nil && !grokSingleLinkPath(candidate, info) {
			linked = true
		}
	}
	path := filepath.Join(grokHome, GrokAuthFileName)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return linked, nil
	}
	if err != nil {
		return linked, &RuntimeCleanupError{Stage: CleanupAuthFile, Reason: CleanupFilesystem}
	}
	if info.Mode().IsRegular() && !grokSingleLinkPath(path, info) {
		linked = true
	}
	return linked, nil
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
