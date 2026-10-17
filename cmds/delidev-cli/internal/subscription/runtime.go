// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const (
	RuntimeFileLimit = 2048
	RuntimeByteLimit = 64 << 20
)

type CleanupStage string

const (
	CleanupNativeProcess CleanupStage = "native-process"
	CleanupAuthFile      CleanupStage = "auth-file"
	CleanupValidation    CleanupStage = "runtime-validation"
	CleanupInventory     CleanupStage = "runtime-inventory"
	CleanupRemoval       CleanupStage = "runtime-removal"
	CleanupSync          CleanupStage = "runtime-sync"
)

type CleanupReason string

const (
	CleanupUnconfirmed CleanupReason = "unconfirmed"
	CleanupReadFailed  CleanupReason = "read-failed"
	CleanupMismatch    CleanupReason = "bundle-mismatch"
	CleanupFilesystem  CleanupReason = "filesystem"
	CleanupIdentity    CleanupReason = "identity"
	CleanupSymlink     CleanupReason = "symlink"
	CleanupNotRegular  CleanupReason = "non-regular"
	CleanupFileLimit   CleanupReason = "file-limit"
	CleanupByteLimit   CleanupReason = "byte-limit"
	// CleanupPendingTemporary marks the target-attributed temporary of an
	// interrupted credential write. CleanupUnsafe marks a retained credential
	// file whose owner or permissions are not the current user's private ones.
	CleanupPendingTemporary CleanupReason = "pending-temporary"
	CleanupUnsafe           CleanupReason = "unsafe-permissions"
)

// RuntimeCleanupError retains only closed classifications and bounded counters.
// Its public error remains the original authentication recovery failure; no
// filesystem error, path or file content escapes into diagnostics or logs.
type RuntimeCleanupError struct {
	Stage     CleanupStage
	Reason    CleanupReason
	FileCount int
	Bytes     int64
}

func (e *RuntimeCleanupError) Error() string { return Invalid().Error() }
func (e *RuntimeCleanupError) Unwrap() error { return Invalid() }

// CleanupRuntime removes only the identity-checked private runtime after the
// caller independently joins its original native process and descendants.
func CleanupRuntime(home string, original os.FileInfo) error {
	count, total := 0, int64(0)
	fail := func(stage CleanupStage, reason CleanupReason) error {
		return &RuntimeCleanupError{Stage: stage, Reason: reason, FileCount: count, Bytes: total}
	}
	if err := security.CheckPrivateDir(home); err != nil {
		return fail(CleanupValidation, CleanupFilesystem)
	}
	current, err := os.Stat(home)
	if err != nil || !os.SameFile(current, original) {
		return fail(CleanupValidation, CleanupIdentity)
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return fail(CleanupValidation, CleanupFilesystem)
	}
	rootClosed := false
	defer func() {
		if !rootClosed {
			_ = root.Close()
		}
	}()
	anchored, err := root.Stat(".")
	if err != nil || !os.SameFile(anchored, original) {
		return fail(CleanupValidation, CleanupIdentity)
	}
	if err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fail(CleanupInventory, CleanupFilesystem)
		}
		// Directory-entry type bits are not complete on every filesystem
		// provider, especially for Windows reparse points. Re-stat the path
		// through the already-open root so symlinks and other replacements are
		// classified from anchored metadata before any removal is attempted.
		info, err := root.Lstat(path)
		if err != nil {
			return fail(CleanupInventory, CleanupFilesystem)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fail(CleanupInventory, CleanupSymlink)
		}
		if info.IsDir() {
			return nil
		}
		count++
		if !info.Mode().IsRegular() {
			return fail(CleanupInventory, CleanupNotRegular)
		}
		if count > RuntimeFileLimit {
			return fail(CleanupInventory, CleanupFileLimit)
		}
		// Saturate each inspected size above the limit. Sparse files cannot
		// overflow this diagnostic counter and bypass the existing byte bound.
		total += min(info.Size(), RuntimeByteLimit+1)
		if total > RuntimeByteLimit {
			return fail(CleanupInventory, CleanupByteLimit)
		}
		return nil
	}); err != nil {
		return err
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return fail(CleanupRemoval, CleanupFilesystem)
	}
	for _, entry := range entries {
		if err := root.RemoveAll(entry.Name()); err != nil {
			return fail(CleanupRemoval, CleanupFilesystem)
		}
	}
	// Windows does not allow removing a directory while its OpenRoot handle is
	// still live. Close the anchored traversal before checking and unlinking the
	// original runtime directory; the deferred close remains for error paths.
	if err := root.Close(); err != nil {
		return fail(CleanupRemoval, CleanupFilesystem)
	}
	rootClosed = true
	current, err = os.Stat(home)
	if err != nil || !os.SameFile(current, original) {
		return fail(CleanupRemoval, CleanupIdentity)
	}
	if err := os.Remove(home); err != nil {
		return fail(CleanupRemoval, CleanupFilesystem)
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		return fail(CleanupRemoval, CleanupFilesystem)
	}
	if err := security.SyncParent(home); err != nil {
		return fail(CleanupSync, CleanupFilesystem)
	}
	return nil
}

