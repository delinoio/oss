// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
)

type committedSubscriptionSecrets struct {
	*accountTestSecrets
	calls        atomic.Int32
	afterCleanup func() error
}

func (v *committedSubscriptionSecrets) UnremovedReferences(ctx context.Context, owner domain.ID) ([]credentials.Ref, error) {
	refs, err := v.accountTestSecrets.UnremovedReferences(ctx, owner)
	if err == nil && v.calls.Add(1) == 2 {
		// Failed-login and unused-original execution Finish enumerate once before
		// mutation and once after commit. All returned references are already the
		// retained generation, so this final observation performs no deletion.
		if err := v.afterCleanup(); err != nil {
			return nil, err
		}
	}
	return refs, err
}

func TestSubscriptionCommittedFinishPresentationFailurePreservesSettlement(t *testing.T) {
	for _, action := range []pb.SubscriptionAction{pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE} {
		for _, cleanupFails := range []bool{false, true} {
			name := action.String() + "/presentation-canceled"
			if cleanupFails {
				name = action.String() + "/final-cleanup-failed"
			}
			t.Run(name, func(t *testing.T) {
				f := newSubscriptionFixture(t)
				var lease *pb.TakeSubscriptionResponse
				var err error
				if action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE {
					clear(f.login())
					lease = takeSubscriptionExecutionFixture(t, f)
				} else {
					lease, err = f.take(f.start(action), action)
					if err != nil {
						t.Fatal(err)
					}
				}
				defer clear(lease.Bundle)
				_, before := f.record()
				ctx, cancel := context.WithCancel(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: f.device, MachineID: f.input.MachineID}))
				defer cancel()
				vault := &committedSubscriptionSecrets{accountTestSecrets: f.secrets}
				vault.afterCleanup = func() error {
					_, settled := f.record()
					if settled.Subscription.Lease != nil || settled.Subscription.RecoveryRequired {
						t.Fatal("fault injection occurred before ownership settlement")
					}
					if cleanupFails {
						return domain.Fail(domain.PermissionDenied, "Fixture final vault observation failed.", "")
					}
					cancel()
					return nil
				}
				f.service.accountSecrets = vault
				request := &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, GenerationId: lease.GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Bundle: bytes.Clone(lease.Bundle), CleanupConfirmed: true}
				defer clear(request.Bundle)
				response, err := f.service.FinishSubscription(ctx, connect.NewRequest(proto.Clone(request).(*pb.FinishSubscriptionRequest)))
				if err == nil || response != nil || vault.calls.Load() != 2 {
					t.Fatal("post-commit failure injection was not observed", err)
				}
				if !cleanupFails && ctx.Err() == nil {
					t.Fatal("presentation read did not use the canceled context")
				}
				record, after := f.record()
				if after.Subscription.Lease != nil || after.Subscription.Pending != nil || after.Subscription.Generation != before.Subscription.Generation {
					t.Fatal("committed Finish ownership was changed")
				}
				if after.Subscription.RecoveryRequired != cleanupFails {
					t.Fatal("presentation cancellation was confused with unfinished vault cleanup")
				}
				wantHealth := before.Health
				if cleanupFails {
					wantHealth = domain.AccountFailed
				}
				if after.Health != wantHealth {
					t.Fatal("committed account health changed without uncertain cleanup")
				}
				if action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE {
					if after.Connection == nil || after.Connection.ID != before.Connection.ID {
						t.Fatal("unused-original settlement changed connection authority")
					}
					stored, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: domain.ID(lease.GenerationId), Purpose: credentials.AccountLogin})
					if err != nil || !bytes.Equal(stored, lease.Bundle) {
						t.Fatal("post-commit failure changed the retained generation", err)
					}
					clear(stored)
				}
				snapshot, _ := json.Marshal(after)
				replayed, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, request))
				if err != nil || !replayed.Msg.Replayed {
					t.Fatal("accepted Finish receipt could not replay", err)
				}
				current, replayedAccount := f.record()
				currentJSON, _ := json.Marshal(replayedAccount)
				if current.Revision != record.Revision || !bytes.Equal(snapshot, currentJSON) || vault.calls.Load() != 2 {
					t.Fatal("receipt replay changed settled or fenced ownership")
				}
			})
		}
	}
}
