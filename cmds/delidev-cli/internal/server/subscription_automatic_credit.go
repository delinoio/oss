// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func (s *Service) SetAutomaticResetCreditConsent(ctx context.Context, req *connect.Request[pb.SetAutomaticResetCreditConsentRequest]) (*connect.Response[pb.SetAutomaticResetCreditConsentResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	actor, err := inboxActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	input := struct {
		Account                domain.ID
		Revision               uint64
		Connection, Generation domain.ID
		Enabled, Confirmed     bool
		Actor                  domain.Principal
	}{domain.ID(m.Id), m.ExpectedRevision, domain.ID(req.Msg.ConnectionId), domain.ID(req.Msg.GenerationId), req.Msg.Enabled, req.Msg.Confirmed, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "subscription.automatic-credit.consent", input, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, input.Account, input.Revision)
		if err != nil {
			return nil, err
		}
		if !quotaAccountReady(a) || a.Subscription.Pending != nil || input.Connection != a.Connection.ID || input.Generation != a.Subscription.Generation || input.Enabled && !input.Confirmed || !input.Enabled && input.Confirmed {
			return nil, subscriptionDenied()
		}
		if err := subscriptionActorValid(tx, actor); err != nil {
			return nil, err
		}
		a.Subscription.AutomaticCreditConsent = nil
		if input.Enabled {
			a.Subscription.AutomaticCreditConsent = &domain.AutomaticResetCreditConsent{Actor: actor, ConnectionID: input.Connection, Generation: input.Generation, ConfirmedAt: tx.ObservationTime()}
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	record, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "automatic_credit_consent_changed", "account_id", input.Account, "enabled", input.Enabled, "request_id", m.RequestId, "replayed", result.Replayed)
	return connect.NewResponse(&pb.SetAutomaticResetCreditConsentResponse{Account: rpc.Resource(record), Replayed: result.Replayed}), nil
}

// Validate the same retained failed Execute job and original Worker/native lease.
// A Sidechat, idle process, text diagnostic or restored/replaced owner cannot
// promote its quota metadata into standing-consent spending authority.
func (s *Service) originalQuotaBlock(tx *store.Tx, account domain.ID, a domain.Account, block domain.SubscriptionQuotaBlock) error {
	st := a.Subscription
	if block.Validate() != nil || !quotaAccountReady(a) || st.Lease == nil || st.Lease.Action != domain.SubscriptionExecute || st.Lease.Epoch != s.subscriptionServerEpoch() || st.Lease.Generation != st.Generation || st.Lease.ForkRevision != 0 {
		return subscriptionDenied()
	}
	lease := st.Lease
	_, machine, err := activeMachine(tx, lease.MachineID)
	if err != nil || !slices.Contains(machine.WorkerCapabilities, domain.CodexQuotaBlockV1) || quotaObservationMachine(tx, a, lease.MachineID, s.subscriptionServerEpoch()) != nil {
		return subscriptionDenied()
	}
	jr, err := tx.Get(domain.JobKind, lease.OperationID)
	if err != nil {
		return subscriptionDenied()
	}
	job, err := store.Decode[domain.Job](jr)
	var input domain.ExecutionJobInput
	if err != nil || job.Type != domain.ExecuteSessionJob || job.State != domain.JobClaimed || job.MachineID != lease.MachineID || job.InstanceID != lease.InstanceID || job.AssignedDeviceID != lease.DeviceID || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.AccountID != account || input.ConnectionID != a.Connection.ID || input.ExecutionID != block.ExecutionID || input.SessionID != block.SessionID || input.Configuration.Harness != domain.Codex || !input.Configuration.Subscription || input.Input.Mode != domain.ExecuteMode {
		return subscriptionDenied()
	}
	_, session, err := sessionRecord(tx, block.SessionID)
	if err != nil || session.Execution == nil {
		return subscriptionDenied()
	}
	progress := session.Execution
	selected := session.ExecutionSelection()
	if progress.JobID != jr.ID || progress.ExecutionID != block.ExecutionID || progress.NativeThreadID != string(block.NativeThreadID) || progress.NativeTurnID != string(block.NativeTurnID) || progress.Outcome != domain.ExecutionFailed || progress.CleanupVerified || selected.AccountID != account || selected.ConnectionID != a.Connection.ID || selected.ID != block.ExecutionID {
		return subscriptionDenied()
	}
	return nil
}

