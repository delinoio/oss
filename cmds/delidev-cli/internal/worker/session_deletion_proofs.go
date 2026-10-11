// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

// The session-wide marker only closes admission. Progress and report identity
// belong to the original device obligation, never this shared fence.
type sessionDeletionMarker struct {
	Version    uint32    `json:"version"`
	ServerID   domain.ID `json:"server_id"`
	SessionID  domain.ID `json:"session_id"`
	DeletionID domain.ID `json:"deletion_id"`
	MachineID  domain.ID `json:"machine_id"`
}

func deletionMarker(w domain.SessionDeletionWork) sessionDeletionMarker {
	return sessionDeletionMarker{2, w.ServerID, w.SessionID, w.DeletionID, w.MachineID}
}
func validDeletionProof(proof sessionDeletionProof, w domain.SessionDeletionWork) bool {
	return proof.Version == 2 && proof.Identity == w.Identity() && proof.Digest == w.Digest() && proof.ReportID.Validate() == nil && (!proof.Complete || proof.RemovalStarted) && (!proof.WorkspaceRetired || proof.Complete)
}

// Acquire the original session fence lock before the obligation lock, then
// synchronize/import metadata. Native cleanup retains only the obligation lock.
func loadSessionDeletionProof(root string, w domain.SessionDeletionWork) (sessionDeletionProof, string, *security.Lock, error) {
	proof := sessionDeletionProof{Version: 2, Identity: w.Identity(), Digest: w.Digest(), ReportID: domain.NewID()}
	pending := func() (sessionDeletionProof, string, *security.Lock, error) {
		return proof, "", nil, domain.SessionDeletionPending()
	}
	if security.PrivateDir(filepath.Join(root, "session-deletions")) != nil {
		return pending()
	}
	fence, err := security.TryLock(filepath.Join(root, "session-deletions", string(w.SessionID)+".lock"))
	if err != nil {
		return pending()
	}
	defer fence.Close()
	paths, err := workspace.SessionDeletionProofPaths(root, w)
	if err != nil {
		return pending()
	}
	lock, err := security.TryLock(paths.Lock)
	if err != nil {
		return pending()
	}
	success := false
	defer func() {
		if !success {
			lock.Close()
		}
	}()
	markerPath := sessionDeletionPath(root, w.SessionID)
	legacyRaw, markerErr := security.ReadPrivate(markerPath, 4096)
	keyedRaw, keyErr := security.ReadPrivate(paths.Worker, 4096)
	var keyed sessionDeletionProof
	if keyErr == nil {
		if domain.Decode(keyedRaw, &keyed) != nil || !validDeletionProof(keyed, w) {
			return pending()
		}
	} else if !errors.Is(keyErr, os.ErrNotExist) {
		return pending()
	}
	if markerErr == nil {
		var header struct {
			Version uint32 `json:"version"`
		}
		if json.Unmarshal(legacyRaw, &header) != nil {
			return pending()
		}
		switch header.Version {
		case 1:
			var legacy sessionDeletionProof
			if domain.Decode(legacyRaw, &legacy) != nil || legacy.Identity != (domain.SessionDeletionIdentity{}) || legacy.WorkspaceRetired || legacy.Digest != w.Digest() || legacy.ReportID.Validate() != nil || legacy.Complete && !legacy.RemovalStarted {
				return pending()
			}
			legacy.Version = 2
			legacy.Identity = w.Identity()
			expected, _ := json.Marshal(legacy)
			if keyErr == nil && !bytes.Equal(keyedRaw, expected) {
				return pending()
			}
			if keyErr != nil {
				if writeJSON(paths.Worker, legacy) != nil {
					return pending()
				}
			}
			proof = legacy
			if workspace.ImportLegacyDeletionWorkspaceProof(root, w) != nil {
				return pending()
			}
			again, err := security.ReadPrivate(markerPath, 4096)
			if err != nil || !bytes.Equal(again, legacyRaw) {
				return pending()
			}
			if writeJSON(markerPath, deletionMarker(w)) != nil {
				return pending()
			}
		case 2:
			var marker sessionDeletionMarker
			if domain.Decode(legacyRaw, &marker) != nil || marker != deletionMarker(w) {
				return pending()
			}
			if keyErr == nil {
				proof = keyed
			} else if writeJSON(paths.Worker, proof) != nil {
				return pending()
			}
			if workspace.ImportLegacyDeletionWorkspaceProof(root, w) != nil {
				return pending()
			}
		default:
			return pending()
		}
	} else if !errors.Is(markerErr, os.ErrNotExist) || keyErr == nil {
		// A keyed proof without its synchronized original admission fence is not a
		// new deletion. Restored/transplanted metadata must stay pending.
		return pending()
	} else {
		if workspace.ImportLegacyDeletionWorkspaceProof(root, w) != nil {
			return pending()
		}
		// Fence first: even a failed initial progress publication rejects new work.
		if writeJSON(markerPath, deletionMarker(w)) != nil || writeJSON(paths.Worker, proof) != nil {
			return pending()
		}
	}
	success = true
	return proof, paths.Worker, lock, nil
}

func finishSessionDeletionProof(root, path string, w domain.SessionDeletionWork, proof sessionDeletionProof) (sessionDeletionProof, error) {
	if !proof.Complete || !validDeletionProof(proof, w) {
		return proof, domain.SessionDeletionPending()
	}
	if !proof.WorkspaceRetired {
		if err := workspace.RetireDeletionWorkspaceProof(root, w); err != nil {
			return proof, err
		}
		proof.WorkspaceRetired = true
		if err := writeJSON(path, proof); err != nil {
			return proof, err
		}
	}
	remnants, err := workspace.SessionDeletionWorkspaceProofRemnants(root, w)
	if err != nil {
		return proof, err
	}
	for _, remnant := range remnants {
		if _, err := os.Lstat(remnant); !errors.Is(err, os.ErrNotExist) {
			return proof, domain.SessionDeletionPending()
		}
	}
	return proof, nil
}
