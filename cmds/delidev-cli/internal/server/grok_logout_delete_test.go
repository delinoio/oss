// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// grokDeletion is a connected Grok account that no Agent or session references,
// so only its credential ownership decides whether it can be deleted.
type grokDeletion struct {
	*grokLogoutRun
	configuration delidevv1connect.ConfigurationServiceClient
}

func newGrokDeletion(t *testing.T) *grokDeletion {
	t.Helper()
	g := newGrokLogout(t)
	_, account := g.f.record()
	id := domain.NewID()
	clone := account
	clone.Alias = "unreferenced grok"
	clone.Connection, clone.Subscription, clone.Health = nil, nil, domain.AccountDisconnected
	_, err := g.f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.deletion", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.AccountKind, id, 0, "", "", clone)
	})
	if err != nil {
		t.Fatal(err)
	}
	g.f.input.AccountID = id
	g.generation = connectGrokAccount(t, g.f, id, g.idp)
	return &grokDeletion{grokLogoutRun: g, configuration: delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, g.f.http.URL)}
}

func (d *grokDeletion) remove(ctx context.Context, request *pb.DeleteConfigurationRequest) error {
	_, err := d.configuration.DeleteConfiguration(ctx, subscriptionRequest(d.f.service.Identity.Token, request))
	return err
}

func (d *grokDeletion) request() *pb.DeleteConfigurationRequest {
	r, _ := d.f.record()
	return &pb.DeleteConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, Mutation: &pb.Mutation{Id: string(r.ID), ExpectedRevision: r.Revision, RequestId: string(domain.NewID())}}
}

func (d *grokDeletion) exists() bool {
	err := d.f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		_, _, err := accountFromTx(tx, d.f.input.AccountID, 0)
		return err
	})
	return err == nil
}

func TestGrokAccountDeletionPerformsLogoutFirst(t *testing.T) {
	d := newGrokDeletion(t)
	old := d.stored()
	request := d.request()
	if err := d.remove(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if d.exists() {
		t.Fatal("the connected account survived its deletion")
	}
	if d.idp.RevokeRequests != 1 || !d.idp.RefreshRevoked() || d.idp.RevokeForms[0].Get("token") != old.RefreshToken() {
		t.Fatal("deleting a connected Grok account must revoke its refresh token first")
	}
	if refs, err := d.f.secrets.UnremovedReferences(context.Background(), d.f.input.AccountID); err != nil || len(refs) != 0 {
		t.Fatalf("the deleted account retained protected references: %v, %v", refs, err)
	}
	// The exact deletion request replays without another logout.
	if err := d.remove(context.Background(), request); err != nil || d.idp.RevokeRequests != 1 {
		t.Fatalf("deletion replay = %v, revocation requests = %d", err, d.idp.RevokeRequests)
	}
	logs := d.logText()
	if !strings.Contains(logs, "grok_delete_logout_started") || !strings.Contains(logs, "grok_delete_logout_completed") || !strings.Contains(logs, `"phase":"LOGOUT"`) {
		t.Fatal("the logout-before-delete was not logged with the LOGOUT phase")
	}
}

func TestGrokAccountDeletionIsBlockedByAnUncertainLogout(t *testing.T) {
	for name, edit := range map[string]func(*grokDeletion){
		"server error": func(d *grokDeletion) { d.idp.RevokeStatus = http.StatusInternalServerError },
		"client error": func(d *grokDeletion) {
			d.idp.RevokeStatus, d.idp.RevokeError = http.StatusBadRequest, "invalid_request"
		},
		"revoke timeout": func(d *grokDeletion) {
			d.f.service.grokLoginTransport = &failingRevokeTransport{base: d.idp.Transport()}
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := newGrokDeletion(t)
			old := d.stored()
			edit(d)
			err := d.remove(context.Background(), d.request())
			if err == nil || domain.SafeError(rpc.ClientError(err)).Code != domain.RecoveryRequired {
				t.Fatalf("an uncertain logout allowed or mis-reported the deletion: %v", err)
			}
			a := d.account()
			if !d.exists() || a.Connection == nil || !a.Subscription.RecoveryRequired || !d.stored().Equal(old) || len(d.f.references()) != 1 {
				t.Fatal("an uncertain logout must retain the account, the fence and the bundle")
			}
			// Deletion is refused again by the recovery fence; nothing is resent.
			if err := d.remove(context.Background(), d.request()); err == nil {
				t.Fatal("a fenced account was deleted")
			}
			if d.idp.RevokeRequests > 1 {
				t.Fatalf("revocation requests = %d", d.idp.RevokeRequests)
			}
		})
	}
}

