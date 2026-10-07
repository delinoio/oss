// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"testing"
	"time"
)

func newQuotaFixture(t *testing.T) *subscriptionFixture {
	f := newSubscriptionFixture(t)
	f.login()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.quota.capability", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.MachineKind, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		m, err := store.Decode[domain.Machine](r)
		if err != nil {
			return nil, err
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.SubscriptionObservationsV1)
		_, err = tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *subscriptionFixture) requestQuota() *pb.RequestSubscriptionObservationResponse {
	r, a := f.record()
	response, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: pb.SubscriptionObservationAction_SUBSCRIPTION_OBSERVATION_ACTION_QUOTA, ConnectionId: string(a.Connection.ID), GenerationId: string(a.Subscription.Generation)}))
	if err != nil {
		f.t.Fatal(err)
	}
	return response.Msg
}
func (f *subscriptionFixture) claimObservation(op string, lease *pb.TakeSubscriptionResponse) *pb.ClaimSubscriptionObservationRequest {
	request := &pb.ClaimSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: op, GenerationId: lease.GenerationId}
	response, err := f.client.ClaimSubscriptionObservation(context.Background(), subscriptionRequest(f.workerToken, request))
	if err != nil {
		f.t.Fatal(err)
	}
	var original domain.SubscriptionObservationOperation
	if domain.Decode(response.Msg.OperationJson, &original) != nil || original.ID != domain.ID(op) || original.Phase != domain.SubscriptionObservationSending {
		f.t.Fatal("native claim lost original operation")
	}
	return request
}
func (f *subscriptionFixture) publishObservation(op string, lease *pb.TakeSubscriptionResponse, result domain.SubscriptionObservationResult) {
	raw, _ := json.Marshal(result)
	request := &pb.PublishSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: op, GenerationId: lease.GenerationId, ObservationJson: raw}
	if _, err := f.client.PublishSubscriptionObservation(context.Background(), subscriptionRequest(f.workerToken, request)); err != nil {
		f.t.Fatal(err)
	}
	if replay, err := f.client.PublishSubscriptionObservation(context.Background(), subscriptionRequest(f.workerToken, request)); err != nil || !replay.Msg.Replayed {
		f.t.Fatal("publication replay not read-only", err)
	}
}
func TestSubscriptionQuotaOriginalClaimAndResetSameKeyReconciliation(t *testing.T) {
	f := newQuotaFixture(t)
	op := f.requestQuota()
	lease, err := f.take(&pb.RequestSubscriptionResponse{OperationId: op.OperationId}, pb.SubscriptionAction_SUBSCRIPTION_ACTION_QUOTA)
	if err != nil {
		t.Fatal(err)
	}
	claim := f.claimObservation(op.OperationId, lease)
	if _, err := f.client.ClaimSubscriptionObservation(context.Background(), subscriptionRequest(f.workerToken, claim)); rpc.ClientError(err).Code != domain.RecoveryRequired {
		t.Fatal("claim replay granted another native send", err)
	}
	now := time.Now().UTC()
	remaining := 0.0
	reset := now.Add(time.Hour)
	credits := []domain.SubscriptionResetCreditDetail{{ID: "credit_1", Status: "available", ResetType: "codexRateLimits", GrantedAt: now.Add(-time.Hour)}}
	observed := &domain.SubscriptionQuotaObservation{ObservedAt: now, Windows: []domain.SubscriptionQuotaWindow{{ID: "codex:primary", Remaining: &remaining, ResetAt: &reset}}, Credits: &domain.SubscriptionResetCredits{ObservationID: domain.NewID(), ObservedAt: now, AvailableCount: 2, Credits: &credits}}
	f.publishObservation(op.OperationId, lease, domain.SubscriptionObservationResult{Quota: observed})
	r, a := f.record()
	if !a.ConfirmedExhausted || a.Subscription.ResetCredits.AvailableCount != 2 || len(*a.Subscription.ResetCredits.Credits) != 1 {
		t.Fatal("quota/count/detail evidence lost")
	}
	bundle, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: r.ID, ID: a.Subscription.Generation, Purpose: credentials.AccountLogin})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(bundle)
	if _, err := f.finish(lease, bundle, true, false, true); err != nil {
		t.Fatal(err)
	}
	r, a = f.record()
	key := domain.NewID()
	request := &pb.RequestSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(key), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: pb.SubscriptionObservationAction_SUBSCRIPTION_OBSERVATION_ACTION_RESET_CREDIT, ConnectionId: string(a.Connection.ID), GenerationId: string(a.Subscription.Generation), CreditId: "credit_1", CreditsObservationId: string(observed.Credits.ObservationID), Confirmed: true}
	accepted, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
	if err != nil {
		t.Fatal(err)
	}
	lease, err = f.take(&pb.RequestSubscriptionResponse{OperationId: accepted.Msg.OperationId}, pb.SubscriptionAction_SUBSCRIPTION_ACTION_RESET_CREDIT)
	if err != nil {
		t.Fatal(err)
	}
	f.claimObservation(string(key), lease)
	f.publishObservation(string(key), lease, domain.SubscriptionObservationResult{ConsumeUncertain: true, QuotaError: domain.Unavailable})
	if _, err := f.finish(lease, bundle, true, false, true); err != nil {
		t.Fatal(err)
	}
	r, a = f.record()
	if a.Subscription.Observation.Phase != domain.SubscriptionObservationUncertain || !a.ConfirmedExhausted || !a.Subscription.QuotaObservedAt.Equal(now) {
		t.Fatal("uncertain consumption fabricated recovery or erased last success")
	}
	replay, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
	if err != nil || !replay.Msg.Replayed || a.Subscription.Observation.Phase != domain.SubscriptionObservationUncertain {
		t.Fatal("uncertain creation replay resent credit", err)
	}
	reconciled, err := f.client.ReconcileSubscriptionCredit(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.ReconcileSubscriptionCreditRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: string(key), ConnectionId: string(a.Connection.ID), GenerationId: string(a.Subscription.Generation)}))
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Msg.Account.Id != string(r.ID) {
		t.Fatal("reconciliation changed account")
	}
	_, a = f.record()
	if a.Subscription.Observation.ID != key || a.Subscription.Observation.Phase != domain.SubscriptionObservationQueued {
		t.Fatal("reconciliation replaced official key")
	}
}

