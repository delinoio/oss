// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// GrokHome is a Grok home anchored to the directory handle it named when the
// credential was materialized. Every credential operation (write, read,
// presence check, retirement) runs relative to that one open directory
// (openat-style, never following a link out of it), so a home that is renamed
// away and replaced between binding and use cannot redirect a write or a removal
// into the substitute: the check that the directory is private and the use of
// it are the same handle, with no path lookup in between.
//
// The native Grok child still reaches the home through its path (it is started
// with GROK_HOME set), and a path cannot be anchored for another process.
// Verify proves the path still names the held directory. A swap after Verify
// therefore cannot misdirect any Worker-side operation here; it can only make
// the native child read or write the substitute, and the next Verify (always
// run before a credential is read or a Finish is reported) rejects the lease as
// uncleaned. The held directory object itself stays reachable through the
// handle, so its credential is still retired.
type GrokHome struct {
	path     string
	root     *os.Root
	identity os.FileInfo
}

// BindGrokHome opens grokHome once, validates the opened directory itself and
// records its identity. Validation reads the handle (type, owner, permissions or
// ACL), not the path; the path is compared with the handle only to refuse a
// symlink or a directory that was swapped during the bind.
func BindGrokHome(grokHome string) (GrokHome, error) {
	named, err := os.Lstat(grokHome)
	if err != nil || !named.IsDir() || named.Mode()&os.ModeSymlink != 0 {
		return GrokHome{}, GrokBundleInvalid()
	}
	root, err := os.OpenRoot(grokHome)
	if err != nil {
		return GrokHome{}, GrokBundleInvalid()
	}
	home := GrokHome{path: grokHome, root: root}
	dir, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return GrokHome{}, GrokBundleInvalid()
	}
	defer dir.Close()
	// File.Stat reads the open handle, so the recorded identity (including the
	// Windows file ID, which is otherwise resolved lazily from a path) belongs
	// to the directory actually held.
	identity, err := dir.Stat()
	if err != nil || !identity.IsDir() || security.CheckPrivateDirHandle(dir) != nil || !os.SameFile(named, identity) {
		_ = root.Close()
		return GrokHome{}, GrokBundleInvalid()
	}
	home.identity = identity
	return home, nil
}

// Path is the path the home was bound at. It is for the native child's
// environment and for diagnostics only; no operation of GrokHome uses it to
// reach a file.
func (h GrokHome) Path() string { return h.path }

// Close releases the held directory handle. Operations on a closed or zero
// GrokHome fail closed.
func (h GrokHome) Close() error {
	if h.root == nil {
		return nil
	}
	return h.root.Close()
}

func homeUnbound() error {
	return &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupIdentity}
}

// checkHeld re-validates the held directory through its own handle.
func (h GrokHome) checkHeld() error {
	if h.root == nil || h.identity == nil {
		return homeUnbound()
	}
	dir, err := h.root.Open(".")
	if err != nil {
		return &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupFilesystem}
	}
	defer dir.Close()
	if security.CheckPrivateDirHandle(dir) != nil {
		return &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupFilesystem}
	}
	return nil
}

// Verify reports an identity failure unless the held directory is still private
// and the bound path still names it, not a link and not a successor. The
// directory object stays alive while it is held, so an equal identity cannot
// belong to a recycled object.
func (h GrokHome) Verify() error {
	if err := h.checkHeld(); err != nil {
		return err
	}
	current, err := os.Lstat(h.path)
	if err != nil {
		return &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupFilesystem}
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.IsDir() {
		return &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupFilesystem}
	}
	if !os.SameFile(h.identity, current) {
		return homeUnbound()
	}
	return nil
}

// WriteAuthFile materializes the bundle as auth.json inside the held home for
// the native child. The path must still name the held directory (Verify); the
// write itself is anchored, so even a swap that lands after Verify writes into
// the held directory only. The file is published atomically with owner-only
// permission, and remnants of an interrupted earlier write are retired first.
// On failure the target is retired so no partial credential file is left behind.
func (h GrokHome) WriteAuthFile(bundle GrokBundle) error {
	if err := h.Verify(); err != nil {
		return GrokBundleInvalid()
	}
	raw, err := bundle.AuthFile()
	if err != nil {
		return err
	}
	defer clear(raw)
	if err := h.writeAnchored(raw); err != nil {
		_ = h.RetireAuthFile()
		return GrokBundleInvalid()
	}
	return nil
}

func (h GrokHome) writeAnchored(raw []byte) error {
	if err := h.checkHeld(); err != nil {
		return err
	}
	if err := h.retireTemporaries(); err != nil {
		return err
	}
	suffix := make([]byte, 12)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	temporary := grokTemporaryPrefix + hex.EncodeToString(suffix)
	file, err := h.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	err = file.Chmod(0o600)
	if err == nil {
		_, err = file.Write(raw)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = h.root.Rename(temporary, GrokAuthFileName)
	}
	if err != nil {
		_ = h.root.Remove(temporary)
		return err
	}
	return h.syncDir()
}

// grokTemporaryPrefix is security.OwnedTemporaryPrefix for the auth file, so
// the attribution contract shared with the path-based writers is unchanged.
var grokTemporaryPrefix = security.OwnedTemporaryPrefix(GrokAuthFileName)

// syncDir makes the held directory's entries durable where the platform needs
// an explicit directory sync.
func (h GrokHome) syncDir() error {
	dir, err := h.root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return syncGrokDirectory(dir)
}

