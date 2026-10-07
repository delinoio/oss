// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
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

func TestDeleteFailedSubscriptionRetainsUnconfirmedNative(t *testing.T) {
	f, _ := legacyFailedServerLogin(t, true)
	request := f.failedLoginDeletion()
	if err := f.deleteFailedLogin(request); err == nil {
		t.Fatal("unproven native ownership deleted")
	}
	r, a := f.record()
	if r.Revision != request.Mutation.ExpectedRevision || !a.Subscription.RecoveryRequired {
		t.Fatal("uncertainty lost original ownership")
	}
	if err := f.deleteFailedLogin(request); err == nil {
		t.Fatal("terminal uncertain cleanup retried")
	}
}
