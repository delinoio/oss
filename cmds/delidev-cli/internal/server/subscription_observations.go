// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func quotaAccountReady(a domain.Account) bool {
	return a.Type == domain.SubscriptionAccount && a.SubscriptionService == domain.SubscriptionChatGPT && a.Connection != nil && a.Connection.Authentication == domain.SubscriptionAuth && a.Subscription != nil && a.Subscription.Generation != "" && !a.Subscription.RecoveryRequired && a.Removal == nil && a.Health == domain.AccountReady
}
func observationMachine(tx *store.Tx, machine domain.ID) error {
	_, m, err := activeMachine(tx, machine)
	if err != nil {
		return err
	}
	if !slices.Contains(m.WorkerCapabilities, domain.SubscriptionObservationsV1) {
		return domain.Fail(domain.Unsupported, "This Runner Device cannot observe native quota or reset credits.", "Update the Runner Device and verify its managed Codex installation.")
	}
	_, err = subscriptionInstallation(tx, machine)
	return err
}
func acceptSubscriptionObservation(tx *store.Tx, r store.Record, a domain.Account, op domain.SubscriptionObservationOperation) error {
	if !quotaAccountReady(a) || a.Subscription.Pending != nil || a.Subscription.Observation != nil && a.Subscription.Observation.Active() {
		return subscriptionDenied()
	}
	if domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(op.ConnectionID), op.ConnectionID != a.Connection.ID) ||
		op.Generation != a.Subscription.Generation || op.Validate() != nil {
		return domain.Fail(domain.Conflict, "The confirmed account generation changed.", "Read the current account and confirm the original operation again before sending.")
	}
	if a.Subscription.Lease != nil && (a.Subscription.Lease.Action != domain.SubscriptionExecute || a.Subscription.Lease.MachineID != op.MachineID) {
		return subscriptionDenied()
	}
	if err := observationMachine(tx, op.MachineID); err != nil {
		return err
	}
	a.Subscription.Observation = &op
	_, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	return err
}
func (s *Service) RequestSubscriptionObservation(ctx context.Context, req *connect.Request[pb.RequestSubscriptionObservationRequest]) (*connect.Response[pb.RequestSubscriptionObservationResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	actor, err := inboxActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	action := domain.SubscriptionQuota
	if req.Msg.Action == pb.SubscriptionObservationAction_SUBSCRIPTION_OBSERVATION_ACTION_RESET_CREDIT {
		action = domain.SubscriptionResetCredit
	} else if req.Msg.Action != pb.SubscriptionObservationAction_SUBSCRIPTION_OBSERVATION_ACTION_QUOTA {
		return nil, rpc.Error(domain.InvalidSubscriptionObservation(), c)
	}
	input := struct {
		Account   domain.ID
		Revision  uint64
		Operation domain.SubscriptionObservationOperation
		Confirmed bool
	}{domain.ID(m.Id), m.ExpectedRevision, domain.SubscriptionObservationOperation{ID: domain.ID(m.RequestId), Action: action, MachineID: domain.ID(req.Msg.MachineId), Actor: actor, ConnectionID: domain.ID(req.Msg.ConnectionId), Generation: domain.ID(req.Msg.GenerationId), Phase: domain.SubscriptionObservationQueued, CreditID: req.Msg.CreditId, NextCredit: req.Msg.NextCredit, CreditsObservationID: domain.ID(req.Msg.CreditsObservationId)}, req.Msg.Confirmed}
	// RequestedAt belongs to initial transaction acceptance, not retry identity.
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "subscription.observe.request", input, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, input.Account, input.Revision)
		if err != nil {
			return nil, err
		}
		op := input.Operation
		op.RequestedAt = time.Now().UTC()
		if action == domain.SubscriptionResetCredit {
			if !input.Confirmed || !quotaAccountReady(a) || a.Subscription.ResetCredits == nil {
				return nil, domain.InvalidSubscriptionObservation()
			}
			credits := a.Subscription.ResetCredits
			if credits.ObservationID != op.CreditsObservationID || time.Since(credits.ObservedAt) > 5*time.Minute || credits.AvailableCount <= 0 {
				return nil, domain.Fail(domain.Conflict, "The reset-credit inventory changed or is stale.", "Refresh native quota and explicitly confirm the current inventory.")
			}
			if op.NextCredit {
				if credits.Credits != nil {
					return nil, domain.InvalidSubscriptionObservation()
				}
			} else {
				if credits.Credits == nil {
					return nil, domain.InvalidSubscriptionObservation()
				}
				found := false
				for _, credit := range *credits.Credits {
					if credit.ID == op.CreditID && credit.ResetType == "codexRateLimits" && credit.Status == "available" && (credit.ExpiresAt == nil || credit.ExpiresAt.After(op.RequestedAt)) {
						found = true
					}
				}
				if !found {
					return nil, domain.InvalidSubscriptionObservation()
				}
			}
		} else if input.Confirmed {
			return nil, domain.InvalidSubscriptionObservation()
		}
		if err := acceptSubscriptionObservation(tx, r, a, op); err != nil {
			return nil, err
		}
		return accountReceipt{ID: r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	r, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "subscription_observation_accepted", "account_id", input.Account, "operation_id", m.RequestId, "action", action, "replayed", result.Replayed)
	return connect.NewResponse(&pb.RequestSubscriptionObservationResponse{Account: rpc.Resource(r), OperationId: m.RequestId, Replayed: result.Replayed}), nil
}

