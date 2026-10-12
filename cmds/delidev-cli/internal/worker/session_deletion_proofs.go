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
	Version     uint32                            `json:"version"`
	ServerID    domain.ID                         `json:"server_id"`
	SessionID   domain.ID                         `json:"session_id"`
	DeletionID  domain.ID                         `json:"deletion_id"`
	MachineID   domain.ID                         `json:"machine_id"`
	Obligations []sessionDeletionMarkerObligation `json:"obligations,omitempty"`
}

type sessionDeletionMarkerObligation struct {
	Identity domain.SessionDeletionIdentity `json:"identity"`
	Digest   string                         `json:"digest"`
	ReportID domain.ID                      `json:"report_id"`
	Phase    sessionDeletionMarkerPhase     `json:"phase"`
}

type sessionDeletionMarkerPhase uint8

const (
	sessionDeletionMarkerInitial sessionDeletionMarkerPhase = iota + 1
	sessionDeletionMarkerPublished
)

func deletionMarker(w domain.SessionDeletionWork) sessionDeletionMarker {
	return sessionDeletionMarker{Version: 3, ServerID: w.ServerID, SessionID: w.SessionID, DeletionID: w.DeletionID, MachineID: w.MachineID}
}

func validDeletionMarker(marker sessionDeletionMarker, w domain.SessionDeletionWork) bool {
	if marker.Version != 3 || marker.ServerID != w.ServerID || marker.SessionID != w.SessionID || marker.DeletionID != w.DeletionID || marker.MachineID != w.MachineID || len(marker.Obligations) > domain.MaxSessionDeletionJobs {
		return false
	}
	seen := make(map[domain.ID]bool, len(marker.Obligations))
	for _, obligation := range marker.Obligations {
		identity := obligation.Identity
		if identity.Validate() != nil || identity.ServerID != marker.ServerID || identity.SessionID != marker.SessionID || identity.DeletionID != marker.DeletionID || identity.MachineID != marker.MachineID || seen[identity.DeviceID] || len(obligation.Digest) != 64 || obligation.ReportID.Validate() != nil || obligation.Phase != sessionDeletionMarkerInitial && obligation.Phase != sessionDeletionMarkerPublished {
			return false
		}
		seen[identity.DeviceID] = true
	}
	return true
}

func markerObligation(marker *sessionDeletionMarker, w domain.SessionDeletionWork) (*sessionDeletionMarkerObligation, bool) {
	for i := range marker.Obligations {
		if marker.Obligations[i].Identity.DeviceID == w.DeviceID {
			if marker.Obligations[i].Identity != w.Identity() || marker.Obligations[i].Digest != w.Digest() {
				return nil, false
			}
			return &marker.Obligations[i], true
		}
	}
	if len(marker.Obligations) >= domain.MaxSessionDeletionJobs {
		return nil, false
	}
	marker.Obligations = append(marker.Obligations, sessionDeletionMarkerObligation{Identity: w.Identity(), Digest: w.Digest(), ReportID: domain.NewID(), Phase: sessionDeletionMarkerInitial})
	return &marker.Obligations[len(marker.Obligations)-1], true
}

func writeDeletionMarker(path string, marker sessionDeletionMarker) error {
	raw, err := json.Marshal(marker)
	if err != nil || len(raw) > domain.MaxSessionDeletionBytes {
		return domain.SessionDeletionPending()
	}
	if err := security.WriteAtomic(path, raw); err != nil {
		return err
	}
	return security.SyncParent(path)
}
func validDeletionProof(proof sessionDeletionProof, w domain.SessionDeletionWork) bool {
	return proof.Version == 2 && proof.Identity == w.Identity() && proof.Digest == w.Digest() && proof.ReportID.Validate() == nil && (!proof.Complete || proof.RemovalStarted) && (!proof.WorkspaceRetired || proof.Complete)
}

func sessionDeletionUsesWorkspace(w domain.SessionDeletionWork) bool {
	return len(w.Copies) != 0 || w.Fork != nil
}

