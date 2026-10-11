// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestPermanentDeletionLegacyObligationProofRecovery(t *testing.T) {
	for _, scenario := range []string{"legacy-only", "matching-pair", "foreign-device", "conflicting-pair", "restored-content", "not-removed"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"locks", "session-deletions"} {
				if err := security.PrivateDir(filepath.Join(root, name)); err != nil {
					t.Fatal(err)
				}
			}
			w := domain.SessionDeletionWork{Version: 1, DeletionID: domain.NewID(), ServerID: domain.NewID(), SessionID: domain.NewID(), MachineID: domain.NewID(), DeviceID: domain.NewID(), Copies: []domain.SessionDeletionCopy{{JobID: domain.NewID(), Type: domain.PrepareWorkspaceJob, Revision: 2, Digest: strings.Repeat("a", 64), InstanceID: domain.NewID()}}}
			original := deletionWorkspaceProof{Version: 1, Digest: w.Digest(), Removed: true}
			if scenario == "foreign-device" {
				other := w
				other.DeviceID = domain.NewID()
				original.Digest = other.Digest()
			}
			if scenario == "restored-content" {
				original.Manifest = &Manifest{SessionID: w.SessionID, MachineID: w.MachineID}
			}
			if scenario == "not-removed" {
				original.Removed = false
			}
			legacy := filepath.Join(root, "session-deletions", string(w.SessionID)+"-workspace.json")
			keyed := filepath.Join(root, "session-deletions", w.ObligationKey()+"-workspace.json")
			if err := writeDeletionWorkspaceProof(legacy, original); err != nil {
				t.Fatal(err)
			}
			if scenario == "matching-pair" || scenario == "conflicting-pair" {
				retained := original
				if scenario == "conflicting-pair" {
					retained.Removed = false
				}
				if err := writeDeletionWorkspaceProof(keyed, retained); err != nil {
					t.Fatal(err)
				}
			}
			err := CheckDeletionProofAbsence(root, w)
			if scenario == "legacy-only" || scenario == "matching-pair" {
				if err != nil {
					t.Fatal("exact interrupted proof could not recover", err)
				}
				if err := CheckDeletionProofAbsence(root, w); err != nil {
					t.Fatal("recovered proof was not replayable", err)
				}
			} else if domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("incomplete or foreign workspace proof acknowledged", err)
			}
			if scenario == "foreign-device" {
				if _, err := os.Lstat(keyed); !os.IsNotExist(err) {
					t.Fatal("foreign proof minted obligation evidence", err)
				}
			}
		})
	}
}
