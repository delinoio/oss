// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type managedObservationOwner struct {
	mu     sync.Mutex
	closed bool
	native *codex.Client
	lease  *managedSubscriptionLease
}
type managedObservationRegistry struct {
	mu     sync.Mutex
	owners map[domain.ID]*managedObservationOwner
}

func (r *managedObservationRegistry) register(account domain.ID, native *codex.Client, lease *managedSubscriptionLease) func() {
	owner := &managedObservationOwner{native: native, lease: lease}
	r.mu.Lock()
	if r.owners == nil {
		r.owners = map[domain.ID]*managedObservationOwner{}
	}
	r.owners[account] = owner
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			if r.owners[account] == owner {
				delete(r.owners, account)
			}
			r.mu.Unlock()
			owner.mu.Lock()
			owner.closed = true
			owner.mu.Unlock()
		})
	}
}
func (r *managedObservationRegistry) run(ctx context.Context, account domain.ID, operation domain.SubscriptionObservationOperation) (bool, error) {
	if r == nil {
		return false, nil
	}
	r.mu.Lock()
	owner := r.owners[account]
	r.mu.Unlock()
	if owner == nil {
		return false, nil
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.closed {
		return false, nil
	}
	return true, owner.lease.observe(ctx, owner.native, operation)
}
func (l *managedSubscriptionLease) observe(ctx context.Context, native *codex.Client, operation domain.SubscriptionObservationOperation) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	claimID := domain.NewID()
	claimed, err := l.client.ClaimSubscriptionObservation(ctx, authenticated(l.credential, &pb.ClaimSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(claimID), Id: string(l.account), ExpectedRevision: l.response.LeaseRevision}, LeaseId: l.response.LeaseId, MachineId: string(l.credential.MachineID), InstanceId: string(l.instance), OperationId: string(operation.ID), GenerationId: l.response.GenerationId}))
	if err != nil {
		if l.logger != nil {
			l.logger.WarnContext(ctx, "subscription_observation_claim_failed", "account_id", l.account, "operation_id", operation.ID, "code", domain.SafeError(rpc.ClientError(err)).Code)
		}
		return rpc.ClientError(err)
	}
	var original domain.SubscriptionObservationOperation
	if domain.Decode(claimed.Msg.OperationJson, &original) != nil || original.Validate() != nil || original.ID != operation.ID || original.Action != operation.Action || original.Generation != domain.ID(l.response.GenerationId) || original.Phase != domain.SubscriptionObservationSending {
		return domain.InvalidSubscriptionObservation()
	}
	result := domain.SubscriptionObservationResult{}
	if original.Action == domain.SubscriptionResetCredit {
		result.Outcome, err = native.ConsumeManagedResetCredit(ctx, original)
		if err != nil {
			result.ConsumeUncertain = true
		}
	}
	observed, readErr := native.ReadManagedQuota(ctx, domain.NewID())
	if readErr != nil {
		result.QuotaError = domain.SafeError(readErr).Code
	} else {
		result.Quota = &observed
	}
	raw, _ := json.Marshal(result)
	reportCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	published, err := l.client.PublishSubscriptionObservation(reportCtx, authenticated(l.credential, &pb.PublishSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(l.account), ExpectedRevision: l.response.LeaseRevision}, LeaseId: l.response.LeaseId, MachineId: string(l.credential.MachineID), InstanceId: string(l.instance), OperationId: string(original.ID), GenerationId: l.response.GenerationId, ObservationJson: raw}))
	if err != nil {
		if l.logger != nil {
			l.logger.WarnContext(reportCtx, "subscription_observation_publication_failed", "account_id", l.account, "operation_id", original.ID, "code", domain.SafeError(rpc.ClientError(err)).Code)
		}
		return rpc.ClientError(err)
	}

	// A stale-inventory refresh can admit only one subsequent consumption while
	// this exact native owner is still held. It never opens an idle native process.
	if original.Action == domain.SubscriptionQuota && original.AutomaticEpisodeID != "" {
		var account domain.Account
		if published.Msg.Account != nil && domain.Decode(published.Msg.Account.DocumentJson, &account) == nil && account.Subscription != nil {
			next := account.Subscription.Observation
			if next != nil && next.ID != original.ID && next.Action == domain.SubscriptionResetCredit && next.AutomaticEpisodeID == original.AutomaticEpisodeID && next.AutomaticLeaseID == domain.ID(l.response.LeaseId) && next.Phase == domain.SubscriptionObservationQueued {
				return l.observe(ctx, native, *next)
			}
		}
	}
	return nil
}
func (l *managedSubscriptionLease) publishQuotaBlock(ctx context.Context, registry *managedObservationRegistry, block domain.SubscriptionQuotaBlock) {
	bounded, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	raw, _ := json.Marshal(domain.SubscriptionObservationResult{QuotaBlock: &block})
	published, err := l.client.PublishSubscriptionObservation(bounded, authenticated(l.credential, &pb.PublishSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(l.account), ExpectedRevision: l.response.LeaseRevision}, LeaseId: l.response.LeaseId, MachineId: string(l.credential.MachineID), InstanceId: string(l.instance), GenerationId: l.response.GenerationId, ObservationJson: raw}))
	if err == nil && published.Msg.Account != nil {
		var account domain.Account
		if domain.Decode(published.Msg.Account.DocumentJson, &account) == nil && account.Subscription != nil {
			op := account.Subscription.Observation
			if op != nil && op.AutomaticBlock != nil && *op.AutomaticBlock == block && op.AutomaticLeaseID == domain.ID(l.response.LeaseId) && op.Phase == domain.SubscriptionObservationQueued {
				_, err = registry.run(ctx, l.account, *op)
			}
		}
	}
	if err != nil && l.logger != nil {
		l.logger.WarnContext(ctx, "automatic_credit_original_owner_failed", "account_id", l.account, "execution_id", block.ExecutionID, "code", domain.SafeError(rpc.ClientError(err)).Code)
	}
}
func (l *managedSubscriptionLease) publishRollingQuota(ctx context.Context, observed domain.SubscriptionQuotaObservation) {
	bounded, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	raw, _ := json.Marshal(domain.SubscriptionObservationResult{Quota: &observed})
	_, err := l.client.PublishSubscriptionObservation(bounded, authenticated(l.credential, &pb.PublishSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(l.account), ExpectedRevision: l.response.LeaseRevision}, LeaseId: l.response.LeaseId, MachineId: string(l.credential.MachineID), InstanceId: string(l.instance), GenerationId: l.response.GenerationId, ObservationJson: raw}))
	if err != nil && l.logger != nil {
		l.logger.WarnContext(bounded, "subscription_rolling_quota_publication_failed", "account_id", l.account, "code", domain.SafeError(rpc.ClientError(err)).Code)
	}
}