// Acquire the original session fence lock before the obligation lock, then
// synchronize/import metadata. Native cleanup retains only the obligation lock.
func loadSessionDeletionProof(root string, w domain.SessionDeletionWork) (sessionDeletionProof, string, *security.Lock, error) {
	proof := sessionDeletionProof{Version: 2, Identity: w.Identity(), Digest: w.Digest(), ReportID: domain.NewID()}
	pending := func() (sessionDeletionProof, string, *security.Lock, error) {
		return proof, "", nil, domain.SessionDeletionPending()
	}
	if w.Validate() != nil {
		return pending()
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
	legacyRaw, markerErr := security.ReadPrivate(markerPath, domain.MaxSessionDeletionBytes)
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
			if writeJSON(paths.Worker, legacy) != nil {
				return pending()
			}
			proof = legacy
			if sessionDeletionUsesWorkspace(w) {
				if workspace.ImportLegacyDeletionWorkspaceProof(root, w, legacy.RemovalStarted || legacy.Complete) != nil {
					return pending()
				}
			} else if workspace.RequireNoLegacyDeletionWorkspaceProof(root, w) != nil || workspace.RequireNoKeyedDeletionWorkspaceProof(root, w) != nil {
				return pending()
			}
			again, err := security.ReadPrivate(markerPath, 4096)
			if err != nil || !bytes.Equal(again, legacyRaw) {
				return pending()
			}
			marker := deletionMarker(w)
			marker.Obligations = []sessionDeletionMarkerObligation{{Identity: w.Identity(), Digest: w.Digest(), ReportID: legacy.ReportID, Phase: sessionDeletionMarkerPublished}}
			if writeDeletionMarker(markerPath, marker) != nil {
				return pending()
			}
		case 2:
			var marker sessionDeletionMarker
			var oldMarker struct {
				Version    uint32    `json:"version"`
				ServerID   domain.ID `json:"server_id"`
				SessionID  domain.ID `json:"session_id"`
				DeletionID domain.ID `json:"deletion_id"`
				MachineID  domain.ID `json:"machine_id"`
			}
			if domain.Decode(legacyRaw, &oldMarker) != nil || oldMarker.Version != 2 || oldMarker.ServerID != w.ServerID || oldMarker.SessionID != w.SessionID || oldMarker.DeletionID != w.DeletionID || oldMarker.MachineID != w.MachineID || keyErr != nil {
				return pending()
			}
			proof = keyed
			marker = deletionMarker(w)
			marker.Obligations = []sessionDeletionMarkerObligation{{Identity: w.Identity(), Digest: w.Digest(), ReportID: keyed.ReportID, Phase: sessionDeletionMarkerPublished}}
			if writeDeletionMarker(markerPath, marker) != nil {
				return pending()
			}
			if workspace.RequireNoLegacyDeletionWorkspaceProof(root, w) != nil {
				return pending()
			}
		case 3:
			var marker sessionDeletionMarker
			if domain.Decode(legacyRaw, &marker) != nil || !validDeletionMarker(marker, w) {
				return pending()
			}
			obligation, exists := markerObligation(&marker, w)
			if !exists {
				return pending()
			}
			if keyErr == nil {
				if keyed.ReportID != obligation.ReportID {
					return pending()
				}
				proof = keyed
				if obligation.Phase == sessionDeletionMarkerInitial {
					obligation.Phase = sessionDeletionMarkerPublished
					if writeDeletionMarker(markerPath, marker) != nil {
						return pending()
					}
				}
			} else {
				if obligation.Phase == sessionDeletionMarkerPublished {
					return pending()
				}
				if workspace.RequireNoKeyedDeletionWorkspaceProof(root, w) != nil {
					return pending()
				}
				// A new device obligation in an existing session fence must be
				// durable before its keyed Worker proof. Otherwise a crash can
				// leave a proof whose report identity is absent from the fence.
				if writeDeletionMarker(markerPath, marker) != nil {
					return pending()
				}
				proof.ReportID = obligation.ReportID
				if writeJSON(paths.Worker, proof) != nil || security.SyncParent(paths.Worker) != nil {
					return pending()
				}
				obligation.Phase = sessionDeletionMarkerPublished
				if writeDeletionMarker(markerPath, marker) != nil {
					return pending()
				}
			}
			if workspace.RequireNoLegacyDeletionWorkspaceProof(root, w) != nil {
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
		if sessionDeletionUsesWorkspace(w) {
			if workspace.ImportLegacyDeletionWorkspaceProof(root, w, false) != nil {
				return pending()
			}
		} else if workspace.RequireNoLegacyDeletionWorkspaceProof(root, w) != nil || workspace.RequireNoKeyedDeletionWorkspaceProof(root, w) != nil {
			return pending()
		}
		// Publish the exact initial receipt in the shared fence before keyed
		// progress. A missing key is recoverable only while this entry remains
		// initial; published entries fail closed if their key disappears.
		marker := deletionMarker(w)
		obligation, ok := markerObligation(&marker, w)
		if !ok {
			return pending()
		}
		proof.ReportID = obligation.ReportID
		if writeDeletionMarker(markerPath, marker) != nil || writeJSON(paths.Worker, proof) != nil || security.SyncParent(paths.Worker) != nil {
			return pending()
		}
		obligation.Phase = sessionDeletionMarkerPublished
		if writeDeletionMarker(markerPath, marker) != nil {
			return pending()
		}
	}
	if sessionDeletionUsesWorkspace(w) && proof.RemovalStarted && !proof.Complete && workspace.RequireKeyedDeletionWorkspaceProof(root, w) != nil {
		return pending()
	}
	if security.SyncParent(paths.Worker) != nil {
		return pending()
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