func TestGrokAccountDeletionWaitsForTheExecutionExit(t *testing.T) {
	d := newGrokDeletion(t)
	d.executionLease()
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	if err := d.remove(ctx, d.request()); err == nil {
		t.Fatal("an account still leased by an execution was deleted")
	}
	a := d.account()
	if !d.exists() || a.Connection == nil || d.idp.RevokeRequests != 0 || len(d.f.references()) != 1 || a.Subscription.Lease == nil {
		t.Fatal("the deletion acted while the original execution held the account")
	}
	if !grokAccountLoggingOut(a) {
		t.Fatal("the queued logout must keep new work refused")
	}
	// After the exit the queued logout completes and the deletion can be repeated.
	d.releaseLease()
	d.scan(2 * time.Second)
	if err := d.remove(context.Background(), d.request()); err != nil {
		t.Fatal(err)
	}
	if d.exists() {
		t.Fatal("the account survived the repeated deletion")
	}
}

func TestGrokAccountDeletionIsBlockedByUnconfirmedCredentialRemoval(t *testing.T) {
	d := newGrokDeletion(t)
	d.f.secrets.mu.Lock()
	d.f.secrets.deleteError = errors.New("vault unavailable")
	d.f.secrets.mu.Unlock()
	if err := d.remove(context.Background(), d.request()); err == nil {
		t.Fatal("deletion was acknowledged while the credential removal was unconfirmed")
	}
	if !d.exists() || len(d.f.references()) != 1 {
		t.Fatal("the account or its protected reference vanished before confirmation")
	}
	a := d.account()
	if a.Connection != nil || !a.Subscription.RecoveryRequired || !serverLogoutRecoveryCandidate(a) {
		t.Fatal("the account must keep the logout-cleanup recovery shape")
	}
	// Once the vault works again an explicit deletion finishes the cleanup first.
	d.f.secrets.mu.Lock()
	d.f.secrets.deleteError = nil
	d.f.secrets.mu.Unlock()
	if err := d.remove(context.Background(), d.request()); err != nil {
		t.Fatal(err)
	}
	if d.exists() || len(d.f.references()) != 0 || d.idp.RevokeRequests != 1 {
		t.Fatal("the confirmed cleanup did not complete the deletion without another revocation")
	}
}

func TestGrokAccountDeletionOfADisconnectedAccountSendsNoRevocation(t *testing.T) {
	d := newGrokDeletion(t)
	if p := d.settle(); p.State != grokSucceeds {
		t.Fatalf("logout = %v", p.State)
	}
	if err := d.remove(context.Background(), d.request()); err != nil {
		t.Fatal(err)
	}
	if d.exists() || d.idp.RevokeRequests != 1 {
		t.Fatal("deleting an already logged out account sent another revocation")
	}
}

