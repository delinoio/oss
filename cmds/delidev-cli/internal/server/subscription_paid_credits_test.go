// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"testing"
	"time"
)

func TestPaidCreditPublicationRequiresOriginalNegotiatedWorker(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(map[bool]string{false: "old-worker", true: "new-worker"}[supported], func(t *testing.T) {
			f := newQuotaFixture(t)
			if supported {
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.paid.capability", nil, func(tx *store.Tx) (any, error) {
					r, err := tx.Get(domain.MachineKind, f.input.MachineID)
					if err != nil {
						return nil, err
					}
					m, err := store.Decode[domain.Machine](r)
					if err != nil {
						return nil, err
					}
					m.WorkerCapabilities = append(m.WorkerCapabilities, domain.SubscriptionPaidCreditsV1)
					_, err = tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
					return nil, err
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			op := f.requestQuota()
			lease, err := f.take(&pb.RequestSubscriptionResponse{OperationId: op.OperationId}, pb.SubscriptionAction_SUBSCRIPTION_ACTION_QUOTA)
			if err != nil {
				t.Fatal(err)
			}
			f.claimObservation(op.OperationId, lease)
			now := time.Now().UTC()
			yes, no := true, false
			balance := "1250.50000000000000000001"
			ob := domain.SubscriptionQuotaObservation{ObservedAt: now, PaidCredits: []domain.SubscriptionPaidCreditBucket{{ID: "codex", HasCredits: &yes, Unlimited: &no, Balance: &balance, ObservedAt: now}}}
			raw, _ := json.Marshal(domain.SubscriptionObservationResult{Quota: &ob})
			request := &pb.PublishSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: op.OperationId, GenerationId: lease.GenerationId, ObservationJson: raw}
			_, err = f.client.PublishSubscriptionObservation(context.Background(), subscriptionRequest(f.workerToken, request))
			_, a := f.record()
			if !supported {
				if rpc.ClientError(err).Code != domain.Unsupported || len(a.Subscription.PaidCredits) != 0 {
					t.Fatal("old Worker manufactured paid-credit support", err)
				}
				return
			}
			if err != nil || len(a.Subscription.PaidCredits) != 1 || *a.Subscription.PaidCredits[0].Balance != balance {
				t.Fatal("authenticated exact balance lost", err)
			}
			// A different generation cannot replace the retained original observation.
			request.Mutation.RequestId = string(domain.NewID())
			request.GenerationId = string(domain.NewID())
			if _, err = f.client.PublishSubscriptionObservation(context.Background(), subscriptionRequest(f.workerToken, request)); err == nil {
				t.Fatal("replacement generation published original balance")
			}
			_, a = f.record()
			if *a.Subscription.PaidCredits[0].Balance != balance || !a.Subscription.PaidCredits[0].ObservedAt.Equal(now) {
				t.Fatal("failed original ownership changed evidence")
			}
		})
	}
}
