// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Triggers of the single usage-read owner. Each one only asks the owner to
// admit a read; the owner decides, so the schedule, single-flight and backoff
// rules cannot differ between an explicit refresh, Refresh all and maintenance.

// dueClaudeUsageAccounts lists the connected server-token Claude accounts whose
// linked usage connection has a scheduled read due at now.
func (s *Service) dueClaudeUsageAccounts(ctx context.Context, now time.Time) ([]domain.ID, error) {
	var due []domain.ID
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		var err error
		due, err = dueClaudeUsageAccountsTx(tx, now)
		return err
	})
	return due, err
}

func dueClaudeUsageAccountsTx(tx *store.Tx, now time.Time) ([]domain.ID, error) {
	var due []domain.ID
	records, err := all(tx, domain.AccountKind)
	if err != nil {
		return nil, err
	}
	for _, r := range records {
		a, err := store.Decode[domain.Account](r)
		if err != nil {
			return nil, err
		}
		if a.ManagedClaudeAccount() && a.Connection != nil && a.Removal == nil && a.Subscription.ClaudeUsage != nil && a.Subscription.ClaudeUsage.ReadDue(now) {
			due = append(due, r.ID)
		}
	}
	return due, nil
}

// claudeUsageReadRef is one scheduled read that a Refresh all request asked the
// usage-read owner to admit, with the operation ID of its durable claim. The
// request receipt stores it, so a replay of the request reproduces the same
// accounts and resumes the same claims.
type claudeUsageReadRef struct {
	Account   domain.ID `json:"account"`
	Operation domain.ID `json:"operation"`
}

// dueClaudeUsageReadsTx selects, inside the Refresh all receipt transaction, the
// due scheduled reads of this request (bounded per pass like the maintenance
// scan) and allocates each claim's operation ID.
func (s *Service) dueClaudeUsageReadsTx(tx *store.Tx) ([]claudeUsageReadRef, error) {
	if !s.claudeUsageActive() {
		return nil, nil
	}
	due, err := dueClaudeUsageAccountsTx(tx, s.claudeUsageNow())
	if err != nil {
		return nil, err
	}
	if len(due) > claudeUsageScanAdmissionPerPass {
		due = due[:claudeUsageScanAdmissionPerPass]
	}
	reads := make([]claudeUsageReadRef, 0, len(due))
	for _, account := range due {
		reads = append(reads, claudeUsageReadRef{Account: account, Operation: domain.NewID()})
	}
	return reads, nil
}

// admitClaudeUsageReceiptReads admits the reads a Refresh all receipt recorded.
// Each admission is keyed by its recorded operation ID and the request, so a
// replay or a retry after a lost response resumes exactly the unfinished claims
// and never starts a second read for an account whose claim already exists. An
// account whose read is running, suppressed or no longer due is skipped silently.
func (s *Service) admitClaudeUsageReceiptReads(ctx context.Context, actor domain.Principal, request domain.ID, reads []claudeUsageReadRef) {
	for _, read := range reads {
		if ctx.Err() != nil {
			return
		}
		_, err := s.admitClaudeUsageRead(ctx, read.Account, read.Operation, actor, claudeUsageTriggerRefreshAll, claudeUsageFence{}, struct{ Account, Request domain.ID }{read.Account, request})
		switch {
		case err == nil, errors.Is(err, errClaudeUsageNotDue), errors.Is(err, errClaudeUsageBusy):
		default:
			if ctx.Err() == nil {
				s.logger.WarnContext(ctx, "claude_usage_admission_failed", "account_id", read.Account, "trigger", claudeUsageTriggerRefreshAll, "code", domain.SafeError(err).Code)
			}
		}
	}
}

// scheduleClaudeUsageReads admits the due scheduled reads on behalf of actor and
// returns the accounts whose read was admitted. An account whose read is
// already running, suppressed or not due is skipped silently: that is the
// coalescing and backoff of the owner, not a failure.
func (s *Service) scheduleClaudeUsageReads(ctx context.Context, actor domain.Principal, trigger claudeUsageTrigger) []domain.ID {
	if !s.claudeUsageActive() {
		return nil
	}
	due, err := s.dueClaudeUsageAccounts(ctx, s.claudeUsageNow())
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "claude_usage_schedule_scan_failed", "trigger", trigger, "code", domain.SafeError(err).Code)
		}
		return nil
	}
	var admitted []domain.ID
	for _, account := range due {
		if len(admitted) >= claudeUsageScanAdmissionPerPass || ctx.Err() != nil {
			break
		}
		_, err := s.admitClaudeUsageRead(ctx, account, domain.NewID(), actor, trigger, claudeUsageFence{}, struct{ Account domain.ID }{account})
		switch {
		case err == nil:
			admitted = append(admitted, account)
		case errors.Is(err, errClaudeUsageNotDue), errors.Is(err, errClaudeUsageBusy):
		default:
			if ctx.Err() == nil {
				s.logger.WarnContext(ctx, "claude_usage_admission_failed", "account_id", account, "trigger", trigger, "code", domain.SafeError(err).Code)
			}
		}
	}
	return admitted
}