// The logout of a deletion advances the account revision. A retry of the same
// deletion request after the process stopped before the deletion receipt was
// committed, or after a later deletion step failed, must continue at the current
// revision instead of conflicting with the request's old one, and must not send
// another revocation.
func TestGrokAccountDeletionRetryContinuesAfterItsOwnLogout(t *testing.T) {
	t.Run("stopped before the receipt", func(t *testing.T) {
		d := newGrokDeletion(t)
		request := d.request()
		revision, err := d.f.service.logoutGrokBeforeDelete(d.ownerContext(), d.f.input.AccountID, request.Mutation.ExpectedRevision, domain.ID(request.Mutation.RequestId))
		if err != nil || revision <= request.Mutation.ExpectedRevision {
			t.Fatalf("the logout did not advance the revision: %d, %v", revision, err)
		}
		if err := d.remove(context.Background(), request); err != nil {
			t.Fatalf("the retry of the same request conflicted with its own logout: %v", err)
		}
		if d.exists() || d.idp.RevokeRequests != 1 {
			t.Fatalf("exists = %v, revocation requests = %d", d.exists(), d.idp.RevokeRequests)
		}
	})
	t.Run("completed through the logout cleanup child", func(t *testing.T) {
		d := newGrokDeletion(t)
		request := d.request()
		d.f.secrets.mu.Lock()
		d.f.secrets.deleteError = errors.New("vault unavailable")
		d.f.secrets.mu.Unlock()
		// The logout publishes the disconnected account but cannot remove the
		// vault generations, so it retains the logout-cleanup recovery shape.
		if _, err := d.f.service.logoutGrokBeforeDelete(d.ownerContext(), d.f.input.AccountID, request.Mutation.ExpectedRevision, domain.ID(request.Mutation.RequestId)); err != nil {
			t.Fatal(err)
		}
		if a := d.account(); a.Connection != nil || !serverLogoutRecoveryCandidate(a) {
			t.Fatal("the account must keep the logout-cleanup recovery shape")
		}
		d.f.secrets.mu.Lock()
		d.f.secrets.deleteError = nil
		d.f.secrets.mu.Unlock()
		if err := d.remove(context.Background(), request); err != nil {
			t.Fatalf("the retry of the same request conflicted with its own logout: %v", err)
		}
		if d.exists() || len(d.f.references()) != 0 || d.idp.RevokeRequests != 1 {
			t.Fatalf("exists = %v, references = %d, revocation requests = %d", d.exists(), len(d.f.references()), d.idp.RevokeRequests)
		}
		// The receipt keeps the revision the caller sent, so the exact request
		// still replays after the cleanup child completed the deletion.
		if err := d.remove(context.Background(), request); err != nil || d.idp.RevokeRequests != 1 {
			t.Fatalf("deletion replay = %v, revocation requests = %d", err, d.idp.RevokeRequests)
		}
	})
	t.Run("another operation is not adopted", func(t *testing.T) {
		d := newGrokDeletion(t)
		if p := d.settle(); p.State != grokSucceeds {
			t.Fatalf("logout = %v", p.State)
		}
		stale := d.request()
		d.f.mutateAccount(t, func(a *domain.Account) { a.Alias = "renamed after the read" })
		if err := d.remove(context.Background(), stale); err == nil {
			t.Fatal("a stale expected revision was accepted for a logout the request did not run")
		}
	})
}

// A rejected deletion must not irreversibly break an account that has to stay in
// use: the references are checked before the logout revokes the token.
func TestGrokAccountDeletionRefusedByReferencesRevokesNothing(t *testing.T) {
	d := newGrokDeletion(t)
	old := d.stored()
	_, err := d.f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.reference", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.AgentKind, domain.NewID(), 0, "", "", domain.Agent{Name: "Referencing agent", Accounts: []domain.WeightedAccount{{ID: d.f.input.AccountID, Weight: 1}}})
	})
	if err != nil {
		t.Fatal(err)
	}
	err = d.remove(context.Background(), d.request())
	if err == nil || domain.SafeError(rpc.ClientError(err)).Code != domain.Conflict {
		t.Fatalf("a referenced account deletion = %v", err)
	}
	a := d.account()
	if !d.exists() || a.Connection == nil || a.Health != domain.AccountReady || a.Subscription.Pending != nil || a.Subscription.RecoveryRequired || !d.stored().Equal(old) || len(d.f.references()) != 1 {
		t.Fatal("a refused deletion changed the connected account")
	}
	if d.idp.RevokeRequests != 0 {
		t.Fatalf("a refused deletion revoked the token (%d requests)", d.idp.RevokeRequests)
	}
	if !strings.Contains(d.logText(), "account_referenced") {
		t.Fatal("the refusal was not logged")
	}
}
