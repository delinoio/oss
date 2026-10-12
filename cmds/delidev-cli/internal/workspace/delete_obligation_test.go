// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestLegacyWorkspaceProofMigratesOnlyForExactObligation(t *testing.T) {
	root := t.TempDir()
	if err := security.PrivateDir(filepath.Join(root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	a := domain.SessionDeletionWork{Version: 1, DeletionID: domain.NewID(), ServerID: domain.NewID(), SessionID: domain.NewID(), MachineID: domain.NewID(), DeviceID: domain.NewID()}
	b := a
	b.DeletionID, b.DeviceID = domain.NewID(), domain.NewID()
	if SessionDeletionObligationName(a) == SessionDeletionObligationName(b) {
		t.Fatal("obligations share a name")
	}
	legacyPath := filepath.Join(root, "session-deletions", string(a.SessionID)+"-workspace.json")
	if err := writeDeletionWorkspaceProof(legacyPath, deletionWorkspaceProof{Version: 1, Digest: a.Digest(), Removed: true}); err != nil {
		t.Fatal(err)
	}
	pathA := filepath.Join(root, "session-deletions", SessionDeletionObligationName(a)+"-workspace.json")
	pathB := filepath.Join(root, "session-deletions", SessionDeletionObligationName(b)+"-workspace.json")
	if err := migrateLegacyWorkspaceProof(root, pathB, b); err == nil {
		t.Fatal("legacy proof for another obligation was admitted")
	}
	if err := migrateLegacyWorkspaceProof(root, pathA, a); err != nil {
		t.Fatal(err)
	}
	raw, err := security.ReadPrivate(pathA, 1<<20)
	var got deletionWorkspaceProof
	if err != nil || domain.Decode(raw, &got) != nil || got.Digest != a.Digest() || !got.Removed {
		t.Fatal("migrated proof lost state", got, err)
	}
	if _, err := os.Lstat(legacyPath); !os.IsNotExist(err) {
		t.Fatal("legacy proof retained after migration", err)
	}
	if err := migrateLegacyWorkspaceProof(root, pathB, b); err != nil {
		t.Fatal(err)
	}
}
