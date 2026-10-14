// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

// Two historical devices on one machine own the same shared workspace
// directory. The first obligation removes it; the second must verify that
// removal from session-scoped evidence instead of staying pending forever.
func TestSecondDeviceObligationVerifiesSharedWorkspaceRemoval(t *testing.T) {
	ctx := context.Background()
	m := manager(t)
	checkout := repository(t)
	request, _ := requestFor(checkout)
	source, err := m.Prepare(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(m.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	serverID := domain.NewID()
	obligation := func(digests ...string) domain.SessionDeletionWork {
		return domain.SessionDeletionWork{Version: 1, DeletionID: domain.NewID(), ServerID: serverID, SessionID: source.SessionID, MachineID: source.MachineID, DeviceID: domain.NewID(), Copies: []domain.SessionDeletionCopy{{JobID: domain.NewID(), Type: domain.PrepareWorkspaceJob, Revision: 2, Digest: strings.Repeat("ab", 32), InstanceID: domain.NewID()}}, PreparationDigests: digests}
	}
	first := obligation(source.InputDigest)
	second := obligation(strings.Repeat("cd", 32))
	if err := m.DeleteOwnedWorkspace(ctx, second, false, func() error { return nil }); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("foreign preparation digest was admitted before removal", err)
	}
	if err := m.DeleteOwnedWorkspace(ctx, first, false, func() error { return nil }); err != nil {
		t.Fatal("first obligation", err)
	}
	if _, err := os.Lstat(filepath.Join(m.Root, "workspaces", string(source.SessionID))); !os.IsNotExist(err) {
		t.Fatal("workspace retained", err)
	}
	copies := 0
	if err := m.DeleteOwnedWorkspace(ctx, second, false, func() error { copies++; return nil }); err != nil || copies != 1 {
		t.Fatal("second obligation did not verify the first removal", err, copies)
	}
	// Replay stays idempotent and obligations never share a workspace proof.
	if err := m.DeleteOwnedWorkspace(ctx, second, false, func() error { return nil }); err != nil {
		t.Fatal("second obligation replay", err)
	}
	// Evidence is bound to the original server, session and machine.
	foreignServer := obligation(strings.Repeat("ef", 32))
	foreignServer.ServerID = domain.NewID()
	foreignSession := obligation(strings.Repeat("ef", 32))
	foreignSession.SessionID = domain.NewID()
	foreignMachine := obligation(strings.Repeat("ef", 32))
	foreignMachine.MachineID = domain.NewID()
	for name, w := range map[string]domain.SessionDeletionWork{"server": foreignServer, "session": foreignSession, "machine": foreignMachine} {
		called := false
		if err := m.DeleteOwnedWorkspace(ctx, w, false, func() error { called = true; return nil }); err == nil || called {
			t.Fatal("removal evidence crossed a "+name+" boundary", err)
		}
	}
}

// The copy callback receives the session owner's maintenance lock object bound
// by the reconciliation itself, and only when that reconciliation ran.
func TestDeleteOwnedWorkspaceWithOwnerBindsReconcilerMaintenanceLock(t *testing.T) {
	for _, ownerIndex := range []bool{false, true} {
		ctx := context.Background()
		m := manager(t)
		request, _ := requestFor(repository(t))
		source, err := m.Prepare(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if err := security.PrivateDir(filepath.Join(m.Root, "session-deletions")); err != nil {
			t.Fatal(err)
		}
		// Preparation may have left a process index; the variants differ only in
		// whether the deletion finds one to reconcile.
		index := filepath.Join(m.Root, "processes", string(source.SessionID))
		if ownerIndex {
			if err := security.PrivateDir(index); err != nil && !os.IsExist(err) {
				t.Fatal(err)
			}
		} else if err := os.RemoveAll(index); err != nil {
			t.Fatal(err)
		}
		w := domain.SessionDeletionWork{Version: 1, DeletionID: domain.NewID(), ServerID: domain.NewID(), SessionID: source.SessionID, MachineID: source.MachineID, DeviceID: domain.NewID(), Copies: []domain.SessionDeletionCopy{{JobID: domain.NewID(), Type: domain.PrepareWorkspaceJob, Revision: 2, Digest: strings.Repeat("ab", 32), InstanceID: domain.NewID()}}, PreparationDigests: []string{source.InputDigest}}
		lock := filepath.Join(m.Root, "processes", string(source.SessionID)+".recovery.lock")
		called := false
		err = m.DeleteOwnedWorkspaceWithOwner(ctx, w, false, func(owner *security.PathIdentity) error {
			called = true
			if owner.Present() != ownerIndex {
				t.Error("owner binding presence does not follow reconciliation", ownerIndex)
			}
			if ownerIndex {
				if !owner.Held() || owner.Record().Kind != security.ObjectRegular || owner.Record().Size != 0 || owner.StillAt(lock) != nil {
					t.Error("owner binding is not the reconciler's empty regular lock")
				}
				taken := owner.Take()
				defer taken.Close()
				if owner.Present() {
					t.Error("Take left the identity in the workspace")
				}
			}
			return nil
		})
		if err != nil || !called {
			t.Fatal(err, called)
		}
	}
}
