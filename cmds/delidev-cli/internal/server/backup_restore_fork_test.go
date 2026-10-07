// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestBackupRestoreRefusesClaimedForkWithoutChangingOwnership(t *testing.T) {
	f, _, accepted := acceptedForkFixture(t)
	forkClaimFixture(t, f, accepted.Job.Id)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	s := f.service.Store
	if err := s.BindIdentity(ctx, f.identity.ServerID); err != nil {
		t.Fatal(err)
	}
	backup, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := s.InspectBackup(ctx, backup, f.identity.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := s.RestoreRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	request := &pb.RestoreBackupRequest{RequestId: string(domain.NewID()), Backup: backupMessage(inspection.Backup), Sha256: inspection.SHA256, ExpectedRestoreRevision: &revision, Confirm: true}
	path := filepath.Join(s.Root(), "backups", string(backup)+".sqlite")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RestoreBackup(ctx, connect.NewRequest(request)); err != nil {
		t.Fatal("claimed fork blocked restore", err)
	}
	if !s.RestoreFrozen() || !f.service.stopping.Load() {
		t.Fatal("restore did not fence the replaced epoch")
	}
	retained, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, retained) {
		t.Fatal("restore changed backup history", err)
	}
}

func TestBackupRestorePreservesPublishedForkMetadataWithoutExecutionAuthority(t *testing.T) {
	f, child, _ := publishedForkFixture(t)
	var original domain.Session
	if domain.Decode(child.DocumentJson, &original) != nil || original.Fork == nil {
		t.Fatal("fixture has no published fork")
	}
	metadata, err := json.Marshal(original.Fork)
	if err != nil {
		t.Fatal(err)
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	s := f.service.Store
	if err := s.BindIdentity(ctx, f.identity.ServerID); err != nil {
		t.Fatal(err)
	}
	backup, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := s.InspectBackup(ctx, backup, f.identity.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise publication with the original fixture's native authority and
	// Worker stream retired; database restore cannot prove their cleanup.
	f.service.executionAuthority.close()
	f.workerStream.Close()
	f.service.stopping.Store(true)
	revision, err := s.RestoreRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := store.BackupRestoreInput{Backup: inspection.Backup, SHA256: inspection.SHA256, ServerID: f.identity.ServerID, Actor: domain.Principal{Type: domain.OwnerDevice}, ExpectedRevision: revision}
	if _, _, err := s.RestoreBackup(ctx, domain.NewID(), input); err != nil {
		t.Fatal(err)
	}
	root := s.Root()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	record, err := reopened.Get(ctx, domain.SessionKind, domain.ID(child.Id))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := store.Decode[domain.Session](record)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := json.Marshal(restored.Fork)
	if err != nil || !bytes.Equal(metadata, retained) || restored.ExecutionSelection() != original.ExecutionSelection() || restored.Dispatch != domain.DispatchPaused || restored.Recovery != domain.NeedsRecovery || restored.CurrentExecution != nil {
		t.Fatal("restore rewrote fork provenance or revived native authority", err)
	}
}
