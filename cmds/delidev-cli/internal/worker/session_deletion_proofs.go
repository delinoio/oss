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

// The session-wide marker closes admission and fences the original report
// identity. Progress remains bound to the original device obligation.
type sessionDeletionMarker struct {
	Version     uint32                            `json:"version"`
	ServerID    domain.ID                         `json:"server_id"`
	SessionID   domain.ID                         `json:"session_id"`
	DeletionID  domain.ID                         `json:"deletion_id"`
	MachineID   domain.ID                         `json:"machine_id"`
	Obligations []sessionDeletionMarkerObligation `json:"obligations,omitempty"`
}

type sessionDeletionMarkerObligation struct {
	Identity       domain.SessionDeletionIdentity `json:"identity"`
	Digest         string                         `json:"digest"`
	PreviousDigest string                         `json:"previous_digest,omitempty"`
	ReportID       domain.ID                      `json:"report_id"`
	Phase          sessionDeletionMarkerPhase     `json:"phase"`
}

type sessionDeletionMarkerPhase uint8

const (
	sessionDeletionMarkerInitial sessionDeletionMarkerPhase = iota + 1
	sessionDeletionMarkerPublished
	sessionDeletionMarkerInitialWorkspaceImport
	sessionDeletionMarkerLegacyWorkspaceImport
)

func deletionMarker(w domain.SessionDeletionWork) sessionDeletionMarker {
	return sessionDeletionMarker{Version: 4, ServerID: w.ServerID, SessionID: w.SessionID, DeletionID: w.DeletionID, MachineID: w.MachineID}
}

func validDeletionMarker(marker sessionDeletionMarker, w domain.SessionDeletionWork) bool {
	if (marker.Version != 3 && marker.Version != 4) || marker.ServerID != w.ServerID || marker.SessionID != w.SessionID || marker.DeletionID != w.DeletionID || marker.MachineID != w.MachineID || len(marker.Obligations) > domain.MaxSessionDeletionJobs {
		return false
	}
	seen := make(map[domain.ID]bool, len(marker.Obligations))
	for _, obligation := range marker.Obligations {
		identity := obligation.Identity
		if identity.Validate() != nil || identity.ServerID != marker.ServerID || identity.SessionID != marker.SessionID || identity.DeletionID != marker.DeletionID || identity.MachineID != marker.MachineID || seen[identity.DeviceID] || len(obligation.Digest) != 64 || obligation.ReportID.Validate() != nil || obligation.Phase != sessionDeletionMarkerInitial && obligation.Phase != sessionDeletionMarkerPublished && obligation.Phase != sessionDeletionMarkerInitialWorkspaceImport && obligation.Phase != sessionDeletionMarkerLegacyWorkspaceImport || obligation.PreviousDigest != "" && (marker.Version != 4 || len(obligation.PreviousDigest) != 64) {
			return false
		}
		seen[identity.DeviceID] = true
	}
	return true
}