// ReadAuthFile reads and validates the native auth.json after a login or
// refresh. The entry must be a regular, bounded, non-linked file inside the
// held home.
func (h GrokHome) ReadAuthFile() (GrokBundle, error) {
	if h.checkHeld() != nil {
		return GrokBundle{}, GrokBundleInvalid()
	}
	info, err := h.root.Lstat(GrokAuthFileName)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxGrokAuthFile {
		return GrokBundle{}, GrokBundleInvalid()
	}
	file, err := h.root.OpenFile(GrokAuthFileName, os.O_RDONLY|grokOpenReadFlags, 0)
	if err != nil {
		return GrokBundle{}, GrokBundleInvalid()
	}
	defer file.Close()
	opened, err := file.Stat()
	// A second directory entry would keep the plaintext credential reachable
	// after the file is retired, so a linked file is never sealed.
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || !grokSingleLinkFile(file, opened) {
		return GrokBundle{}, GrokBundleInvalid()
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaxGrokAuthFile+1))
	defer clear(raw)
	if err != nil {
		return GrokBundle{}, GrokBundleInvalid()
	}
	return ParseGrokAuthFile(raw)
}

// AuthFilePresent reports whether the credential file or an interrupted
// write's target-attributed temporary remains in the held home.
func (h GrokHome) AuthFilePresent() (bool, error) {
	if err := h.checkHeld(); err != nil {
		return true, err
	}
	temporaries, err := h.temporaries()
	if err != nil {
		return true, err
	}
	if len(temporaries) != 0 {
		return true, nil
	}
	info, err := h.root.Lstat(GrokAuthFileName)
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
	if !grokSingleLinkEntry(h.root, GrokAuthFileName, info) {
		return true, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupIdentity}
	}
	return true, nil
}

// RetireAuthFile removes only the credential file and its attributed
// temporaries from the held home, through the held handle: it works even when
// the bound path was since replaced, which is what removes a credential this
// Worker wrote into a directory that was then swapped away. The caller has
// already joined the native process and its descendants; every other file is
// left to runtime cleanup. A credential file or temporary with another
// directory entry is still unlinked here, but retirement then reports an
// unconfirmed cleanup because the other entry keeps the plaintext reachable.
func (h GrokHome) RetireAuthFile() error {
	if h.root == nil {
		return homeUnbound()
	}
	linked, probeErr := h.credentialLinked()
	if err := h.retireEntries(); err != nil {
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

// temporaries lists the target-attributed temporary files an interrupted write
// can leave beside auth.json. A non-regular entry with the attributed name is
// reported as an error.
func (h GrokHome) temporaries() ([]string, error) {
	dir, err := h.root.Open(".")
	if err != nil {
		return nil, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupFilesystem}
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupFilesystem}
	}
	var found []string
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), grokTemporaryPrefix) {
			continue
		}
		info, err := h.root.Lstat(entry.Name())
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupFilesystem}
		}
		if !info.Mode().IsRegular() {
			return nil, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupNotRegular}
		}
		found = append(found, entry.Name())
	}
	return found, nil
}

func (h GrokHome) retireTemporaries() error {
	found, err := h.temporaries()
	if err != nil {
		return err
	}
	for _, name := range found {
		if err := h.root.Remove(name); err != nil && !os.IsNotExist(err) {
			return &RuntimeCleanupError{Stage: CleanupAuthFile, Reason: CleanupFilesystem}
		}
	}
	// An empty scan can follow a removal whose directory sync failed, so the
	// directory is synced before an absent temporary counts as retired.
	if err := h.syncDir(); err != nil {
		return &RuntimeCleanupError{Stage: CleanupSync, Reason: CleanupFilesystem}
	}
	return nil
}

func (h GrokHome) retireEntries() error {
	if err := h.retireTemporaries(); err != nil {
		return err
	}
	info, err := h.root.Lstat(GrokAuthFileName)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return &RuntimeCleanupError{Stage: CleanupAuthFile, Reason: CleanupFilesystem}
	}
	if !info.Mode().IsRegular() {
		return &RuntimeCleanupError{Stage: CleanupAuthFile, Reason: CleanupNotRegular}
	}
	if err := h.root.Remove(GrokAuthFileName); err != nil && !os.IsNotExist(err) {
		return &RuntimeCleanupError{Stage: CleanupAuthFile, Reason: CleanupFilesystem}
	}
	if err := h.syncDir(); err != nil {
		return &RuntimeCleanupError{Stage: CleanupSync, Reason: CleanupFilesystem}
	}
	return nil
}

// credentialLinked reports whether the credential file or one of its
// attributed temporaries has more than one directory entry.
func (h GrokHome) credentialLinked() (bool, error) {
	temporaries, err := h.temporaries()
	if err != nil {
		return false, err
	}
	linked := false
	for _, name := range temporaries {
		if info, err := h.root.Lstat(name); err == nil && !grokSingleLinkEntry(h.root, name, info) {
			linked = true
		}
	}
	info, err := h.root.Lstat(GrokAuthFileName)
	if os.IsNotExist(err) {
		return linked, nil
	}
	if err != nil {
		return linked, &RuntimeCleanupError{Stage: CleanupAuthFile, Reason: CleanupFilesystem}
	}
	if info.Mode().IsRegular() && !grokSingleLinkEntry(h.root, GrokAuthFileName, info) {
		linked = true
	}
	return linked, nil
}

// SealFinish reads the native credential file and seals it as the Finish bundle
// for the next generation. When expected is non-zero the file must still belong
// to the original bound identity.
func (h GrokHome) SealFinish(server, account, generation domain.ID, expected GrokBundle) ([]byte, GrokBundle, error) {
	bundle, err := h.ReadAuthFile()
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
