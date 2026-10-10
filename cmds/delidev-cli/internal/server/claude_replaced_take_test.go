// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"reflect"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Both lifecycle requests use real acceptance/cancellation. The second verified
// machine is fixture metadata only; no native Worker or account is launched.
func claudeReplacementFixture(t *testing.T, otherMachine bool) (*subscriptionFixture, *pb.RequestSubscriptionResponse, *pb.RequestSubscriptionResponse) {
	t.Helper()
	f := newClaudeSubscriptionFixture(t)
	original := claudeStart(t, f, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	_, err := f.client.CancelSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: acctMutation(original.Account, domain.NewID())}))
	if err != nil {
		t.Fatal(err)
	}
	machine := f.input.MachineID
	if otherMachine {
		machine = domain.NewID()
		_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.claude-replacement-machine", nil, func(tx *store.Tx) (any, error) {
			_, observed, err := activeMachine(tx, f.input.MachineID)
			if err != nil {
				return nil, err
			}
			return tx.Put(domain.MachineKind, machine, 0, "", "", observed)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	r, _ := f.record()
	replacement, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{Id: string(r.ID), RequestId: string(domain.NewID()), ExpectedRevision: r.Revision}, MachineId: string(machine), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}))
	if err != nil {
		t.Fatal(err)
	}
	return f, original, replacement.Msg
}

func TestClaudeReplacedInitialTakeRetiresOnlyOriginalClaim(t *testing.T) {
	f, original, replacement := claudeReplacementFixture(t, true)
	r, before := f.record()
	if before.Subscription.OwnerMachineID == f.input.MachineID || before.Subscription.NativeProfileID == "" || before.Subscription.Pending.ID != domain.ID(replacement.OperationId) {
		t.Fatal("fixture did not accept independently owned replacement")
	}
	// A retains its original positive observation, not B's current revision.
	response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{Id: string(r.ID), RequestId: string(domain.NewID()), ExpectedRevision: original.Account.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: original.OperationId, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}))
	problem := domain.SafeError(rpc.ClientError(err))
	if response != nil || problem.Code != domain.Canceled || problem.Cause != "subscription_take_not_admitted" {
		t.Fatalf("stale claim did not receive exact no-grant proof: %+v", problem)
	}
	afterRecord, after := f.record()
	if afterRecord.Revision != r.Revision || !reflect.DeepEqual(before, after) {
		t.Fatal("stale Take changed replacement profile, pending operation or receipt state")
	}
}

func TestClaudeReplacedTakeRequiresOriginalCurrentWorkerFences(t *testing.T) {
	for _, fence := range []string{"instance", "installation", "recovery", "claimed", "future-revision", "wrong-action", "update"} {
		t.Run(fence, func(t *testing.T) {
			f, original, _ := claudeReplacementFixture(t, true)
			instance := f.instance
			if fence == "instance" {
				instance = domain.NewID()
			}
			if fence == "installation" || fence == "recovery" || fence == "claimed" || fence == "update" {
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.claude-replaced-fence", nil, func(tx *store.Tx) (any, error) {
					if fence == "update" {
						return tx.Put(domain.UpdateKind, domain.NewID(), 0, "", "", updates.Operation{ServerID: f.service.Identity.ServerID, Actor: domain.Principal{Type: domain.OwnerDevice}, Component: updates.Worker, State: updates.Waiting, MachineID: f.input.MachineID, DeviceID: f.device})
					}
					if fence == "installation" {
						r, m, err := activeMachine(tx, f.input.MachineID)
						if err != nil {
							return nil, err
						}
						m.Installations = nil
						return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
					}
					r, a, err := accountFromTx(tx, f.input.AccountID, 0)
					if err != nil {
						return nil, err
					}
					if fence == "recovery" {
						a.Subscription.RecoveryRequired = true
					} else {
						a.Subscription.Pending.Phase = domain.SubscriptionClaimed
					}
					return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			r, before := f.record()
			revision := original.Account.Revision
			if fence == "future-revision" {
				revision = r.Revision + 1
			}
			action := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN
			if fence == "wrong-action" {
				action = pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH
			}
			response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{Id: string(r.ID), RequestId: string(domain.NewID()), ExpectedRevision: revision}, MachineId: string(f.input.MachineID), InstanceId: string(instance), OperationId: original.OperationId, Action: action}))
			if response != nil || err == nil || domain.SafeError(rpc.ClientError(err)).Cause != "" {
				t.Fatal("unproven refusal supplied no-grant proof", err)
			}
			afterRecord, after := f.record()
			if afterRecord.Revision != r.Revision || !reflect.DeepEqual(before, after) {
				t.Fatal("refused claim changed replacement ownership")
			}
		})
	}
}