func markerObligation(marker *sessionDeletionMarker, w domain.SessionDeletionWork) (*sessionDeletionMarkerObligation, bool) {
	for i := range marker.Obligations {
		if marker.Obligations[i].Identity.DeviceID == w.DeviceID {
			obligation := &marker.Obligations[i]
			if obligation.Identity != w.Identity() {
				return nil, false
			}
			if obligation.Digest == w.Digest() {
				if obligation.PreviousDigest != "" && !w.AcceptsPreviousDigest(obligation.PreviousDigest) {
					return nil, false
				}
				return obligation, true
			}
			if w.AcceptsPreviousDigest(obligation.Digest) && (obligation.PreviousDigest == "" || obligation.PreviousDigest == obligation.Digest) {
				obligation.PreviousDigest = obligation.Digest
				obligation.Digest = w.Digest()
				marker.Version = 4
				return obligation, true
			}
			return nil, false
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

func validPreviousDeletionProof(proof sessionDeletionProof, w domain.SessionDeletionWork) bool {
	return proof.Version == 2 && proof.Identity == w.Identity() && w.AcceptsPreviousDigest(proof.Digest) && proof.ReportID.Validate() == nil && (!proof.Complete || proof.RemovalStarted) && (!proof.WorkspaceRetired || proof.Complete)
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
		if domain.Decode(keyedRaw, &keyed) != nil || !validDeletionProof(keyed, w) && !validPreviousDeletionProof(keyed, w) {
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
			if domain.Decode(legacyRaw, &legacy) != nil || legacy.Identity != (domain.SessionDeletionIdentity{}) || legacy.WorkspaceRetired || legacy.Digest != w.Digest() && !w.AcceptsPreviousDigest(legacy.Digest) || legacy.ReportID.Validate() != nil || legacy.Complete && !legacy.RemovalStarted {
				return pending()
			}
			legacy.Version = 2
			legacy.Identity = w.Identity()
			legacy.Digest = w.Digest()
			expected, _ := json.Marshal(legacy)
			if keyErr == nil {
				if validPreviousDeletionProof(keyed, w) {
					keyed.Digest = w.Digest()
				}
				keyedExpected, err := json.Marshal(keyed)
				if err != nil || !bytes.Equal(keyedExpected, expected) {
					return pending()
				}
			}
			hasLegacyWorkspaceProof := false
			if sessionDeletionUsesWorkspace(w) {
				var err error
				hasLegacyWorkspaceProof, err = workspace.ValidateFreshDeletionWorkspaceProof(root, w)
				if err != nil || legacy.RemovalStarted && !hasLegacyWorkspaceProof {
					return pending()
				}
			}
			if writeJSON(paths.Worker, legacy) != nil || security.SyncParent(paths.Worker) != nil {
				return pending()
			}
			proof = legacy
			var migrationMarkerRaw []byte
			if hasLegacyWorkspaceProof {
				marker := deletionMarker(w)
				marker.Obligations = []sessionDeletionMarkerObligation{{Identity: w.Identity(), Digest: w.Digest(), ReportID: legacy.ReportID, Phase: sessionDeletionMarkerLegacyWorkspaceImport}}
				migrationMarkerRaw, err = json.Marshal(marker)
				if err != nil {
					return pending()
				}
				if writeDeletionMarker(markerPath, marker) != nil {
					return pending()
				}
				if workspace.ImportLegacyDeletionWorkspaceProof(root, w, legacy.RemovalStarted || legacy.Complete) != nil {
					return pending()
				}
			} else if workspace.RequireNoLegacyDeletionWorkspaceProof(root, w) != nil || workspace.RequireNoKeyedDeletionWorkspaceProof(root, w) != nil {
				return pending()
			}
			again, err := security.ReadPrivate(markerPath, domain.MaxSessionDeletionBytes)
			if hasLegacyWorkspaceProof {
				if err != nil || !bytes.Equal(again, migrationMarkerRaw) {
					return pending()
				}
			} else if err != nil || !bytes.Equal(again, legacyRaw) {
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
			if !validDeletionProof(keyed, w) {
				if !validPreviousDeletionProof(keyed, w) {
					return pending()
				}
				keyed.Digest = w.Digest()
				if writeJSON(paths.Worker, keyed) != nil || security.SyncParent(paths.Worker) != nil {
					return pending()
				}
			}
			proof = keyed
			marker = deletionMarker(w)
			marker.Obligations = []sessionDeletionMarkerObligation{{Identity: w.Identity(), Digest: w.Digest(), ReportID: keyed.ReportID, Phase: sessionDeletionMarkerPublished}}
			if writeDeletionMarker(markerPath, marker) != nil {
				return pending()
			}
			if w.PreviousDigest != "" && workspace.MigrateKeyedDeletionWorkspaceProof(root, w, keyed.RemovalStarted && !keyed.WorkspaceRetired) != nil {
				return pending()
			}
			if workspace.RequireNoLegacyDeletionWorkspaceProof(root, w) != nil {
				return pending()
			}
		case 3, 4:
			var marker sessionDeletionMarker
			if domain.Decode(legacyRaw, &marker) != nil || !validDeletionMarker(marker, w) {
				return pending()
			}
			obligation, exists := markerObligation(&marker, w)
			if !exists {
				return pending()
			}
			if keyErr == nil && keyed.ReportID != obligation.ReportID {
				return pending()
			}
			migratingDigest := obligation.PreviousDigest != ""
			if migratingDigest && writeDeletionMarker(markerPath, marker) != nil {
				return pending()
			}
			if obligation.Phase == sessionDeletionMarkerInitialWorkspaceImport && !sessionDeletionUsesWorkspace(w) {
				return pending()
			}
			if obligation.Phase == sessionDeletionMarkerLegacyWorkspaceImport && (!sessionDeletionUsesWorkspace(w) || keyErr != nil) {
				return pending()
			}
			if keyErr == nil {
				if !validDeletionProof(keyed, w) {
					if !validPreviousDeletionProof(keyed, w) {
						return pending()
					}
					keyed.Digest = w.Digest()
					if writeJSON(paths.Worker, keyed) != nil || security.SyncParent(paths.Worker) != nil {
						return pending()
					}
				}
				proof = keyed
				if w.PreviousDigest != "" && obligation.Phase != sessionDeletionMarkerInitial && obligation.Phase != sessionDeletionMarkerInitialWorkspaceImport && obligation.Phase != sessionDeletionMarkerLegacyWorkspaceImport {
					requireExisting := keyed.RemovalStarted && !keyed.WorkspaceRetired
					if workspace.MigrateKeyedDeletionWorkspaceProof(root, w, requireExisting) != nil {
						return pending()
					}
				}
				if obligation.Phase == sessionDeletionMarkerInitial {
					if workspace.RequireNoKeyedDeletionWorkspaceProof(root, w) != nil {
						return pending()
					}
					obligation.Phase = sessionDeletionMarkerPublished
					if writeDeletionMarker(markerPath, marker) != nil {
						return pending()
					}
				} else if obligation.Phase == sessionDeletionMarkerInitialWorkspaceImport || obligation.Phase == sessionDeletionMarkerLegacyWorkspaceImport {
					requireExisting := keyed.RemovalStarted || keyed.Complete
					if !sessionDeletionUsesWorkspace(w) || workspace.RecoverInitialDeletionWorkspaceProof(root, w, requireExisting) != nil {
						return pending()
					}
					obligation.Phase = sessionDeletionMarkerPublished
					if writeDeletionMarker(markerPath, marker) != nil {
						return pending()
					}
				}
			} else {
				if obligation.Phase == sessionDeletionMarkerPublished {
					return pending()
				}
				if obligation.Phase == sessionDeletionMarkerLegacyWorkspaceImport {
					return pending()
				}
				if obligation.Phase == sessionDeletionMarkerInitial && workspace.RequireNoKeyedDeletionWorkspaceProof(root, w) != nil {
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
				if obligation.Phase == sessionDeletionMarkerInitialWorkspaceImport && workspace.RecoverInitialDeletionWorkspaceProof(root, w, false) != nil {
					return pending()
				}
				obligation.Phase = sessionDeletionMarkerPublished
				if !migratingDigest && writeDeletionMarker(markerPath, marker) != nil {
					return pending()
				}
			}
			if migratingDigest {
				obligation.PreviousDigest = ""
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
		hasLegacyWorkspaceProof := false
		if sessionDeletionUsesWorkspace(w) {
			var err error
			hasLegacyWorkspaceProof, err = workspace.ValidateFreshDeletionWorkspaceProof(root, w)
			if err != nil {
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
		if hasLegacyWorkspaceProof {
			obligation.Phase = sessionDeletionMarkerInitialWorkspaceImport
		}
		proof.ReportID = obligation.ReportID
		if writeDeletionMarker(markerPath, marker) != nil || writeJSON(paths.Worker, proof) != nil || security.SyncParent(paths.Worker) != nil {
			return pending()
		}
		if hasLegacyWorkspaceProof && workspace.RecoverInitialDeletionWorkspaceProof(root, w, false) != nil {
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
	if !sessionDeletionUsesWorkspace(w) && workspace.RequireNoKeyedDeletionWorkspaceProof(root, w) != nil {
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