// CredentialFiles lists the exact transient credential files a native adapter
// may write below a persistent profile root, relative to that root. Only these
// files are retired after an operation; native history, caches and helper
// files stay in the retained profile.
var CredentialFiles = []string{
	"codex/auth.json",
	"claude/.credentials.json",
}

// credentialParentReal proves that the directory containing a credential file
// is a real directory, so a planted link can never redirect an inspection or
// removal outside the profile.
func credentialParentReal(root, name string) (bool, error) {
	parent := filepath.Dir(filepath.Join(root, filepath.FromSlash(name)))
	info, err := os.Lstat(parent)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupFilesystem}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupSymlink}
	}
	if !info.IsDir() {
		return false, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupNotRegular}
	}
	return true, nil
}

// credentialTemporaries lists the target-attributed temporary files that an
// interrupted atomic credential write can leave beside a credential file. Such
// a file may hold a plaintext credential bundle under a name other than the
// final path, so every presence check and retirement covers it. A non-regular
// entry with the attributed name is reported as an error.
func credentialTemporaries(root, name string) ([]string, bool, error) {
	real, err := credentialParentReal(root, name)
	if err != nil || !real {
		return nil, real, err
	}
	path := filepath.Join(root, filepath.FromSlash(name))
	parent, prefix := filepath.Dir(path), security.OwnedTemporaryPrefix(path)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil, true, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupFilesystem}
	}
	var found []string
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		candidate := filepath.Join(parent, entry.Name())
		info, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, true, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupFilesystem}
		}
		if !info.Mode().IsRegular() {
			return nil, true, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupNotRegular}
		}
		found = append(found, candidate)
	}
	return found, true, nil
}

func retireCredentialTemporaries(root, name string) error {
	return retireCredentialTemporariesWithSync(root, name, security.SyncParent)
}

func retireCredentialTemporariesWithSync(root, name string, syncParent func(string) error) error {
	found, real, err := credentialTemporaries(root, name)
	if err != nil {
		return err
	}
	if !real {
		return nil
	}
	path := filepath.Join(root, filepath.FromSlash(name))
	for _, candidate := range found {
		if err := os.Remove(candidate); err != nil && !os.IsNotExist(err) {
			return &RuntimeCleanupError{Stage: CleanupAuthFile, Reason: CleanupFilesystem}
		}
		if err := syncParent(candidate); err != nil {
			return &RuntimeCleanupError{Stage: CleanupSync, Reason: CleanupFilesystem}
		}
	}
	// An empty scan can follow a removal whose parent sync failed. Confirm the
	// validated parent before treating the absent temporary as retired.
	if len(found) == 0 {
		if err := syncParent(path); err != nil {
			return &RuntimeCleanupError{Stage: CleanupSync, Reason: CleanupFilesystem}
		}
	}
	return nil
}

// WriteCredentialFile publishes one credential file atomically through a
// temporary name attributed to its target. Remnants of an earlier interrupted
// write to the same target are retired first, so a crash can never leave a
// credential bundle that a later operation would consider clean.
func WriteCredentialFile(path string, contents []byte) error {
	if err := retireCredentialTemporaries(filepath.Dir(path), filepath.Base(path)); err != nil {
		return err
	}
	return security.WriteAtomicOwned(path, contents)
}