func TestClaudeMatchingReplacementTakeKeepsProfileAndMachineChecks(t *testing.T) {
	for _, invalid := range []string{"profile", "owner-machine", "action", "pending-machine", "valid"} {
		t.Run(invalid, func(t *testing.T) {
			f, _, replacement := claudeReplacementFixture(t, false)
			if invalid != "valid" {
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.claude-matching-profile", nil, func(tx *store.Tx) (any, error) {
					r, a, err := accountFromTx(tx, f.input.AccountID, 0)
					if err != nil {
						return nil, err
					}
					switch invalid {
					case "profile":
						a.Subscription.NativeProfileID = ""
					case "owner-machine":
						a.Subscription.OwnerMachineID = domain.NewID()
					case "action":
						a.Subscription.Pending.Action = domain.SubscriptionRefresh
					case "pending-machine":
						a.Subscription.Pending.MachineID = domain.NewID()
					}
					return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			_, before := f.record()
			lease, err := f.take(replacement, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			if invalid == "valid" {
				if err != nil || lease == nil || lease.NativeProfileId != string(before.Subscription.NativeProfileID) || len(lease.Bundle) != 0 {
					t.Fatal("matching original replacement lost its exclusive native profile", err)
				}
				_, after := f.record()
				if after.Subscription.Lease == nil || after.Subscription.Lease.OperationID != domain.ID(replacement.OperationId) || after.Subscription.Pending.Phase != domain.SubscriptionClaimed {
					t.Fatal("matching replacement did not claim its own operation")
				}
			} else {
				if err == nil || lease != nil || domain.SafeError(rpc.ClientError(err)).Cause != "" {
					t.Fatal("invalid matching operation gained authority or no-grant proof", err)
				}
				_, after := f.record()
				if !reflect.DeepEqual(before, after) {
					t.Fatal("invalid matching operation changed profile ownership")
				}
			}
		})
	}
}

func TestClaudeReplacedTakeCannotRetireClaimedReplacementOrReplayItsGrant(t *testing.T) {
	f, original, replacement := claudeReplacementFixture(t, false)
	r, _ := f.record()
	request := &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{Id: string(r.ID), RequestId: string(domain.NewID()), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: replacement.OperationId, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}
	response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, request))
	if err != nil || response == nil {
		t.Fatal("matching replacement could not acquire original lease", err)
	}
	beforeRecord, before := f.record()
	if lease, err := f.take(original, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN); lease != nil || err == nil || domain.SafeError(rpc.ClientError(err)).Cause != "" {
		t.Fatal("claimed replacement allowed stale no-grant proof", err)
	}
	replay, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, request))
	if replay != nil || err == nil || domain.SafeError(rpc.ClientError(err)).Cause != "" {
		t.Fatal("Take replay redistributed native authority or no-grant proof", err)
	}
	afterRecord, after := f.record()
	if afterRecord.Revision != beforeRecord.Revision || !reflect.DeepEqual(before, after) {
		t.Fatal("stale/replayed Take changed replacement lease or recovery")
	}
}
