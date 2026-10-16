// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// A settled frozen automatic refresh that declines to admit a credit retains the
// obsolete captured reference: it writes no durable fence, performs no vault
// work and a vault Delete failure cannot affect the result.
func TestAutomaticServerQuotaSettlementRetainsCapturedReferenceWithoutFence(t *testing.T) {
	f, b, n, quota := automaticServerFixture(t, true, true)
	admitFixture(t, f, &b, "")
	r, a := f.record()
	captured := a.Subscription.Generation
	// Fresh inventory is not exhausted, so no credit is admitted.
	one := 1.0
	quota.observed.Windows[0].Remaining = &one
	quota.duringRead = func() {
		_, current := f.record()
		l := current.Subscription.Lease
		lease := &pb.TakeSubscriptionResponse{LeaseId: string(l.ID), LeaseRevision: l.Revision, GenerationId: string(l.Generation)}
		bundle, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: r.ID, ID: l.Generation, Purpose: credentials.AccountLogin})
		if err != nil {
			t.Error(err)
			return
		}
		defer clear(bundle)
		if _, err = f.finish(lease, bundle, true, false, true); err != nil {
			t.Error(err)
		}
	}
	f.secrets.deleteError = domain.Fail(domain.Unavailable, "Fixture cleanup failure.", "")
	f.service.runServerQuota(creditContext(), f.input.AccountID)
	_, a = f.record()
	o := a.Subscription.ServerQuota
	if a.Subscription.Generation == captured || o == nil || o.Generation != captured || o.Phase != domain.SubscriptionObservationSucceeded {
		t.Fatal("refresh did not settle against a rotated generation", a.Subscription)
	}
	if a.Subscription.ServerCredit != nil || n.count() != 0 {
		t.Fatal("a credit was admitted from a non-exhausted inventory")
	}
	if o.ReferenceRetirementPending || !referenceExists(t, f, captured) || !referenceExists(t, f, a.Subscription.Generation) {
		t.Fatal("settlement wrote a retirement fence or removed a generation", o)
	}
	if got := protectedGenerations(a.Subscription); len(got) != 0 {
		t.Fatal("a settled read still protects its obsolete reference", got)
	}
	if !serverQuotaReady(a) {
		t.Fatal("a vault Delete failure blocked server reads")
	}
	if f.secrets.deletes != 0 {
		t.Fatal("settlement called the vault Delete")
	}
}

// A retirement fence written by an earlier build is released by the legacy
// transition without deleting the retained captured reference.
func TestLegacyServerQuotaRetirementFenceReleasesWithoutDeletion(t *testing.T) {
	f, b, _, quota := automaticServerFixture(t, true, true)
	admitFixture(t, f, &b, "")
	r, a := f.record()
	captured := a.Subscription.Generation
	one := 1.0
	quota.observed.Windows[0].Remaining = &one
	quota.duringRead = func() {
		_, current := f.record()
		l := current.Subscription.Lease
		lease := &pb.TakeSubscriptionResponse{LeaseId: string(l.ID), LeaseRevision: l.Revision, GenerationId: string(l.Generation)}
		bundle, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: r.ID, ID: l.Generation, Purpose: credentials.AccountLogin})
		if err != nil {
			t.Error(err)
			return
		}
		defer clear(bundle)
		if _, err = f.finish(lease, bundle, true, false, true); err != nil {
			t.Error(err)
		}
	}
	f.service.runServerQuota(creditContext(), f.input.AccountID)
	mutateCreditAccount(t, f, func(a *domain.Account) { a.Subscription.ServerQuota.ReferenceRetirementPending = true })
	if _, a = f.record(); serverQuotaReady(a) {
		t.Fatal("the seeded fence did not block server reads")
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := f.service.reconcilePendingServerQuotaRetirements(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	_, a = f.record()
	if a.Subscription.ServerQuota.ReferenceRetirementPending || !referenceExists(t, f, captured) || !referenceExists(t, f, a.Subscription.Generation) || !serverQuotaReady(a) {
		t.Fatal("the legacy fence was not released without deletion", a.Subscription.ServerQuota)
	}
	if f.secrets.deletes != 0 {
		t.Fatal("the legacy transition called the vault Delete")
	}
}

func TestServerQuotaRetirementFenceRequiresSettledAutomaticAccessOnlyRead(t *testing.T) {
	o := domain.ServerQuotaOperation{AccessOnly: true, AutomaticEpisodeID: domain.NewID(), ID: domain.NewID(), Epoch: domain.NewID(), FinishID: domain.NewID(), ConnectionID: domain.NewID(), Generation: domain.NewID(), Actor: domain.Principal{Type: domain.OwnerDevice}, Phase: domain.SubscriptionObservationSucceeded, RequestedAt: time.Now().UTC(), CleanupConfirmed: true, ReferenceRetirementPending: true}
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*domain.ServerQuotaOperation){
		"active":      func(o *domain.ServerQuotaOperation) { o.Phase = domain.SubscriptionObservationSending },
		"managed":     func(o *domain.ServerQuotaOperation) { o.AccessOnly, o.AutomaticEpisodeID = false, "" },
		"manual read": func(o *domain.ServerQuotaOperation) { o.AutomaticEpisodeID = "" },
	} {
		c := o
		change(&c)
		if c.Validate() == nil {
			t.Fatal("invalid fence accepted:", name)
		}
	}
}
