// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"errors"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// finishCodexScopedRetry reuses the original durable request identifier for
// every ambiguous Finish. A member never sends authentication bytes; the final
// owner Finish alone can write back the account's refreshed generation.
func (l *managedSubscriptionLease) finishCodexScopedRetry(bundle []byte, cleanup, success bool, scope pb.SubscriptionFinishScope, revision uint64) error {
	delay := time.Second
	for attempt := 1; ; attempt++ {
		err := l.finishCodexScoped(bundle, cleanup, success, scope, revision)
		if !codexFinishOutcomeUnknown(err) || attempt >= claudeFinishAttempts {
			return err
		}
		if l.logger != nil {
			l.logger.Warn("codex_finish_retrying", "account_id", l.account, "lease_id", l.response.LeaseId, "finish_scope", scope, "attempt", attempt)
		}
		time.Sleep(delay)
		delay = min(delay*2, 8*time.Second)
	}
}

func codexFinishOutcomeUnknown(err error) bool {
	var uncertain *managedExecutionUncertain
	return errors.As(err, &uncertain) || claudeFinishOutcomeUnknown(err)
}

func (l *managedSubscriptionLease) finishCodexScoped(bundle []byte, cleanup, success bool, scope pb.SubscriptionFinishScope, revision uint64) error {
	if scope == pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER && len(bundle) != 0 {
		return subscription.Invalid()
	}
	if scope != pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER {
		// The execution ended. A shared member's Finish does not end its owner.
		codexLeasesInFlight.Delete(l.response.LeaseId)
	}
	l.journal.State = managedClosed
	l.journal.Cleanup = cleanup
	l.journal.Refresh = false
	l.journal.Success = success
	if err := writeJSON(l.journalPath, l.journal); err != nil {
		return subscription.Invalid()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	response, err := l.client.FinishSubscription(ctx, authenticated(l.credential, &pb.FinishSubscriptionRequest{
		Mutation: &pb.Mutation{RequestId: string(l.finishID), Id: string(l.account), ExpectedRevision: revision},
		LeaseId:  l.response.LeaseId, GenerationId: l.response.GenerationId,
		MachineId: string(l.credential.MachineID), InstanceId: string(l.instance),
		Bundle: bundle, CleanupConfirmed: cleanup, Succeeded: success, FinishScope: scope,
	}))
	if err != nil {
		return rpc.ClientError(err)
	}
	var account domain.Account
	if response == nil || response.Msg == nil || response.Msg.Account == nil || domain.Decode(response.Msg.Account.DocumentJson, &account) != nil || account.Subscription == nil {
		return &managedExecutionUncertain{subscription.Invalid()}
	}
	state := account.Subscription
	leaseID := l.leaseID()
	if scope == pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER {
		member, present := state.Member(leaseID)
		if state.Lease == nil || !state.Lease.Shared || cleanup && present || !cleanup && (!present || member.State != domain.SubscriptionMemberRetained) {
			return &managedExecutionUncertain{subscription.Invalid()}
		}
	} else if state.Lease != nil && (state.Lease.ID == leaseID || state.Lease.Shared && state.Lease.ID == domain.ID(l.response.OwnerLeaseId)) {
		return &managedExecutionUncertain{subscription.Invalid()}
	}
	l.journal.State = managedReported
	if err := writeJSON(l.journalPath, l.journal); err != nil {
		return subscription.Invalid()
	}
	return nil
}
