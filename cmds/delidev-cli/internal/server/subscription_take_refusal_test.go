// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"net/http"
	"reflect"
	"testing"
)

func TestSubscriptionFreshTakeRefusalDoesNotAcquireAuthority(t *testing.T) {
	for _, service := range []string{"chatgpt", "claude"} {
		for _, state := range []string{"absent", "replaced", "canceled", "wrong-action", "wrong-machine", "claimed", "recovery"} {
			t.Run(service+"/"+state, func(t *testing.T) {
				var f *subscriptionFixture
				var op *pb.RequestSubscriptionResponse
				if service == "claude" {
					f = newClaudeSubscriptionFixture(t)
					op = claudeStart(t, f, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
				} else {
					f = newSubscriptionFixture(t)
					op = f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
				}
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.take-refusal", nil, func(tx *store.Tx) (any, error) {
					r, a, err := accountFromTx(tx, f.input.AccountID, 0)
					if err != nil {
						return nil, err
					}
					switch state {
					case "absent":
						a.Subscription.Pending = nil
					case "replaced":
						a.Subscription.Pending.ID = domain.NewID()
					case "canceled":
						a.Subscription.Pending.Canceled = true
					case "wrong-action":
						a.Subscription.Pending.Action = domain.SubscriptionRefresh
					case "wrong-machine":
						a.Subscription.Pending.MachineID = domain.NewID()
					case "claimed":
						a.Subscription.Pending.Phase = domain.SubscriptionClaimed
					case "recovery":
						a.Subscription.RecoveryRequired = true
					}
					_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
					return nil, err
				})
				if err != nil {
					t.Fatal(err)
				}
				response, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
				problem := domain.SafeError(rpc.ClientError(err))
				marked := state == "absent" || state == "replaced" || state == "canceled"
				if marked {
					if problem.Code != domain.Canceled || problem.Cause != "subscription_take_not_admitted" {
						t.Fatalf("missing exact no-grant proof: %+v", problem)
					}
				} else if problem.Cause != "" || problem.Code != domain.RecoveryRequired {
					t.Fatalf("unproven refusal marked: %+v", problem)
				}
				if response != nil {
					t.Fatal("refusal delivered protected bytes")
				}
				_, a := f.record()
				if a.Subscription.Lease != nil {
					t.Fatal("refusal acquired lease")
				}
			})
		}
	}
}

func TestSubscriptionCanceledTakePreservesIndependentOriginalLease(t *testing.T) {
	f := newSubscriptionFixture(t)
	original := f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	lease, err := f.take(original, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	_, before := f.record()
	var other store.Record
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.independent-refusal", nil, func(tx *store.Tx) (any, error) {
		var err error
		other, err = tx.Put(domain.AccountKind, domain.NewID(), 0, "", "", domain.Account{Alias: "Canceled independent", SubscriptionService: domain.SubscriptionChatGPT, Type: domain.SubscriptionAccount, Enabled: true, Health: domain.AccountDisconnected})
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(other.ID), ExpectedRevision: other.Revision}, MachineId: string(f.input.MachineID), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}))
	if err != nil {
		t.Fatal(err)
	}
	// Hold the watched original operation until its deliberate cancellation settles.
	_, err = f.client.CancelSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: acctMutation(started.Msg.Account, domain.NewID())}))
	if err != nil {
		t.Fatal(err)
	}
	client := delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, f.http.URL)
	response, err := client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(other.ID), ExpectedRevision: started.Msg.Account.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: started.Msg.OperationId, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}))
	problem := domain.SafeError(rpc.ClientError(err))
	if response != nil || problem.Code != domain.Canceled || problem.Cause != "subscription_take_not_admitted" {
		t.Fatal("canceled A lost exact refusal", problem)
	}
	_, after := f.record()
	if !reflect.DeepEqual(before, after) || string(after.Subscription.Lease.ID) != lease.LeaseId {
		t.Fatal("A changed B original lease/native operation")
	}
}

func TestClaudeCanceledInitialTakeUsesOriginalRetiredProfileProof(t *testing.T) {
	f := newClaudeSubscriptionFixture(t)
	op := claudeStart(t, f, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	_, err := f.client.CancelSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: acctMutation(op.Account, domain.NewID())}))
	if err != nil {
		t.Fatal(err)
	}
	_, before := f.record()
	if before.Subscription.NativeProfileID != "" || before.Subscription.Pending != nil {
		t.Fatal("original unused profile not retired")
	}
	response, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	problem := domain.SafeError(rpc.ClientError(err))
	if response != nil || problem.Code != domain.Canceled || problem.Cause != "subscription_take_not_admitted" {
		t.Fatal(problem)
	}
	_, after := f.record()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("refusal resurrected original profile")
	}
	if _, err = f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH); domain.SafeError(rpc.ClientError(err)).Cause != "" {
		t.Fatal("wrong action inherited retired-profile proof")
	}
}
