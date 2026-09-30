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

func TestForkLocalRejectsParentOwnedWorktrees(t *testing.T) {
	m := manager(t)
	request, _ := requestFor(repository(t))
	source, err := m.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	child := domain.NewID()
	if _, err := m.ForkPreparation(context.Background(), source, child, domain.Local); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("Local fork borrowed the parent's removable worktree", err)
	}
	if _, err := os.Stat(filepath.Join(m.Root, "workspaces", string(child))); !os.IsNotExist(err) {
		t.Fatal("rejected Local fork created child metadata", err)
	}
	if _, err := os.Stat(filepath.Join(source.PrimaryPath, "tracked.txt")); err != nil {
		t.Fatal("rejection changed the parent workspace", err)
	}
}

func TestForkLocalUserCheckoutSurvivesParentDeletion(t *testing.T) {
	m := manager(t)
	checkout := repository(t)
	request, _ := requestFor(checkout)
	request.Type, request.OriginMachineID = domain.Local, request.MachineID
	request.Repositories[0].Starting = domain.Reference{}
	source, err := m.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	fork, err := m.ForkPreparation(context.Background(), source, domain.NewID(), domain.Local)
	if err != nil {
		t.Fatal("user-owned Local source rejected", err)
	}
	snapshot, err := m.InspectForkSnapshot(context.Background(), source, fork)
	if err != nil {
		t.Fatal(err)
	}
	child, err := m.PrepareFork(context.Background(), fork, snapshot)
	if err != nil || child.Repositories[0].Owned || child.PrimaryPath != source.PrimaryPath {
		t.Fatal("Local fork copied or took ownership of the original checkout", err)
	}
	if err := security.PrivateDir(filepath.Join(m.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	work := domain.SessionDeletionWork{Version: 1, DeletionID: domain.NewID(), ServerID: domain.NewID(), SessionID: source.SessionID, MachineID: source.MachineID, DeviceID: domain.NewID(), Copies: []domain.SessionDeletionCopy{{JobID: domain.NewID(), Type: domain.PrepareWorkspaceJob, Revision: 2, Digest: strings.Repeat("ab", 32), InstanceID: domain.NewID()}}, PreparationDigests: []string{source.InputDigest}}
	if err := m.DeleteOwnedWorkspace(context.Background(), work, false, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Read(child.SessionID); err != nil {
		t.Fatal("parent deletion invalidated Local child metadata", err)
	}
	if raw, err := os.ReadFile(filepath.Join(child.PrimaryPath, "tracked.txt")); err != nil || len(raw) == 0 {
		t.Fatal("parent deletion removed the shared user checkout", err)
	}
	changed := source
	changed.Repositories = append([]PreparedRepository(nil), source.Repositories...)
	changed.Repositories[0].Owned = true
	if err := ValidateLocalForkSource(changed); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("Local label hid a parent-owned checkout", err)
	}
}