// runClaudeUsageReads is the inactive maintenance trigger. The scan only reads
// the accounts; a read is admitted solely when it is due, so a frequent scan
// never forces a fetch before the four-minute interval.
func (s *Service) runClaudeUsageReads(ctx context.Context) {
	ctx = domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice})
	ticker := time.NewTicker(claudeUsageScanInterval)
	defer ticker.Stop()
	for {
		s.scheduleClaudeUsageReads(ctx, domain.Principal{Type: domain.OwnerDevice}, claudeUsageTriggerMaintenance)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// claudeUsageRefreshAccount reports whether the account of an observation
// request is a server-token Claude account that holds a usage section.
func (s *Service) claudeUsageRefreshAccount(ctx context.Context, id domain.ID) bool {
	found := false
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := accountFromTx(tx, id, 0)
		if err != nil {
			return err
		}
		found = a.ManagedClaudeAccount() && a.Subscription.ClaudeUsage != nil
		return nil
	})
	return err == nil && found
}

// requestClaudeUsageRefresh serves an explicit row or Manage refresh of a Claude
// usage connection through the observation request. It reports handled=false for
// any other account so the existing ChatGPT lane is untouched. The request is
// replay-safe through its durable claim; overlapping requests coalesce into the
// running read; a provider-requested suppression is never bypassed.
func (s *Service) requestClaudeUsageRefresh(ctx context.Context, req *connect.Request[pb.RequestSubscriptionObservationRequest], actor domain.Principal, c string) (response *connect.Response[pb.RequestSubscriptionObservationResponse], handled bool, err error) {
	m := req.Msg.Mutation
	account := domain.ID(m.Id)
	if !s.claudeUsageActive() || !s.claudeUsageRefreshAccount(ctx, account) {
		return nil, false, nil
	}
	fail := func(err error) (*connect.Response[pb.RequestSubscriptionObservationResponse], bool, error) {
		return nil, true, rpc.Error(err, c)
	}
	connection, generation := domain.ID(req.Msg.ConnectionId), domain.ID(req.Msg.GenerationId)
	if req.Msg.Action != pb.SubscriptionObservationAction_SUBSCRIPTION_OBSERVATION_ACTION_QUOTA || req.Msg.MachineId != "" || req.Msg.CreditId != "" || req.Msg.NextCredit || req.Msg.CreditsObservationId != "" || req.Msg.Confirmed || connection.Validate() != nil || generation.Validate() != nil {
		return fail(domain.InvalidSubscriptionObservation())
	}
	operation := domain.ID(m.RequestId)
	input := struct {
		Account, Connection, Generation domain.ID
		Actor                           domain.Principal
	}{account, connection, generation, actor}
	respond := func(replayed bool) (*connect.Response[pb.RequestSubscriptionObservationResponse], bool, error) {
		r, err := s.accountRecord(ctx, account)
		if err != nil {
			return fail(err)
		}
		return connect.NewResponse(&pb.RequestSubscriptionObservationResponse{Account: rpc.Resource(r), OperationId: m.RequestId, Replayed: replayed}), true, nil
	}
	if _, replayed, err := s.Store.Replay(ctx, operation, "claude.usage.read.claim", input); err != nil {
		return fail(err)
	} else if replayed {
		return respond(true)
	}
	_, err = s.admitClaudeUsageRead(ctx, account, operation, actor, claudeUsageTriggerExplicit, claudeUsageFence{connection: connection, generation: generation}, input)
	switch {
	case err == nil:
		s.logger.InfoContext(ctx, "claude_usage_refresh_accepted", "account_id", account, "operation_id", operation, "coalesced", false)
	case errors.Is(err, errClaudeUsageBusy):
		// Single-flight: the running read answers this request too, but only
		// after the request was validated and durably claimed, so an exact retry
		// replays instead of starting another provider read.
		if err := s.claimCoalescedClaudeUsageRefresh(ctx, account, operation, actor, claudeUsageFence{connection: connection, generation: generation}, input); err != nil {
			return fail(err)
		}
		s.logger.InfoContext(ctx, "claude_usage_refresh_accepted", "account_id", account, "operation_id", operation, "coalesced", true)
	default:
		return fail(err)
	}
	return respond(false)
}

// claimCoalescedClaudeUsageRefresh records the receipt of an explicit refresh
// that coalesced into a running read. It applies the admission's validation (a
// linked connection, the confirmed execution connection and generation, a valid
// actor) and writes only the durable claim under the admission's receipt name, so
// the exact retained request replays after the running read finishes instead of
// starting a second provider read.
func (s *Service) claimCoalescedClaudeUsageRefresh(ctx context.Context, account, operation domain.ID, actor domain.Principal, fence claudeUsageFence, input any) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, operation, "claude.usage.read.claim", input, func(tx *store.Tx) (any, error) {
		_, a, err := claudeUsageAccountTx(tx, account, 0)
		if err != nil {
			return nil, err
		}
		usage := a.Subscription.ClaudeUsage
		if a.Connection == nil || a.Removal != nil || usage == nil || usage.Connection != domain.ClaudeUsageLinked {
			return nil, claudeUsageConflict("The Claude usage connection is not linked.")
		}
		if fence.connection != "" && (a.Connection.ID != fence.connection || a.Subscription.Generation != fence.generation) {
			return nil, domain.Fail(domain.Conflict, "The confirmed account generation changed.", "Read the current account and confirm the original operation again before sending.")
		}
		if subscriptionActorValid(tx, actor) != nil {
			return nil, subscriptionDenied()
		}
		return struct{}{}, nil
	})
	return err
}