type subscriptionQuotaBatchReceipt struct {
	Accounts []domain.ID `json:"accounts"`
}

func subscriptionQuotaCandidates(tx *store.Tx, now time.Time, dueOnly bool) ([]store.Record, error) {
	records, err := all(tx, domain.AccountKind)
	if err != nil {
		return nil, err
	}
	result := []store.Record{}
	if len(records) > 10000 {
		return result, domain.Fail(domain.ResourceExhausted, "The native account inventory exceeds its bound.", "Reduce retained account metadata before requesting a complete refresh.")
	}
	for _, r := range records {
		a, err := store.Decode[domain.Account](r)
		if err != nil {
			return result, err
		}
		if !quotaAccountReady(a) || a.Subscription.Pending != nil || a.Subscription.Observation != nil && a.Subscription.Observation.Active() {
			continue
		}
		state := a.Subscription
		if dueOnly && state.Observation != nil && now.Sub(state.Observation.RequestedAt) < 5*time.Minute {
			continue
		}
		machine := state.OwnerMachineID
		if state.Lease != nil {
			if state.Lease.Action != domain.SubscriptionExecute {
				continue
			}
			machine = state.Lease.MachineID
		}
		if machine == "" || observationMachine(tx, machine) != nil {
			continue
		}
		result = append(result, r)
	}
	return result, nil
}
func queueSubscriptionQuotas(tx *store.Tx, actor domain.Principal, now time.Time, dueOnly bool) (subscriptionQuotaBatchReceipt, error) {
	records, err := subscriptionQuotaCandidates(tx, now, dueOnly)
	result := subscriptionQuotaBatchReceipt{Accounts: []domain.ID{}}
	if err != nil {
		return result, err
	}
	if dueOnly && len(records) == 0 {
		return result, errNoDueSubscriptionQuota
	}
	for _, r := range records {
		a, err := store.Decode[domain.Account](r)
		if err != nil {
			return result, err
		}
		machine := a.Subscription.OwnerMachineID
		if a.Subscription.Lease != nil {
			machine = a.Subscription.Lease.MachineID
		}
		op := domain.SubscriptionObservationOperation{ID: domain.NewID(), Action: domain.SubscriptionQuota, MachineID: machine, Actor: actor, ConnectionID: a.Connection.ID, Generation: a.Subscription.Generation, Phase: domain.SubscriptionObservationQueued, RequestedAt: now}
		if err := acceptSubscriptionObservation(tx, r, a, op); err != nil {
			return result, err
		}
		result.Accounts = append(result.Accounts, r.ID)
	}
	return result, nil
}

// Keep this private rollback signal typed so Store.Mutate preserves its identity.
// It never crosses RPC: only background maintenance treats it as an empty pass.
var errNoDueSubscriptionQuota = domain.Fail(domain.Conflict, "No native quota observation is due.", "")

