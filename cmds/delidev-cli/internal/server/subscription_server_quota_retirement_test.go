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

// A settled frozen automatic refresh that declines to admit a credit must keep a
// durable fence when its checked reference retirement fails, and clear it once a
// retry retires exactly the obsolete captured reference.
func TestAutomaticServerQuotaRetirementFailureKeepsDurableFence(t *testing.T) {
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
	if !o.ReferenceRetirementPending || !referenceExists(t, f, captured) {
		t.Fatal("failed retirement lost its durable fence", o)
	}
	if got := protectedGenerations(a.Subscription); len(got) != 1 || got[0] != captured {
		t.Fatal("fenced reference is not protected from rotation cleanup", got)
	}
	if serverQuotaReady(a) {
		t.Fatal("a new server read was admitted while the obsolete reference was fenced")
	}
	// A retry while deletion still fails keeps the fence.
	if err := f.service.reconcilePendingServerQuotaRetirements(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, a = f.record()
	if !a.Subscription.ServerQuota.ReferenceRetirementPending || !referenceExists(t, f, captured) {
		t.Fatal("failed retry released the fence")
	}
	f.secrets.deleteError = nil
	if err := f.service.reconcilePendingServerQuotaRetirements(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, a = f.record()
	if a.Subscription.ServerQuota.ReferenceRetirementPending || referenceExists(t, f, captured) || !referenceExists(t, f, a.Subscription.Generation) {
		t.Fatal("retry did not retire exactly the captured reference and release the fence", a.Subscription.ServerQuota)
	}
	if !serverQuotaReady(a) {
		t.Fatal("released fence still blocks server reads")
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
