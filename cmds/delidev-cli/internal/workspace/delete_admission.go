// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type workspaceRemovalAdmission struct {
	Version  uint32                         `json:"version"`
	Identity domain.SessionDeletionIdentity `json:"identity"`
	Digest   string                         `json:"digest"`
	Native   *security.RemovalAdmission     `json:"native"`
}

func workspaceRemovalAdmissionPath(proofPath string) string {
	return strings.TrimSuffix(proofPath, ".json") + "-admission.json"
}
func loadWorkspaceRemovalAdmission(path string, w domain.SessionDeletionWork, proof deletionWorkspaceProof) (*security.RemovalAdmission, error) {
	raw, err := security.ReadPrivate(workspaceRemovalAdmissionPath(path), security.MaxRemovalAdmissionBytes)
	if errors.Is(err, os.ErrNotExist) && proof.AdmissionDigest == "" {
		return nil, nil
	}
	var record workspaceRemovalAdmission
	if err != nil || domain.DecodeWithLimit(raw, &record, security.MaxRemovalAdmissionBytes) != nil || record.Version != 1 || record.Identity != w.Identity() || record.Digest != w.Digest() || record.Native.Validate() != nil || proof.AdmissionDigest != "" && workspaceAdmissionDigest(raw) != proof.AdmissionDigest {
		return nil, domain.SessionDeletionPending()
	}
	return record.Native, nil
}
func persistWorkspaceRemovalAdmission(ctx context.Context, root, path string, w domain.SessionDeletionWork, proof *deletionWorkspaceProof, a *security.RemovalAdmission) error {
	if a.CheckTrees(ctx, root) != nil {
		return domain.SessionDeletionPending()
	}
	if proof.AdmissionDigest == "" {
		raw, err := json.Marshal(workspaceRemovalAdmission{1, w.Identity(), w.Digest(), a})
		if err != nil || len(raw) > security.MaxRemovalAdmissionBytes {
			return domain.SessionDeletionPending()
		}
		if err := security.WriteAtomic(workspaceRemovalAdmissionPath(path), raw); err != nil {
			return domain.SessionDeletionPending()
		}
		proof.AdmissionDigest = workspaceAdmissionDigest(raw)
		return writeDeletionWorkspaceProof(path, *proof)
	}
	return nil
}

func workspaceAdmissionDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
