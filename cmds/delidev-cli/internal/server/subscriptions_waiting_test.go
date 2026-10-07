// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
)

func TestSubscriptionLifecycleTakeRetainsOriginalRevisionAfterBusy(t *testing.T) {
	for _, action := range []pb.SubscriptionAction{pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT} {
		t.Run(action.String(), func(t *testing.T) {
			f := newSubscriptionFixture(t)
			raw := f.login()
			defer clear(raw)
			execution := takeSubscriptionExecutionFixture(t, f)
			clear(execution.Bundle)
			op := f.start(action)
			request := &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: op.Account.Id, ExpectedRevision: op.Account.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: op.OperationId, Action: action}
			original := proto.Clone(request).(*pb.TakeSubscriptionRequest)
			response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, request))
			if err != nil {
				t.Fatal("unchanged lifecycle claim could not acquire the released account", err)
			}
			defer clear(response.Msg.Bundle)
			_, account := f.record()
			if !proto.Equal(request, original) || account.Subscription.Lease == nil || account.Subscription.Lease.OperationID != domain.ID(op.OperationId) || account.Subscription.Lease.Action != subscriptionAction(action) || response.Msg.GenerationId != execution.GenerationId || !bytes.Equal(response.Msg.Bundle, raw) {
				t.Fatal("retry changed the original lifecycle authority or generation")
			}
			if _, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, request)); err == nil {
				t.Fatal("accepted receipt redistributed its bundle")
			}
		})
	}
}

func TestSubscriptionLifecycleTakeStaleObservationPreservesPendingAuthority(t *testing.T) {
	for _, action := range []pb.SubscriptionAction{pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT} {
		for _, change := range []string{"metadata", "canceled", "replaced", "recovery", "future"} {
			t.Run(action.String()+"/"+change, func(t *testing.T) {
				f := newSubscriptionFixture(t)
				if action != pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN {
					clear(f.login())
				}
				op := f.start(action)
				request := &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: op.Account.Id, ExpectedRevision: op.Account.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: op.OperationId, Action: action}
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.lifecycle-observation", nil, func(tx *store.Tx) (any, error) {
					r, account, err := accountFromTx(tx, f.input.AccountID, 0)
					if err != nil {
						return nil, err
					}
					account.Alias = "Updated metadata"
					switch change {
					case "canceled":
						account.Subscription.Pending.Canceled = true
					case "replaced":
						account.Subscription.Pending.ID = domain.NewID()
					case "recovery":
						account.Subscription.RecoveryRequired = true
					case "future":
						request.Mutation.ExpectedRevision = r.Revision + 2
					}
					_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", account)
					return nil, err
				})
				if err != nil {
					t.Fatal(err)
				}
				before, _ := f.record()
				response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, request))
				if change == "metadata" || change == "recovery" {
					if err != nil {
						t.Fatal("unchanged queued operation lost authority after metadata edit", err)
					}
					clear(response.Msg.Bundle)
					_, account := f.record()
					if account.Alias != "Updated metadata" || account.Subscription.Lease == nil || account.Subscription.Lease.OperationID != domain.ID(op.OperationId) {
						t.Fatal("Take discarded later metadata or original operation")
					}
				} else {
					after, account := f.record()
					if err == nil || after.Revision != before.Revision || account.Subscription.Lease != nil {
						t.Fatal("stale observation bypassed changed operation authority", err)
					}
					if change == "future" && domain.SafeError(rpc.ClientError(err)).Code != domain.Conflict {
						t.Fatal("future revision did not retain typed conflict", err)
					}
				}
			})
		}
	}
}