// One account transaction owns both the episode fence and original operation.
// Refresh and consumption reuse only the current active original Execute lease.
// Restart/status cannot replay a completed or sending operation.
func (s *Service) admitAutomaticCredit(tx *store.Tx, account domain.ID, a *domain.Account, block *domain.SubscriptionQuotaBlock, completed domain.ID) error {
	st := a.Subscription
	now := tx.ObservationTime()
	consent := st.AutomaticCreditConsent
	if block != nil {
		if err := s.originalQuotaBlock(tx, account, *a, *block); err != nil {
			return err
		}
		if slices.Contains(st.AutomaticCreditBlocks, *block) {
			return nil
		}
		if len(st.AutomaticCreditBlocks) >= 1024 {
			return nil
		}
		st.AutomaticCreditBlocks = append(st.AutomaticCreditBlocks, *block)
		if st.AutomaticCreditEpisode != nil {
			return nil
		}
		if consent == nil || consent.ConnectionID != a.Connection.ID || consent.Generation != st.Generation || subscriptionActorValid(tx, consent.Actor) != nil {
			return nil
		}
		if block.Reason == domain.CodexRateLimitExceeded && !freshSubscriptionExhaustion(*a, now) {
			return nil
		}
		st.AutomaticCreditEpisode = &domain.AutomaticResetCreditEpisode{ID: domain.NewID(), Generation: st.Generation, SessionID: block.SessionID, LeaseID: st.Lease.ID, ExecutionID: block.ExecutionID, NativeThreadID: block.NativeThreadID, NativeTurnID: block.NativeTurnID, Block: block.Reason, ObservedAt: now}
		if domain.ConfirmedSubscriptionQuotaUsable(*a, now) && st.QuotaObservedAt != nil && now.After(*st.QuotaObservedAt) {
			if _, err := tx.CreateOperationalInbox(st.AutomaticCreditEpisode.ID, domain.InboxOperational{Kind: domain.QuotaExhaustedNotification, AccountID: account, ConnectionID: a.Connection.ID, ObservedAt: now}); err != nil {
				return err
			}
		}
		a.ConfirmedExhausted = true
	}
	episode := st.AutomaticCreditEpisode
	if episode == nil || episode.OperationID != "" || st.Lease == nil || st.Lease.ID != episode.LeaseID || st.Lease.Action != domain.SubscriptionExecute || st.Lease.Epoch != s.subscriptionServerEpoch() || episode.Generation != st.Generation || consent == nil || consent.Generation != st.Generation || consent.ConnectionID != a.Connection.ID || subscriptionActorValid(tx, consent.Actor) != nil || st.Pending != nil || st.ServerObservationActive() || st.Observation != nil && st.Observation.Active() {
		return nil
	}
	if episode.RefreshOperationID != "" && (completed != episode.RefreshOperationID || st.Observation == nil || st.Observation.Phase != domain.SubscriptionObservationSucceeded) {
		return nil
	}
	credit, available := domain.SelectAutomaticResetCredit(st.ResetCredits, now)
	if !available {
		stale := st.ResetCredits == nil || st.ResetCredits.ObservedAt.After(now) || now.Sub(st.ResetCredits.ObservedAt) > 5*time.Minute
		if !stale || episode.RefreshOperationID != "" {
			return nil
		}
		operation := domain.NewID()
		episode.RefreshOperationID = operation
		st.Observation = &domain.SubscriptionObservationOperation{ID: operation, Action: domain.SubscriptionQuota, MachineID: st.Lease.MachineID, Actor: consent.Actor, ConnectionID: a.Connection.ID, Generation: st.Generation, Phase: domain.SubscriptionObservationQueued, RequestedAt: now, AutomaticEpisodeID: episode.ID, AutomaticLeaseID: st.Lease.ID, AutomaticBlock: &domain.SubscriptionQuotaBlock{SessionID: episode.SessionID, ExecutionID: episode.ExecutionID, NativeThreadID: episode.NativeThreadID, NativeTurnID: episode.NativeTurnID, Reason: episode.Block}}
		return nil
	}
	operation := domain.NewID()
	episode.OperationID = operation
	st.Observation = &domain.SubscriptionObservationOperation{ID: operation, Action: domain.SubscriptionResetCredit, MachineID: st.Lease.MachineID, Actor: consent.Actor, ConnectionID: a.Connection.ID, Generation: st.Generation, Phase: domain.SubscriptionObservationQueued, RequestedAt: now, CreditID: credit, NextCredit: credit == "", CreditsObservationID: st.ResetCredits.ObservationID, AutomaticEpisodeID: episode.ID, AutomaticLeaseID: st.Lease.ID, AutomaticBlock: &domain.SubscriptionQuotaBlock{SessionID: episode.SessionID, ExecutionID: episode.ExecutionID, NativeThreadID: episode.NativeThreadID, NativeTurnID: episode.NativeTurnID, Reason: episode.Block}}
	return nil
}
func freshSubscriptionExhaustion(a domain.Account, now time.Time) bool {
	if !a.ConfirmedExhausted || a.Subscription == nil || a.Subscription.QuotaState != domain.Observed || a.Subscription.QuotaObservedAt == nil || a.Subscription.QuotaObservedAt.After(now) || now.Sub(*a.Subscription.QuotaObservedAt) > 5*time.Minute {
		return false
	}
	for _, window := range a.Quota {
		if window.ComparisonGroup == "chatgpt" && window.Blocking && window.State == domain.Observed && window.Remaining != nil && *window.Remaining == 0 && !window.ObservedAt.After(now) && now.Sub(window.ObservedAt) <= 5*time.Minute {
			return true
		}
	}
	return false
}