func (s *Service) queueDueSubscriptionQuotas(ctx context.Context, now time.Time) error {
	var due bool
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		records, err := subscriptionQuotaCandidates(tx, now, true)
		due = len(records) > 0
		return err
	}); err != nil {
		return err
	}
	if !due {
		return nil
	}
	// Recheck inside the write transaction. A concurrent owner may have queued
	// every candidate; roll back that no-op instead of publishing an empty receipt.
	_, err := s.Store.Mutate(ctx, domain.NewID(), "subscription.quota.maintenance", struct{}{}, func(tx *store.Tx) (any, error) {
		return queueSubscriptionQuotas(tx, domain.Principal{Type: domain.OwnerDevice}, now, true)
	})
	if errors.Is(err, errNoDueSubscriptionQuota) {
		return nil
	}
	return err
}
func (s *Service) RefreshAllSubscriptionQuotas(ctx context.Context, req *connect.Request[pb.RefreshAllSubscriptionQuotasRequest]) (*connect.Response[pb.RefreshAllSubscriptionQuotasResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := inboxActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "subscription.quota.all", actor, func(tx *store.Tx) (any, error) { return queueSubscriptionQuotas(tx, actor, time.Now().UTC(), false) })
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	var receipt subscriptionQuotaBatchReceipt
	if domain.Decode(result.Data, &receipt) != nil {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	response := &pb.RefreshAllSubscriptionQuotasResponse{RequestId: req.Msg.RequestId, Replayed: result.Replayed}
	for _, id := range receipt.Accounts {
		response.Accounts = append(response.Accounts, string(id))
	}
	return connect.NewResponse(response), nil
}
func (s *Service) runSubscriptionQuotaMaintenance(ctx context.Context) {
	ctx = domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice})
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		err := s.queueDueSubscriptionQuotas(ctx, time.Now().UTC())
		if err != nil && ctx.Err() == nil {
			s.logger.WarnContext(ctx, "subscription_quota_maintenance_failed", "code", domain.SafeError(err).Code)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Service) ReconcileSubscriptionCredit(ctx context.Context, req *connect.Request[pb.ReconcileSubscriptionCreditRequest]) (*connect.Response[pb.ReconcileSubscriptionCreditResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	actor, err := inboxActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	input := struct {
		Account, Operation, Connection, Generation domain.ID
		Revision                                   uint64
		Actor                                      domain.Principal
	}{domain.ID(m.Id), domain.ID(req.Msg.OperationId), domain.ID(req.Msg.ConnectionId), domain.ID(req.Msg.GenerationId), m.ExpectedRevision, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "subscription.credit.reconcile", input, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, input.Account, input.Revision)
		if err != nil {
			return nil, err
		}
		if !quotaAccountReady(a) || a.Subscription.Pending != nil || a.Connection.ID != input.Connection || a.Subscription.Generation != input.Generation {
			return nil, subscriptionDenied()
		}
		op := a.Subscription.Observation
		if op == nil || op.ID != input.Operation || op.Action != domain.SubscriptionResetCredit || op.Phase != domain.SubscriptionObservationUncertain ||
			domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(op.ConnectionID), op.ConnectionID != input.Connection) {
			return nil, domain.InvalidSubscriptionObservation()
		}
		if a.Subscription.Lease != nil && a.Subscription.Lease.Action != domain.SubscriptionExecute {
			return nil, subscriptionDenied()
		}
		if a.Subscription.Lease != nil {
			op.MachineID = a.Subscription.Lease.MachineID
		}
		if err := observationMachine(tx, op.MachineID); err != nil {
			return nil, err
		}
		op.Generation = input.Generation
		op.Actor = actor
		op.Phase = domain.SubscriptionObservationQueued
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	r, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	return connect.NewResponse(&pb.ReconcileSubscriptionCreditResponse{Account: rpc.Resource(r), Replayed: result.Replayed}), nil
}
func (s *Service) ClaimSubscriptionObservation(ctx context.Context, req *connect.Request[pb.ClaimSubscriptionObservationRequest]) (*connect.Response[pb.ClaimSubscriptionObservationResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	input := struct {
		Account, Lease, Machine, Instance, Operation, Generation domain.ID
		Revision                                                 uint64
	}{domain.ID(m.Id), domain.ID(req.Msg.LeaseId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.OperationId), domain.ID(req.Msg.GenerationId), m.ExpectedRevision}
	var operation domain.SubscriptionObservationOperation
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "subscription.observation.claim", input, func(tx *store.Tx) (any, error) {
		r, a, err := s.subscriptionLease(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance)
		if err != nil {
			return nil, err
		}
		if !quotaAccountReady(a) || a.Subscription.Lease.Revision != input.Revision || a.Subscription.Generation != input.Generation || a.Subscription.Pending != nil {
			return nil, subscriptionDenied()
		}
		op := a.Subscription.Observation
		if op == nil || op.ID != input.Operation || op.Generation != input.Generation ||
			domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(op.ConnectionID), op.ConnectionID != a.Connection.ID) ||
			op.Phase != domain.SubscriptionObservationQueued ||
			domain.OwnershipBlocks(domain.OwnershipMachine, domain.ID(op.MachineID), op.MachineID != input.Machine) ||
			subscriptionActorValid(tx, op.Actor) != nil {
			return nil, subscriptionDenied()
		}
		op.Phase = domain.SubscriptionObservationSending
		operation = *op
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if result.Replayed {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	raw, _ := json.Marshal(operation)
	return connect.NewResponse(&pb.ClaimSubscriptionObservationResponse{OperationJson: raw}), nil
}
func (s *Service) PublishSubscriptionObservation(ctx context.Context, req *connect.Request[pb.PublishSubscriptionObservationRequest]) (*connect.Response[pb.PublishSubscriptionObservationResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	var observed domain.SubscriptionObservationResult
	if len(req.Msg.ObservationJson) > 32<<10 || domain.Decode(req.Msg.ObservationJson, &observed) != nil || !domain.ValidSubscriptionObservationCode(observed.QuotaError) || observed.Quota != nil && observed.Quota.Validate(time.Now().UTC()) != nil || observed.Quota != nil && observed.QuotaError != "" || observed.Outcome != "" && !observed.Outcome.Valid() {
		return nil, rpc.Error(domain.InvalidSubscriptionObservation(), c)
	}
	input := struct {
		Account, Lease, Machine, Instance, Operation, Generation domain.ID
		Revision                                                 uint64
		Observed                                                 domain.SubscriptionObservationResult
	}{domain.ID(m.Id), domain.ID(req.Msg.LeaseId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.OperationId), domain.ID(req.Msg.GenerationId), m.ExpectedRevision, observed}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "subscription.observation.publish", input, func(tx *store.Tx) (any, error) {
		r, a, err := s.subscriptionLease(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance)
		if err != nil {
			return nil, err
		}
		state := a.Subscription
		if state.RecoveryRequired || state.Lease.Revision != input.Revision || state.Generation != input.Generation {
			return nil, subscriptionDenied()
		}
		source := domain.ID(m.RequestId)
		if input.Operation != "" {
			op := state.Observation
			if op == nil || op.ID != input.Operation || op.Phase != domain.SubscriptionObservationSending || op.Generation != input.Generation ||
				domain.OwnershipBlocks(domain.OwnershipMachine, domain.ID(op.MachineID), op.MachineID != input.Machine) {
				return nil, subscriptionDenied()
			}
			if op.Action == domain.SubscriptionQuota && (observed.Outcome != "" || observed.ConsumeUncertain) || op.Action == domain.SubscriptionResetCredit && (observed.ConsumeUncertain == (observed.Outcome != "")) {
				return nil, domain.InvalidSubscriptionObservation()
			}
			source = op.ID
			op.Outcome = observed.Outcome
			op.ErrorCode = observed.QuotaError
			op.Phase = domain.SubscriptionObservationSucceeded
			if observed.ConsumeUncertain {
				op.Phase = domain.SubscriptionObservationUncertain
			} else if op.Action == domain.SubscriptionQuota && observed.Quota == nil {
				op.Phase = domain.SubscriptionObservationFailed
			}
		} else if !quotaAccountReady(a) || state.Pending != nil || observed.Outcome != "" || observed.ConsumeUncertain || state.Lease.Action != domain.SubscriptionExecute {
			return nil, domain.InvalidSubscriptionObservation()
		}
		recovered := false
		if observed.Quota != nil && quotaAccountReady(a) && state.Pending == nil {
			recovered, err = domain.ApplySubscriptionQuota(&a, *observed.Quota, time.Now().UTC())
			if err != nil {
				return nil, err
			}
		} else if observed.Quota == nil {
			if observed.QuotaError == "" {
				return nil, domain.InvalidSubscriptionObservation()
			}
			state.QuotaState = domain.ObservationFailed
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		if recovered {
			if _, err := tx.CreateSubscriptionRecoveryInbox(r.ID, source, a.Connection.ID, observed.Quota.ObservedAt); err != nil {
				return nil, err
			}
		}
		return accountReceipt{ID: r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	r, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "subscription_observation_published", "account_id", input.Account, "operation_id", input.Operation, "generation", input.Generation, "quota_code", observed.QuotaError, "consume_uncertain", observed.ConsumeUncertain, "replayed", result.Replayed)
	return connect.NewResponse(&pb.PublishSubscriptionObservationResponse{Account: rpc.Resource(r), Replayed: result.Replayed}), nil
}
