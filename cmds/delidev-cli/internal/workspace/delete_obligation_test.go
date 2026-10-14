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

func absentObligation(serverID, sessionID, machineID domain.ID, digest string) domain.SessionDeletionWork {
	return domain.SessionDeletionWork{Version: 1, DeletionID: domain.NewID(), ServerID: serverID, SessionID: sessionID, MachineID: machineID, DeviceID: domain.NewID(), Copies: []domain.SessionDeletionCopy{{JobID: domain.NewID(), Type: domain.PrepareWorkspaceJob, Revision: 2, Digest: strings.Repeat("ab", 32), InstanceID: domain.NewID()}}, PreparationDigests: []string{digest}}
}

// A failed-only preparation never owned a workspace. Its absent-root cleanup
// finishes locally but must not publish removal evidence that a later
// successful-preparation obligation could mistake for original removal proof.
func TestAbsentFailedOnlyCleanupPublishesNoSharedRemovalEvidence(t *testing.T) {
	ctx := context.Background()
	m := manager(t)
	session, machine, server := domain.NewID(), domain.NewID(), domain.NewID()
	if err := m.initialize(); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(m.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	failed := absentObligation(server, session, machine, strings.Repeat("ab", 32))
	strict := absentObligation(server, session, machine, strings.Repeat("cd", 32))
	evidencePath := sharedWorkspaceRemovalPath(m.Root, failed)
	copies := 0
	for i := 0; i < 2; i++ {
		// A reconstructed Manager must repeat the cleanup idempotently.
		mm := &Manager{Root: m.Root, Logger: m.Logger}
		if err := mm.DeleteOwnedWorkspace(ctx, failed, true, func() error { copies++; return nil }); err != nil {
			t.Fatal("failed-only cleanup", i, err)
		}
		if _, err := os.Lstat(evidencePath); !os.IsNotExist(err) {
			t.Fatal("shared removal evidence was published", i, err)
		}
		if _, err := os.Lstat(filepath.Join(m.Root, "workspaces", string(session))); !os.IsNotExist(err) {
			t.Fatal("workspace was fabricated", i, err)
		}
	}
	if copies != 2 {
		t.Fatal("failed-only cleanup did not finish its own copy cleanup on each replay", copies)
	}
	called := false
	if err := (&Manager{Root: m.Root, Logger: m.Logger}).DeleteOwnedWorkspace(ctx, strict, false, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("strict obligation adopted absence without original proof", err)
	}
	if _, err := os.Lstat(evidencePath); !os.IsNotExist(err) {
		t.Fatal("pending strict obligation fabricated evidence", err)
	}
}

// Real owned removal still publishes durable shared evidence, and unreadable or
// foreign evidence keeps later obligations pending.
func TestOwnedRemovalPublishesEvidenceAndUnreadableEvidenceStaysPending(t *testing.T) {
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
	server := domain.NewID()
	first := absentObligation(server, source.SessionID, source.MachineID, source.InputDigest)
	second := absentObligation(server, source.SessionID, source.MachineID, strings.Repeat("cd", 32))
	if err := m.DeleteOwnedWorkspace(ctx, first, true, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	evidencePath := sharedWorkspaceRemovalPath(m.Root, first)
	if _, err := os.Lstat(evidencePath); err != nil {
		t.Fatal("owned removal did not publish evidence", err)
	}
	// Unreadable evidence leaves the obligation pending.
	good, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evidencePath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := (&Manager{Root: m.Root, Logger: m.Logger}).DeleteOwnedWorkspace(ctx, second, false, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("unreadable evidence was adopted", err)
	}
	if err := os.WriteFile(evidencePath, good, 0o600); err != nil {
		t.Fatal(err)
	}
	// A reappearing root is not an absent workspace; the obligation stays pending.
	if err := os.MkdirAll(filepath.Join(m.Root, "workspaces", string(source.SessionID)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (&Manager{Root: m.Root, Logger: m.Logger}).DeleteOwnedWorkspace(ctx, second, false, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("reappearing root was adopted", err)
	}
}

// Version-1 shared evidence was also written by older Workers for an absent
// failed-only workspace, so an upgraded Worker must not adopt it. A genuine
// removal replaces it with version-2 evidence that later obligations accept.
func TestLegacyVersionOneRemovalEvidenceIsIgnored(t *testing.T) {
	ctx := context.Background()
	m := manager(t)
	if err := m.initialize(); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(m.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	session, machine, server := domain.NewID(), domain.NewID(), domain.NewID()
	strict := absentObligation(server, session, machine, strings.Repeat("cd", 32))
	evidencePath := sharedWorkspaceRemovalPath(m.Root, strict)
	legacy := []byte(`{"version":1,"server_id":"` + string(server) + `","session_id":"` + string(session) + `","machine_id":"` + string(machine) + `","removed":true}`)
	if err := security.WriteAtomic(evidencePath, legacy); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := (&Manager{Root: m.Root, Logger: m.Logger}).DeleteOwnedWorkspace(ctx, strict, false, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("version-1 removal evidence was adopted", err)
	}
	if err := writeSharedWorkspaceRemoval(m.Root, strict); err != nil {
		t.Fatal(err)
	}
	if err := (&Manager{Root: m.Root, Logger: m.Logger}).DeleteOwnedWorkspace(ctx, strict, false, func() error { called = true; return nil }); err != nil || !called {
		t.Fatal("version-2 removal evidence was rejected", err)
	}
}
