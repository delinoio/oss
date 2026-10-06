// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func TestParentDeletionReconcilesOriginalUnpublishedSidechatAfterRestart(t *testing.T) {
	c, w, parent, _ := deletionWorkerFixture(t, domain.GeneralChat)
	m := &workspace.Manager{Root: c.Root, Logger: c.Logger}
	prep := workspace.PrepareRequest{SessionID: w.SessionID, MachineID: w.MachineID, OriginMachineID: w.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}
	fork, child := domain.NewID(), domain.NewID()
	if _, _, err := m.PrepareSidechatReference(context.Background(), fork, child, prep, parent); err != nil {
		t.Fatal(err)
	}
	copy := domain.SessionDeletionCopy{JobID: fork, Type: domain.ForkSessionJob, Revision: 2, Digest: strings.Repeat("a", 64), InstanceID: w.Copies[0].InstanceID, UnpublishedSidechatID: child}
	w.Copies = append(w.Copies, copy)
	// The failed native operation has joined and its original terminal receipt
	// remains retained. Only that immutable job/child scope grants cleanup.
	if err := writeJSON(filepath.Join(c.Root, "jobs", string(fork)+".json"), journal{Version: 1, JobID: fork, InstanceID: copy.InstanceID, Revision: copy.Revision, Digest: copy.Digest, State: journalReported, ReportID: domain.NewID(), Problem: domain.Fail(domain.Unavailable, "fixture rejected native fork", "Inspect original work.")}); err != nil {
		t.Fatal(err)
	}
	proof, err := deleteSessionCopies(context.Background(), c, w)
	if err != nil || !proof.Complete {
		t.Fatal("unpublished reference stranded parent deletion", proof, err)
	}
	for _, path := range []string{filepath.Join(c.Root, "workspaces", string(child)), workspace.SidechatForkClaimPath(c.Root, fork), filepath.Join(c.Root, "workspaces", string(w.SessionID))} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("original deletion retained owned metadata", err)
		}
	}
	if _, err := deleteSessionCopies(context.Background(), c, w); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(c.Root, "workspaces", string(child)), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := deleteSessionCopies(context.Background(), c, w); err == nil {
		t.Fatal("completed proof ignored replaced child metadata")
	}
	if _, err := os.Stat(filepath.Join(c.Root, "workspaces", string(child))); err != nil {
		t.Fatal("proof replay removed replacement", err)
	}
}
