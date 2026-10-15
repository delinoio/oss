// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// elapseQueuedWindow moves the pending native operation's window into the past,
// exactly as if its fifteen minutes passed while it waited.
func elapseQueuedWindow(t *testing.T, f *subscriptionFixture) {
	t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.elapse-queued-window", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		o := a.Subscription.NativeOperation
		started := time.Now().UTC().Add(-time.Hour)
		o.StartedAt, o.ExpiresAt = started, started.Add(claudeOperationWindow)
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestClaudeRefreshQueuedBehindOwnerClaimsAfterItsWindowElapsedWhileWaiting(t *testing.T) {
	f := newClaudeMemberFixture(t, true)
	jobs := f.prepareJobs(1)
	var leases []*pb.TakeSubscriptionResponse
	for _, job := range jobs[:2] {
		lease, err := f.takeJob(job)
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lease)
	}
	refresh := claudeStart(t, f.subscriptionFixture, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	_, queued := f.record()
	if queued.Subscription.Pending == nil || !queued.Subscription.Pending.QueuedBehindLease {
		t.Fatal("a refresh behind the owner was not marked as waiting for its lifetime")
	}
	elapseQueuedWindow(t, f.subscriptionFixture)
	// The contended Take keeps waiting; the wait itself is not an expiry.
	if _, err := f.take(refresh, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH); err == nil {
		t.Fatal("refresh authority overlapped the live members")
	}
	if progress, err := f.client.GetSubscriptionProgress(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.GetSubscriptionProgressRequest{AccountId: string(f.input.AccountID), OperationId: refresh.OperationId})); err != nil || progress.Msg.State == pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_EXPIRED {
		t.Fatal("a refresh waiting behind the owner was reported as expired", err)
	}
	if _, err := f.finishScope(leases[1], leases[1].LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, leases[0].GenerationId, nil, true, true); err != nil {
		t.Fatal(err)
	}
	_, mid := f.record()
	owner := &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: leases[0].LeaseRevision}, LeaseId: leases[0].LeaseId, GenerationId: leases[0].GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), CleanupConfirmed: true, Succeeded: true, FinishScope: pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER, NativeIdentity: &pb.NativeSubscriptionIdentity{ProfileId: leases[0].NativeProfileId, IdentityCommitment: mid.Subscription.IdentityCommitment}}
	if _, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, owner)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.take(refresh, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH); err != nil {
		t.Fatal("the drained owner left the queued refresh unclaimable", err)
	}
	_, held := f.record()
	o := held.Subscription.NativeOperation
	if held.Subscription.Pending == nil || held.Subscription.Pending.Phase != domain.SubscriptionClaimed || !o.ExpiresAt.After(time.Now().UTC().Add(10*time.Minute)) {
		t.Fatal("the refresh window did not begin at the claim")
	}
}

func TestClaudeExpiredUnclaimedQueuedOperationNeverBlocksAdmissionOrRequests(t *testing.T) {
	f := newClaudeMemberFixture(t, true)
	jobs := f.prepareJobs(1)
	refresh := claudeStart(t, f.subscriptionFixture, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	_, queued := f.record()
	if queued.Subscription.Pending == nil || queued.Subscription.Pending.QueuedBehindLease {
		t.Fatal("an idle-account refresh claimed to wait behind a lease")
	}
	if _, err := f.takeJob(jobs[0]); err == nil {
		t.Fatal("a queued refresh left admission open")
	}
	elapseQueuedWindow(t, f.subscriptionFixture)
	// No Worker claimed it within its window, so it can never be claimed: the
	// next admission retires it instead of staying blocked until a user cancels.
	if _, err := f.takeJob(jobs[0]); err != nil {
		t.Fatal("an expired unclaimed refresh kept blocking admission", err)
	}
	_, after := f.record()
	if after.Subscription.Pending != nil || after.Subscription.NativeOperation.State != domain.SubscriptionExpired || after.Subscription.NativeOperation.ID != domain.ID(refresh.OperationId) {
		t.Fatal("the expired queued operation was not retired as expired")
	}
	if err := after.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeExpiredUnclaimedQueuedOperationIsSupersededByANewRequest(t *testing.T) {
	f := newClaudeMemberFixture(t, true)
	stale := claudeStart(t, f.subscriptionFixture, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	elapseQueuedWindow(t, f.subscriptionFixture)
	next := claudeStart(t, f.subscriptionFixture, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	_, account := f.record()
	if next.OperationId == stale.OperationId || account.Subscription.Pending == nil || account.Subscription.Pending.ID != domain.ID(next.OperationId) {
		t.Fatal("a new request did not supersede the expired queued operation")
	}
}

func TestClaudeQueuedLogoutKeepsItsWindowForTheClaim(t *testing.T) {
	f := newClaudeMemberFixture(t, true)
	logout := claudeStart(t, f.subscriptionFixture, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	elapseQueuedWindow(t, f.subscriptionFixture)
	if _, err := f.take(logout, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT); err != nil {
		t.Fatal("a queued logout became unclaimable after waiting", err)
	}
}
