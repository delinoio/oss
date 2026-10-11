// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type DeletionProofPaths struct {
	Worker    string
	Workspace string
	Lock      string
}

// SessionDeletionProofPaths derives a fixed bounded namespace from validated
// original IDs, never paths supplied by a Worker report or current device.
func SessionDeletionProofPaths(root string, w domain.SessionDeletionWork) (DeletionProofPaths, error) {
	key, err := w.Identity().Key()
	if err != nil {
		return DeletionProofPaths{}, err
	}
	base := filepath.Join(root, "session-deletions", string(w.SessionID)+"-"+key)
	return DeletionProofPaths{base + ".json", base + "-workspace.json", base + ".lock"}, nil
}
func legacyDeletionWorkspacePath(root string, w domain.SessionDeletionWork) string {
	return filepath.Join(root, "session-deletions", string(w.SessionID)+"-workspace.json")
}

// ImportLegacyDeletionWorkspaceProof requires the caller's original deletion or
// workspace lock. Synchronize the exact keyed copy before retiring old evidence.
// A foreign digest cannot establish another device's workspace ownership.
func ImportLegacyDeletionWorkspaceProof(root string, w domain.SessionDeletionWork) error {
	if w.Validate() != nil {
		return domain.SessionDeletionPending()
	}
	paths, err := SessionDeletionProofPaths(root, w)
	if err != nil {
		return err
	}
	legacyPath := legacyDeletionWorkspacePath(root, w)
	raw, err := security.ReadPrivate(legacyPath, 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var legacy deletionWorkspaceProof
	if err != nil || domain.Decode(raw, &legacy) != nil || legacy.Version != 1 || legacy.AdmissionDigest != "" || legacy.Identity != (domain.SessionDeletionIdentity{}) || legacy.Digest != w.Digest() {
		return domain.SessionDeletionPending()
	}
	legacy.Version = 2
	legacy.Identity = w.Identity()
	if !validDeletionWorkspaceProof(legacy, w) {
		return domain.SessionDeletionPending()
	}
	expected, _ := json.Marshal(legacy)
	current, err := security.ReadPrivate(paths.Workspace, 1<<20)
	if err == nil {
		// Only the exact equal import pair can finish an interrupted publication.
		if !bytes.Equal(current, expected) {
			return domain.SessionDeletionPending()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return domain.SessionDeletionPending()
	}
	if err := writeDeletionWorkspaceProof(paths.Workspace, legacy); err != nil {
		return err
	}
	// Recheck the original bytes before retiring only that admitted legacy name.
	again, err := security.ReadPrivate(legacyPath, 1<<20)
	if err != nil || !bytes.Equal(raw, again) {
		return domain.SessionDeletionPending()
	}
	if err := os.Remove(legacyPath); err != nil {
		return domain.SessionDeletionPending()
	}
	if security.SyncParent(legacyPath) != nil {
		return domain.SessionDeletionPending()
	}
	return nil
}

// RetireDeletionWorkspaceProof runs only after the original complete Worker
// progress was synchronized. It removes no workspace or native copy. Once its
// retirement commits, any restored proof is absence-only on completed replay.
func RetireDeletionWorkspaceProof(root string, w domain.SessionDeletionWork) error {
	paths, err := SessionDeletionProofPaths(root, w)
	if err != nil {
		return err
	}
	raw, err := security.ReadPrivate(paths.Workspace, 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var proof deletionWorkspaceProof
	if err != nil || domain.Decode(raw, &proof) != nil || proof.Version != 2 || proof.Identity != w.Identity() || proof.Digest != w.Digest() || !proof.Removed || proof.Manifest != nil {
		return domain.SessionDeletionPending()
	}
	again, err := security.ReadPrivate(paths.Workspace, 1<<20)
	if err != nil || !bytes.Equal(raw, again) {
		return domain.SessionDeletionPending()
	}
	if os.Remove(paths.Workspace) != nil || security.SyncParent(paths.Workspace) != nil {
		return domain.SessionDeletionPending()
	}
	return nil
}

func SessionDeletionWorkspaceProofRemnants(root string, w domain.SessionDeletionWork) ([]string, error) {
	paths, err := SessionDeletionProofPaths(root, w)
	if err != nil {
		return nil, err
	}
	return []string{paths.Workspace, legacyDeletionWorkspacePath(root, w)}, nil
}

// A current fence has already completed legacy import. Reappearing legacy
// evidence is protected on retry, including before final proof retirement.
func RequireNoLegacyDeletionWorkspaceProof(root string, w domain.SessionDeletionWork) error {
	if _, err := os.Lstat(legacyDeletionWorkspacePath(root, w)); !errors.Is(err, os.ErrNotExist) {
		return domain.SessionDeletionPending()
	}
	return nil
}

func validDeletionWorkspaceProof(proof deletionWorkspaceProof, w domain.SessionDeletionWork) bool {
	if proof.Version != 2 || proof.Identity != w.Identity() || proof.Digest != w.Digest() {
		return false
	}
	return proof.Manifest == nil || proof.Manifest.SessionID == w.SessionID && proof.Manifest.MachineID == w.MachineID && proof.Manifest.Type.Valid() && slices.Contains(w.PreparationDigests, proof.Manifest.InputDigest)
}
