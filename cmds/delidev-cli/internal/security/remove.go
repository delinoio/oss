// SPDX-License-Identifier: Apache-2.0
package security

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// maxRemoveEntries bounds one traversal; maxRemoveDepth bounds directory nesting
// so a hostile tree cannot exhaust descriptors.
const (
	maxRemoveEntries = 100000
	maxRemoveDepth   = 256
)

// RemoveOwnedTree is used only after independently stopping original owners.
// It takes its own baseline of whatever exists at path, so it is only for
// callers that have no earlier validated identity to bind. Callers that validated
// ownership first must use RemoveOwnedTreeIdentity.
func RemoveOwnedTree(ctx context.Context, root, path string) error {
	identity, e := CapturePathIdentity(path)
	if e != nil {
		return e
	}
	defer identity.Close()
	return RemoveOwnedTreeIdentity(ctx, root, path, identity)
}

// RemoveOwnedTreeIdentity removes path only when it is still exactly the object
// captured during ownership validation. A path that was absent at validation
// must still be absent, and a different object at the path (including a
// same-content replacement) is left untouched and reported as pending cleanup.
// A validated object that has since vanished counts as already removed.
//
// On Unix the removal is anchored to the validated object: the path is opened
// once without following links, compared with the captured identity, and every
// descendant is then opened and unlinked relative to that open directory, so a
// directory swapped in for the root or for a subdirectory mid-removal is never
// entered. See removeTree for the platform guarantees and their limits.
func RemoveOwnedTreeIdentity(ctx context.Context, root, path string, expected PathIdentity) error {
	parent := filepath.Dir(path)
	if _, e := os.Lstat(parent); errors.Is(e, os.ErrNotExist) {
		return nil
	} else if e != nil {
		return domain.SessionDeletionPending()
	}
	canonical, e := filepath.EvalSymlinks(parent)
	if e != nil || canonical != parent || !strings.HasPrefix(parent, root+string(filepath.Separator)) {
		return domain.SessionDeletionPending()
	}
	if !expected.present {
		// Proven absence: any object that appeared since is foreign.
		if _, e := os.Lstat(path); errors.Is(e, os.ErrNotExist) {
			return SyncParent(path)
		}
		return domain.SessionDeletionPending()
	}
	if expected.record.Kind == ObjectSymlink {
		return domain.SessionDeletionPending()
	}
	if e := removeTree(ctx, path, expected); e != nil {
		return e
	}
	// The held handle (if any) must not outlive the removed object.
	expected.Release()
	if e := SyncParent(path); e != nil {
		return domain.SessionDeletionPending()
	}
	return nil
}
