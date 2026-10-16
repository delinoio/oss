// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// A Worker-owned Grok logout whose private home cleanup is not confirmed keeps
// the lease and fences the account. Only the original lease owner may report the
// cleanup again; the retry releases the lease and completes the logout.
func TestGrokUnconfirmedHomeCleanupIsRetriedOnlyByTheOriginalLeaseOwner(t *testing.T) {
	f := newGrokFixture(t)
	f.login("synthetic-principal-1")
	logs := f.captureLogs()
	logout := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT
	lease, err := f.take(f.queue(logout), logout)
	if err != nil {
		t.Fatal(err)
	}
	// Fault injection: the Worker could not remove the credential file from its
	// private home, so it reports the cleanup as unconfirmed.
	if _, err := f.finishWith(lease, domain.NewID(), nil, true, false, false); err != nil {
		t.Fatal(err)
	}
	_, fenced := f.record()
	st := fenced.Subscription
	if !st.RecoveryRequired || st.Lease == nil || !st.Lease.CleanupUnconfirmed || fenced.Health != domain.AccountFailed || fenced.Connection == nil || len(f.references()) != 1 {
		t.Fatal("an unconfirmed home cleanup must keep the lease, the generation and the recovery fence")
	}

	retry := func(instance domain.ID, revision uint64, cleanup bool) error {
		_, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: revision}, LeaseId: lease.LeaseId, GenerationId: lease.GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(instance), Succeeded: true, CleanupConfirmed: cleanup}))
		return err
	}
	// Another Worker instance is refused, and so is another Worker device.
	if err := retry(domain.NewID(), lease.LeaseRevision, true); err == nil {
		t.Fatal("a different Worker instance finished another owner's lease")
	}
	other := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: f.input.MachineID})
	request := connect.NewRequest(&pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, GenerationId: lease.GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Succeeded: true, CleanupConfirmed: true})
	if _, err := f.service.finishGrokSubscription(other, request); err == nil {
		t.Fatal("a different Worker device finished another owner's lease")
	}
	if _, a := f.record(); a.Subscription.Lease == nil || !a.Subscription.RecoveryRequired || a.Connection == nil {
		t.Fatal("a refused retry changed the account")
	}
	// A wrong lease revision from the right owner is refused too.
	if err := retry(f.instance, lease.LeaseRevision+1, true); err == nil {
		t.Fatal("a retry with another lease revision was accepted")
	}
	// The original owner may fail again; the lease stays held.
	if err := retry(f.instance, lease.LeaseRevision, false); err != nil {
		t.Fatal(err)
	}
	if _, a := f.record(); a.Subscription.Lease == nil || !a.Subscription.RecoveryRequired {
		t.Fatal("a second unconfirmed report released the lease")
	}
	// The same Worker removes the file and reports the cleanup confirmed.
	if err := retry(f.instance, lease.LeaseRevision, true); err != nil {
		t.Fatal(err)
	}
	_, done := f.record()
	st = done.Subscription
	if st.Lease != nil || st.RecoveryRequired || st.Pending != nil || st.Generation != "" || done.Connection != nil || done.Health != domain.AccountDisconnected || len(f.references()) != 0 {
		t.Fatal("the original owner's confirmed cleanup did not complete the logout")
	}
	text := logs.buf.String()
	if !strings.Contains(text, `"phase":"CLEANUP"`) || !strings.Contains(text, "grok_subscription_cleanup_retry") {
		t.Fatal("the cleanup retry was not logged with the CLEANUP phase")
	}
}

// The first Finish outcome is frozen: a native failure reported with an
// unconfirmed cleanup cannot be turned into a success by the cleanup retry. The
// retry only confirms the cleanup, so the failed logout never clears the
// connection or the generation.
func TestGrokCleanupRetryCannotFlipAReportedFailureIntoSuccess(t *testing.T) {
	f := newGrokFixture(t)
	f.login("synthetic-principal-1")
	logout := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT
	lease, err := f.take(f.queue(logout), logout)
	if err != nil {
		t.Fatal(err)
	}
	before := func() domain.Account { _, a := f.record(); return a }()
	// The native logout failed and the private cleanup was not confirmed.
	if _, err := f.finishWith(lease, domain.NewID(), nil, false, false, false); err != nil {
		t.Fatal(err)
	}
	first := func() domain.Account { _, a := f.record(); return a }()
	if o := first.Subscription.Lease.FirstFinish; o == nil || o.Succeeded || o.RefreshConfirmed {
		t.Fatalf("the first outcome was not frozen on the lease: %+v", o)
	}
	// The retry claims success while confirming the cleanup.
	if _, err := f.finishWith(lease, domain.NewID(), nil, true, true, true); err != nil {
		t.Fatal(err)
	}
	_, after := f.record()
	st := after.Subscription
	if after.Connection == nil || st.Generation != before.Subscription.Generation || len(f.references()) != 1 {
		t.Fatal("a retry turned a reported native failure into a completed logout")
	}
	if o := st.ServerOperation; o == nil || o.State == domain.SubscriptionSucceeded {
		t.Fatal("the persisted operation no longer records the first failure")
	}
}

// A lease fenced for any other reason has no cleanup report to retry.
func TestGrokRecoveryWithoutAnUnconfirmedCleanupCannotBeFinished(t *testing.T) {
	f := newGrokFixture(t)
	f.login("synthetic-principal-1")
	logout := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT
	lease, err := f.take(f.queue(logout), logout)
	if err != nil {
		t.Fatal(err)
	}
	f.mutateAccount(t, func(a *domain.Account) {
		a.Subscription.RecoveryRequired = true
		a.Health = domain.AccountFailed
	})
	if _, err := f.finishWith(lease, domain.NewID(), nil, true, false, true); err == nil {
		t.Fatal("a lease fenced without an unconfirmed cleanup was finished")
	}
}