// CredentialFilePresent reports whether a transient credential file, or the
// target-attributed temporary of an interrupted credential write, remains in a
// retained profile. An unexpected non-regular entry is reported as an error
// because it cannot be proven to be removable credential state.
func CredentialFilePresent(root string) (bool, error) {
	for _, name := range CredentialFiles {
		real, err := credentialParentReal(root, name)
		if err != nil {
			return true, err
		}
		if !real {
			continue
		}
		temporaries, _, err := credentialTemporaries(root, name)
		if err != nil {
			return true, err
		}
		if len(temporaries) != 0 {
			return true, nil
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return true, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupFilesystem}
		}
		if !info.Mode().IsRegular() {
			return true, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupNotRegular}
		}
		return true, nil
	}
	return false, nil
}

// InspectRetainedCredentials admits the account-owned credential files that a
// completed operation retained in a persistent auth profile. It reports whether
// at least one credential file is present. A retained regular file with private
// permissions owned by the current user is admitted. Anything else is an
// integrity failure: a symlink or other non-regular entry, a file that other
// users can access or that another user owns, and the target-attributed
// temporary of an interrupted credential write (which may hold a partial or
// plaintext bundle under a name other than the final path). Admission is not
// authority: a retained file never authorizes use; only the published vault
// generation does.
func InspectRetainedCredentials(root string) (bool, error) {
	return InspectRetainedCredentialFiles(root, CredentialFiles)
}

// InspectRetainedCredentialFiles is InspectRetainedCredentials for an explicit
// set of credential files below root. An adapter that owns one native target
// passes exactly that target, so another harness' credential file in the same
// profile is neither admitted nor an integrity failure of this adapter.
func InspectRetainedCredentialFiles(root string, names []string) (bool, error) {
	present := false
	for _, name := range names {
		real, err := credentialParentReal(root, name)
		if err != nil {
			return false, err
		}
		if !real {
			continue
		}
		temporaries, _, err := credentialTemporaries(root, name)
		if err != nil {
			return false, err
		}
		if len(temporaries) != 0 {
			return false, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupPendingTemporary}
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupFilesystem}
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return false, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupSymlink}
		}
		if !info.Mode().IsRegular() {
			return false, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupNotRegular}
		}
		if err := security.RegularPrivate(path); err != nil {
			return false, &RuntimeCleanupError{Stage: CleanupValidation, Reason: CleanupUnsafe}
		}
		present = true
	}
	return present, nil
}

// RetireCredentialFiles removes only the listed transient credential files and
// their target-attributed temporaries from a retained profile. The profile
// directory and everything else it contains, including native-created helper
// symlinks, are left untouched. The caller has already joined the original
// native process and descendants.
func RetireCredentialFiles(root string) error {
	return retireCredentialFilesWithSync(root, security.SyncParent)
}

// RetireCredentialFilesNamed removes only the named credential files (and their
// target-attributed temporaries) below root. An adapter that owns one native
// target retires exactly that target and never another harness' credential
// file that happens to share the profile.
func RetireCredentialFilesNamed(root string, names []string) error {
	return retireNamedCredentialFilesWithSync(root, names, security.SyncParent)
}

func retireCredentialFilesWithSync(root string, syncParent func(string) error) error {
	return retireNamedCredentialFilesWithSync(root, CredentialFiles, syncParent)
}

func retireNamedCredentialFilesWithSync(root string, names []string, syncParent func(string) error) error {
	for _, name := range names {
		if err := retireCredentialFile(root, name, syncParent); err != nil {
			return err
		}
	}
	return nil
}

// retireCredentialFile removes one credential file and its target-attributed
// temporaries below root, proving the parent directory is real first.
func retireCredentialFile(root, name string, syncParent func(string) error) error {
	real, err := credentialParentReal(root, name)
	if err != nil {
		return err
	}
	if !real {
		return nil
	}
	if err := retireCredentialTemporariesWithSync(root, name, syncParent); err != nil {
		return err
	}
	path := filepath.Join(root, filepath.FromSlash(name))
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := syncParent(path); err != nil {
			return &RuntimeCleanupError{Stage: CleanupSync, Reason: CleanupFilesystem}
		}
		return nil
	}
	if err != nil {
		return &RuntimeCleanupError{Stage: CleanupAuthFile, Reason: CleanupFilesystem}
	}
	if !info.Mode().IsRegular() {
		return &RuntimeCleanupError{Stage: CleanupAuthFile, Reason: CleanupNotRegular}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return &RuntimeCleanupError{Stage: CleanupAuthFile, Reason: CleanupFilesystem}
	}
	if err := syncParent(path); err != nil {
		return &RuntimeCleanupError{Stage: CleanupSync, Reason: CleanupFilesystem}
	}
	return nil
}
