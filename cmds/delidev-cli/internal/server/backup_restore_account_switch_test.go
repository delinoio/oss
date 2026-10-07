// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestBackupRestoreRetainsAccountSwitchHistoryWithoutSelectionAuthority(t *testing.T) {
	f, b, _ := accountSwitchFixture(t, domain.FullNativeHistory, true)
	request := switchRequest(f, b, t)
	if _, err := sessionClient(f.accountFixture).SwitchSessionAccount(context.Background(), ownerRequest(f.identity, request)); err != nil {
		t.Fatal(err)
	}
	before, err := store.Decode[domain.Session](f.refresh(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(before.AccountChanges) != 1 {
		t.Fatal("fixture has no accepted account switch")
	}
	original, err := json.Marshal(before.AccountChanges)
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
	// Retire the original fixture's native authority and Worker stream before
	// exercising the stopped-store publication boundary. Its HTTP fixture has
	// no remaining product callers and is reaped by the original cleanup.
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
	record, err := reopened.Get(ctx, domain.SessionKind, domain.ID(request.Mutation.Id))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := store.Decode[domain.Session](record)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := json.Marshal(restored.AccountChanges)
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != string(retained) || restored.ExecutionSelection() != before.ExecutionSelection() || restored.Dispatch != domain.DispatchPaused || restored.Recovery != domain.NeedsRecovery {
		t.Fatal("restore rewrote account history or revived execution authority")
	}
	for _, id := range []domain.ID{domain.ID(f.account.Id), domain.ID(b.Id)} {
		record, err := reopened.Get(ctx, domain.AccountKind, id)
		if err != nil {
			t.Fatal(err)
		}
		account, err := store.Decode[domain.Account](record)
		if err != nil {
			t.Fatal(err)
		}
		if account.Health != domain.AccountDisconnected || account.Connection != nil || account.Validation != nil {
			t.Fatal("restored account retained connection authority")
		}
	}
	service := &Service{Store: reopened, logger: f.service.logger}
	attempted := connect.NewRequest(&pb.SwitchSessionAccountRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(record.ID), ExpectedRevision: record.Revision}, AccountId: f.account.Id})
	if _, err := service.SwitchSessionAccount(ctx, attempted); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatal("quarantined session returned an unexpected account-selection result", err)
	}
	after, err := reopened.Get(ctx, domain.SessionKind, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != record.Revision || string(after.Data) != string(record.Data) {
		t.Fatal("refused account selection changed restored history")
	}
}