func TestSubscriptionCreditNonSuccessOutcomesAndStaleConfirmation(t *testing.T) {
	for _, outcome := range []domain.SubscriptionResetOutcome{domain.SubscriptionNoCredit, domain.SubscriptionNothingToReset} {
		t.Run(string(outcome), func(t *testing.T) {
			f := newQuotaFixture(t)
			now := time.Now().UTC()
			inventory := domain.NewID()
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.credit.inventory", nil, func(tx *store.Tx) (any, error) {
				r, a, err := accountFromTx(tx, f.input.AccountID, 0)
				if err != nil {
					return nil, err
				}
				a.ConfirmedExhausted = true
				a.Subscription.ResetCredits = &domain.SubscriptionResetCredits{ObservationID: inventory, ObservedAt: now, AvailableCount: 2}
				_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
				return nil, err
			})
			if err != nil {
				t.Fatal(err)
			}
			r, a := f.record()
			request := &pb.RequestSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: pb.SubscriptionObservationAction_SUBSCRIPTION_OBSERVATION_ACTION_RESET_CREDIT, ConnectionId: string(a.Connection.ID), GenerationId: string(a.Subscription.Generation), NextCredit: true, CreditsObservationId: string(domain.NewID()), Confirmed: true}
			if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, request)); rpc.ClientError(err).Code != domain.Conflict {
				t.Fatal("stale inventory granted consumption", err)
			}
			request.CreditsObservationId = string(inventory)
			request.Mutation.RequestId = string(domain.NewID())
			request.Confirmed = false
			if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, request)); err == nil {
				t.Fatal("implicit confirmation granted consumption")
			}
			request.Mutation.RequestId = string(domain.NewID())
			request.Confirmed = true
			accepted, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
			if err != nil {
				t.Fatal(err)
			}
			lease, err := f.take(&pb.RequestSubscriptionResponse{OperationId: accepted.Msg.OperationId}, pb.SubscriptionAction_SUBSCRIPTION_ACTION_RESET_CREDIT)
			if err != nil {
				t.Fatal(err)
			}
			f.claimObservation(accepted.Msg.OperationId, lease)
			f.publishObservation(accepted.Msg.OperationId, lease, domain.SubscriptionObservationResult{Outcome: outcome, QuotaError: domain.Unavailable})
			_, a = f.record()
			if a.Subscription.Observation.Outcome != outcome || a.Subscription.Observation.Phase != domain.SubscriptionObservationSucceeded || !a.ConfirmedExhausted || a.Subscription.QuotaObservedAt != nil || a.Subscription.QuotaState != domain.ObservationFailed {
				t.Fatal("non-success outcome fabricated quota recovery")
			}
		})
	}
}

func TestSubscriptionQuotaActiveExecutionRetainsSingleNativeOwner(t *testing.T) {
	f := newQuotaFixture(t)
	var original domain.SubscriptionLease
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.original.execution.lease", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		original = domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: r.Revision + 1, Action: domain.SubscriptionExecute, MachineID: f.input.MachineID, InstanceID: f.instance, DeviceID: f.device, Epoch: f.service.subscriptionServerEpoch(), Generation: a.Subscription.Generation, StartedAt: time.Now().UTC()}
		a.Subscription.Lease = &original
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	op := f.requestQuota()
	lease, err := f.take(&pb.RequestSubscriptionResponse{OperationId: op.OperationId}, pb.SubscriptionAction_SUBSCRIPTION_ACTION_QUOTA)
	if err != nil {
		t.Fatal("execution ownership blocked quota admission", err)
	}
	clear(lease.Bundle)
	f.claimObservation(op.OperationId, lease)
	now, remaining := time.Now().UTC(), 0.25
	f.publishObservation(op.OperationId, lease, domain.SubscriptionObservationResult{Quota: &domain.SubscriptionQuotaObservation{ObservedAt: now, Windows: []domain.SubscriptionQuotaWindow{{ID: "codex:primary", Remaining: &remaining}}}})
	_, a := f.record()
	if a.Subscription.Lease.ID != domain.ID(lease.LeaseId) || a.Subscription.Lease.Action != domain.SubscriptionQuota || a.Subscription.QuotaObservedAt == nil {
		t.Fatal("quota replaced its original execution owner")
	}
	// Revocation between acceptance and claim cannot send a queued native call.
	op = f.requestQuota()
	f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	request := &pb.ClaimSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: original.Revision}, LeaseId: string(original.ID), MachineId: string(original.MachineID), InstanceId: string(original.InstanceID), OperationId: op.OperationId, GenerationId: string(original.Generation)}
	if _, err := f.client.ClaimSubscriptionObservation(context.Background(), subscriptionRequest(f.workerToken, request)); rpc.ClientError(err).Code != domain.RecoveryRequired {
		t.Fatal("revoked observation gained native send authority", err)
	}
}
