// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestForkChildDeletionAfterParentDeletion(t *testing.T) {
	m := manager(t)
	m.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	checkout := repository(t)
	request, _ := requestFor(checkout)
	source, err := m.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	fork, err := m.ForkPreparation(context.Background(), source, domain.NewID(), domain.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	child, err := m.Prepare(context.Background(), fork)
	if err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(m.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	remove := func(manifest Manifest) {
		t.Helper()
		w := domain.SessionDeletionWork{Version: 1, DeletionID: domain.NewID(), ServerID: domain.NewID(), SessionID: manifest.SessionID, MachineID: manifest.MachineID, DeviceID: domain.NewID(), Copies: []domain.SessionDeletionCopy{{JobID: domain.NewID(), Type: domain.PrepareWorkspaceJob, Revision: 2, Digest: strings.Repeat("ab", 32), InstanceID: domain.NewID()}}, PreparationDigests: []string{manifest.InputDigest}}
		if err := m.DeleteOwnedWorkspace(context.Background(), w, false, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	remove(source)
	if _, err := os.Stat(filepath.Join(child.PrimaryPath, "tracked.txt")); err != nil {
		t.Fatal("parent deletion removed child", err)
	}
	remove(child)
	if _, err := os.Stat(filepath.Join(checkout, "tracked.txt")); err != nil {
		t.Fatal("child cleanup removed original checkout", err)
	}
}
