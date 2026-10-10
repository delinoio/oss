// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type DeletionProofKind uint8

const (
	WorkerDeletionProof DeletionProofKind = iota
	WorkspaceDeletionProof
)

// Session directories remain admission tombstones; files belong to one original
// deletion/device obligation and retain their complete immutable work digest.
func SessionDeletionDirectory(root string, session domain.ID) string {
	return filepath.Join(root, "session-deletions", string(session))
}
func SessionDeletionProofPath(root string, w domain.SessionDeletionWork, kind DeletionProofKind) string {
	suffix := ".json"
	if kind == WorkspaceDeletionProof {
		suffix = "-workspace.json"
	}
	return filepath.Join(SessionDeletionDirectory(root, w.SessionID), string(w.DeletionID)+"-"+string(w.DeviceID)+suffix)
}

// The caller holds the original legacy/session lock and the new obligation lock
// where applicable. Rename only validated original evidence, never copy it to a
// different owner. Atomic rename avoids a crash leaving two active proof copies.
func MigrateSessionDeletionProof(root string, w domain.SessionDeletionWork, kind DeletionProofKind, limit int64, valid func([]byte) bool) error {
	if w.Validate() != nil || kind != WorkerDeletionProof && kind != WorkspaceDeletionProof {
		return domain.SessionDeletionPending()
	}
	parent := filepath.Join(root, "session-deletions")
	if err := security.PrivateDir(parent); err != nil {
		return domain.SessionDeletionPending()
	}
	if err := security.PrivateDir(SessionDeletionDirectory(root, w.SessionID)); err != nil {
		return domain.SessionDeletionPending()
	}
	legacy := filepath.Join(parent, string(w.SessionID)+".json")
	if kind == WorkspaceDeletionProof {
		legacy = filepath.Join(parent, string(w.SessionID)+"-workspace.json")
	}
	destination := SessionDeletionProofPath(root, w, kind)
	raw, err := security.ReadPrivate(legacy, limit)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !valid(raw) {
		return domain.SessionDeletionPending()
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		return domain.SessionDeletionPending()
	}
	if err := os.Rename(legacy, destination); err != nil {
		return domain.SessionDeletionPending()
	}
	if err := security.SyncParent(legacy); err != nil {
		return domain.SessionDeletionPending()
	}
	if err := security.SyncParent(destination); err != nil {
		return domain.SessionDeletionPending()
	}
	return nil
}
