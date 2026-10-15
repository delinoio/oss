// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// The ownerless cleanup fence (grok_cleanup_fence.go). A Finish that published a
// rotated credential and then failed to remove the superseded generation leaves
// the account fenced with no lease, no pending operation and no active
// operation. The scanner owns that fence:
//
//	Finish that rotated the credential | removal    | account after the Finish          | scanner
//	execute                            | fails      | fenced, no lease, no operation    | removes the old generation, clears the fence, health ready
//	refresh                            | fails      | fenced, no lease, no operation    | same
//	execute                            | still fails| fenced                            | keeps the fence, retries later
func TestGrokRotationCleanupFailureIsOwnedByTheScanner(t *testing.T) {
	type subject struct {
		service *Service
		secrets *accountTestSecrets
		account domain.ID
		record  func() (store.Record, domain.Account)
		rotated domain.ID
	}
	failRemoval := func(secrets *accountTestSecrets) {
		secrets.afterPut = func() {
			secrets.mu.Lock()
			secrets.deleteError = domain.Fail(domain.Unavailable, "The vault removal failed.", "")
			secrets.mu.Unlock()
		}
	}
	for name, setup := range map[string]func(t *testing.T) subject{
		"execute": func(t *testing.T) subject {
			g := newGrokLoginFixture(t)
			lease, _ := g.holdPlainExecution(t)
			rotated := domain.NewID()
			failRemoval(g.secrets)
			if _, err := g.finishWith(lease, rotated, g.grokRotatedEnvelope(rotated), true, false, true); err == nil {
				t.Fatal("a failed removal of the superseded generation was reported as success")
			}
			return subject{g.service, g.secrets, g.input.AccountID, g.record, rotated}
		},
		"refresh": func(t *testing.T) subject {
			f := newGrokFixture(t)
			f.login("synthetic-principal-1")
			refresh := pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH
			lease, err := f.take(f.queue(refresh), refresh)
			if err != nil {
				t.Fatal(err)
			}
			rotated := domain.NewID()
			failRemoval(f.secrets)
			if _, err := f.finishWith(lease, rotated, f.grokRotatedEnvelope(rotated), true, true, true); err == nil {
				t.Fatal("a failed removal of the superseded generation was reported as success")
			}
			return subject{f.service, f.secrets, f.input.AccountID, f.record, rotated}
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := setup(t)
			g.secrets.afterPut = nil
			_, a := g.record()
			st := a.Subscription
			if !st.RecoveryRequired || st.Lease != nil || st.Pending != nil || st.Generation != g.rotated || !grokOwnerlessCleanupFence(a) {
				t.Fatalf("the failed removal did not leave the ownerless fence: fenced=%v lease=%v pending=%v", st.RecoveryRequired, st.Lease != nil, st.Pending != nil)
			}
			// The removal still fails: the fence stays.
			if err := g.service.recoverGrokOwnerlessCleanupFence(ownerContext(), g.account); err == nil {
				t.Fatal("a failing removal was reported as recovered")
			}
			if _, a = g.record(); !a.Subscription.RecoveryRequired {
				t.Fatal("a failed scanner removal cleared the fence")
			}
			g.secrets.mu.Lock()
			g.secrets.deleteError = nil
			g.secrets.mu.Unlock()
			if err := g.service.recoverGrokOwnerlessCleanupFence(ownerContext(), g.account); err != nil {
				t.Fatal(err)
			}
			_, a = g.record()
			if a.Subscription.RecoveryRequired || a.Health != domain.AccountReady || a.Subscription.Generation != g.rotated || a.Connection == nil {
				t.Fatalf("the scanner did not restore the account: fenced=%v health=%v", a.Subscription.RecoveryRequired, a.Health)
			}
			refs, err := g.secrets.UnremovedReferences(context.Background(), g.account)
			if err != nil || len(refs) != 1 || refs[0].ID != g.rotated {
				t.Fatalf("the superseded generation was not removed: %v %v", refs, err)
			}
			// A repeated scan finds nothing to do.
			if err := g.service.recoverGrokOwnerlessCleanupFence(ownerContext(), g.account); err != nil {
				t.Fatalf("a settled fence made the scanner fail: %v", err)
			}
		})
	}
}

// The ownerless shape is exact: a fence that has a lease, a pending operation or
// an active operation belongs to its own recovery and is never selected.
func TestGrokOwnerlessCleanupFenceSelectsOnlyTheOwnerlessShape(t *testing.T) {
	base := func() domain.Account {
		return domain.Account{Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionGrok, Connection: &domain.AccountConnection{}, Subscription: &domain.SubscriptionState{Generation: domain.NewID(), RecoveryRequired: true}}
	}
	for name, test := range map[string]struct {
		change func(a *domain.Account)
		want   bool
	}{
		"ownerless": {func(a *domain.Account) {}, true},
		"settled operation": {func(a *domain.Account) {
			a.Subscription.ServerOperation = &domain.ServerSubscriptionOperation{State: domain.SubscriptionFailed}
		}, true},
		"not fenced":        {func(a *domain.Account) { a.Subscription.RecoveryRequired = false }, false},
		"lease kept":        {func(a *domain.Account) { a.Subscription.Lease = &domain.SubscriptionLease{} }, false},
		"pending operation": {func(a *domain.Account) { a.Subscription.Pending = &domain.SubscriptionOperation{} }, false},
		"active operation": {func(a *domain.Account) {
			a.Subscription.ServerOperation = &domain.ServerSubscriptionOperation{State: domain.SubscriptionRecovery}
		}, false},
		"native started": {func(a *domain.Account) {
			a.Subscription.ServerOperation = &domain.ServerSubscriptionOperation{State: domain.SubscriptionFailed, NativeStarted: true}
		}, false},
		"revoked generation set": {func(a *domain.Account) { a.Subscription.RevokedGeneration = a.Subscription.Generation }, false},
		"disconnected":           {func(a *domain.Account) { a.Connection = nil }, false},
		"no generation":          {func(a *domain.Account) { a.Subscription.Generation = "" }, false},
		"not grok":               {func(a *domain.Account) { a.SubscriptionService = domain.SubscriptionChatGPT }, false},
	} {
		a := base()
		test.change(&a)
		if got := grokOwnerlessCleanupFence(a); got != test.want {
			t.Errorf("%s: selected = %v, want %v", name, got, test.want)
		}
	}
}
