// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestDeleteFailedSubscriptionCleansOriginalLogin(t *testing.T) {
	f, _ := legacyFailedServerLogin(t, false)
	ref := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
	if _, err := f.secrets.Put(context.Background(), ref, []byte("synthetic-staged-token")); err != nil {
		t.Fatal(err)
	}
	request := f.failedLoginDeletion()
	if err := f.deleteFailedLogin(request); err != nil {
		t.Fatal(err)
	}
	if _, err := f.secrets.Get(context.Background(), ref); err == nil {
		t.Fatal("protected credentials retained")
	}
	if _, err := f.service.Store.Get(failedLoginContext(), domain.AccountKind, f.input.AccountID); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("account remains", err)
	}
	if err := f.deleteFailedLogin(request); err != nil {
		t.Fatal("original revision receipt could not replay", err)
	}
	request.Mutation.ExpectedRevision++
	if err := f.deleteFailedLogin(request); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("changed public command replayed", err)
	}
}

func TestDeleteFailedSubscriptionTerminalVaultFailureSurvivesRestart(t *testing.T) {
	f, _ := legacyFailedServerLogin(t, false)
	ref := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
	if _, err := f.secrets.Put(context.Background(), ref, []byte("synthetic-staged-token")); err != nil {
		t.Fatal(err)
	}
	f.secrets.deleteError = domain.Fail(domain.Unavailable, "Fixture vault unavailable.", "Retry explicit cleanup.")
	request := f.failedLoginDeletion()
	if err := f.deleteFailedLogin(request); err == nil {
		t.Fatal("vault failure permitted deletion")
	}
	root := f.service.Store.Root()
	if err := f.service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(failedLoginContext(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	f.service.Store = reopened
	f.secrets.deleteError = nil
	if err := f.deleteFailedLogin(request); err == nil {
		t.Fatal("terminal failure retried vault cleanup")
	}
	if _, err := f.secrets.Get(context.Background(), ref); err != nil {
		t.Fatal("original failed request deleted credentials on retry", err)
	}
	if err := f.deleteFailedLogin(f.failedLoginDeletion()); err != nil {
		t.Fatal("fresh explicit deletion failed", err)
	}
}

func TestDeleteFailedSubscriptionCompletesScopedCleanupWithUnknownNative(t *testing.T) {
	f, _ := legacyFailedServerLogin(t, true)
	request := f.failedLoginDeletion()
	if err := f.deleteFailedLogin(request); err != nil {
		t.Fatal("scoped cleanup retained deletion for an unknown native observation", err)
	}
	if _, err := f.service.Store.Get(failedLoginContext(), domain.AccountKind, f.input.AccountID); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("completed scoped cleanup retained the account", err)
	}
	if f.secrets.enumerations == 0 {
		t.Fatal("scoped cleanup skipped protected-reference inspection")
	}
	if err := f.deleteFailedLogin(request); err != nil {
		t.Fatal("original deletion receipt did not replay", err)
	}
}

func TestDeleteFailedSubscriptionBindsOriginalActorAndConfirmation(t *testing.T) {
	f, _ := legacyFailedServerLogin(t, false)
	ref := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
	if _, err := f.secrets.Put(context.Background(), ref, []byte("synthetic-staged-token")); err != nil {
		t.Fatal(err)
	}
	f.secrets.deleteError = domain.Fail(domain.Unavailable, "Fixture vault unavailable.", "Retry explicit cleanup.")
	request := f.failedLoginDeletion()
	if err := f.deleteFailedLogin(request); err == nil {
		t.Fatal("vault failure permitted deletion")
	}
	client := domain.NewID()
	_, err := f.service.Store.Mutate(failedLoginContext(), domain.NewID(), "fixture.cleanup.actor", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.DeviceKind, client, 0, "", "", domain.Device{Name: "Cleanup client", Type: domain.ClientDevice, PairedAt: time.Now().UTC()})
	})
	if err != nil {
		t.Fatal(err)
	}
	f.secrets.deleteError = nil
	other := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: client})
	if _, err := f.service.DeleteConfiguration(other, connect.NewRequest(request)); err == nil {
		t.Fatal("different actor adopted original deletion")
	}
	_, err = f.service.Store.Mutate(failedLoginContext(), domain.ID(request.Mutation.RequestId), "fixture.different.command", nil, func(tx *store.Tx) (any, error) {
		t.Fatal("reserved public deletion ID reached another command")
		return struct{}{}, nil
	})
	if domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("original request ID was not reserved", err)
	}
	if _, _, err := f.service.Store.Replay(failedLoginContext(), domain.ID(request.Mutation.RequestId), "fixture.different.command", nil); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("different command ignored pending reservation", err)
	}
	current := f.failedLoginDeletion()
	current.Mutation.RequestId = request.Mutation.RequestId
	if err := f.deleteFailedLogin(current); err == nil {
		t.Fatal("changed confirmed revision adopted original deletion")
	}
	if _, err := f.secrets.Get(context.Background(), ref); err != nil {
		t.Fatal("mismatched request repeated cleanup", err)
	}
	// A stale fresh request cannot gain the new cleanup checkpoint revision.
	stale := &pb.DeleteConfigurationRequest{Kind: request.Kind, Mutation: &pb.Mutation{Id: request.Mutation.Id, ExpectedRevision: request.Mutation.ExpectedRevision, RequestId: string(domain.NewID())}}
	if err := f.deleteFailedLogin(stale); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale confirmation admitted", err)
	}
	if err := f.deleteFailedLogin(f.failedLoginDeletion()); err != nil {
		t.Fatal(err)
	}
}
