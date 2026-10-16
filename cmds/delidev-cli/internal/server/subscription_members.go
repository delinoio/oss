package server

import (
	"context"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// subscriptionMemberNotAdmittedCause is the typed proof the Worker accepts for a
// joined execution member whose lease was released after its commit but before
// the member began. The owner and every sibling keep running.
const subscriptionMemberNotAdmittedCause = "subscription_member_not_admitted"

// subscriptionMemberNotAdmitted is the definite result of a member whose
// admission was rolled back without delivering anything. Unlike
// subscriptionDenied it grants no recovery authority and fences no sibling.
func subscriptionMemberNotAdmitted(code domain.Code) *domain.Error {
	problem := domain.Fail(code, "The execution member was not admitted to the shared account owner.", "No credential was delivered; the account owner and its other members are unchanged.")
	problem.Cause = subscriptionMemberNotAdmittedCause
	return problem
}

// subscriptionFinishInput is the idempotency identity of one managed Codex
// Finish. Scope is omitted for the legacy meaning, so the retained request digest
// of every historical Finish receipt is unchanged and its replay stays valid.
type subscriptionFinishInput struct {
	Account, Lease, Machine, Instance, Generation domain.ID
	Commitment                                    string
	Revision                                      uint64
	Cleanup, Refresh, Success                     bool
	Scope                                         domain.SubscriptionFinishScope `json:",omitempty"`
}

// memberLeaseResolver resolves one exact member of a shared owner inside a
// transaction for the Codex or the device-owned Claude lane. Each lane keeps its
// own account-type and installation checks.
type memberLeaseResolver func(ctx context.Context, tx *store.Tx, account, lease, machine, instance domain.ID, memberScope bool) (store.Record, domain.Account, domain.SubscriptionLease, *domain.SubscriptionMember, error)

// sharedMemberAdmissible proves that one more execute job may join the live
// shared owner. The owner stays bound to its original Runner, device, instance,
// server epoch and credential generation, and an owner with no runnable member is
// already retiring: new work waits for it instead of reviving its runtime.
func (s *Service) sharedMemberAdmissible(state *domain.SubscriptionState, machine, instance, device, operation domain.ID) error {
	busy := func() error {
		return domain.Fail(domain.ResourceExhausted, "The selected account is exclusively leased.", "Wait for its original cleanup; never switch accounts automatically.")
	}
	lease := state.Lease
	if lease == nil || !lease.Shared || lease.Action != domain.SubscriptionExecute {
		return busy()
	}
	if lease.MachineID != machine || lease.InstanceID != instance || lease.DeviceID != device || lease.Epoch != s.subscriptionServerEpoch() || lease.Generation != state.Generation {
		return busy()
	}
	if state.RunnableMembers() == 0 {
		return busy()
	}
	// The complete member set must stay within one recovery settlement request, so
	// a further member waits for the owner's retirement instead of being admitted.
	if len(state.Members) >= domain.MaxSubscriptionMembers {
		return busy()
	}
	// One job is owned by exactly one member, in any state.
	if _, owned := state.MemberByOperation(operation); owned {
		return subscriptionDenied()
	}
	return nil
}

// releaseUndeliveredMember removes exactly one joined member whose Take is
// returning an error after its admission committed. No bundle crossed the
// boundary and no generation changed, so the owner and siblings are untouched.
// The release is a bounded write detached from request cancellation. The caller
// holds the accounts gate.
func (s *Service) releaseUndeliveredMember(ctx context.Context, resolve memberLeaseResolver, account, lease, machine, instance domain.ID, reason string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := s.Store.Mutate(ctx, domain.NewID(), "subscription.member-release", struct{ Account, Lease domain.ID }{account, lease}, func(tx *store.Tx) (any, error) {
		r, a, _, member, err := resolve(ctx, tx, account, lease, machine, instance, true)
		if err != nil {
			return nil, err
		}
		if member == nil || member.State != domain.SubscriptionMemberRunning || a.Subscription.RecoveryRequired {
			return nil, subscriptionDenied()
		}
		a.Subscription.ReleaseMember(lease)
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	})
	if err == nil {
		s.logger.InfoContext(ctx, "subscription_member_released_undelivered", "account_id", account, "lease_id", lease, "reason", reason)
	}
	return err
}

// settleSubscriptionMember releases or retains exactly one execution member. It
// never reads, writes or rotates a credential generation, never touches a
// sibling and never retires the owner. A member whose native or descendant
// closure is unproven stays retained with its workspace and cleanup ownership
// until final owner containment closes; that retention is not a completed Stop.
func (s *Service) settleSubscriptionMember(ctx context.Context, tx *store.Tx, resolve memberLeaseResolver, account, lease, machine, instance, generation domain.ID, revision uint64, cleanup, success bool) (any, error) {
	if err := tx.Authorize(); err != nil {
		return nil, err
	}
	r, a, view, member, err := resolve(ctx, tx, account, lease, machine, instance, true)
	if err != nil {
		return nil, err
	}
	state := a.Subscription
	if member == nil || !state.SharedOwner() || member.State != domain.SubscriptionMemberRunning || state.RecoveryRequired || state.Generation != generation || state.Lease.Generation != generation || view.Revision != revision {
		return nil, subscriptionDenied()
	}
	outcome := "released"
	if cleanup {
		state.ReleaseMember(lease)
	} else {
		state.RetainMember(lease)
		outcome = "retained"
	}
	if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
		return nil, err
	}
	s.logger.InfoContext(ctx, "subscription_member_finished", "account_id", account, "lease_id", lease, "owner_lease_id", state.Lease.ID, "operation_id", view.OperationID, "outcome", outcome, "succeeded", success, "runnable_members", state.RunnableMembers(), "members", len(state.Members))
	return accountReceipt{ID: r.ID}, nil
}

// claudeFinishInput is the idempotency identity of one device-owned Claude
// Finish; Scope is omitted for the legacy meaning exactly like the Codex input.
type claudeFinishInput struct {
	Account, Lease, Machine, Instance, Generation, Profile domain.ID
	Identity                                               string
	Revision                                               uint64
	Cleanup, Success                                       bool
	Scope                                                  domain.SubscriptionFinishScope `json:",omitempty"`
}

// ownerFinishAllowed proves that an owner-scoped Finish may settle the stored
// lease. A shared owner is retired only by the owner scope, a singleton lease
// only by the legacy scope, and a shared owner only after its last runnable
// member (the finishing one, when it names a member identifier).
func ownerFinishAllowed(st *domain.SubscriptionState, scope domain.SubscriptionFinishScope, member *domain.SubscriptionMember) error {
	if st.SharedOwner() != (scope == domain.SubscriptionFinishOwner) {
		return subscriptionDenied()
	}
	if !st.SharedOwner() {
		return nil
	}
	remaining := st.RunnableMembers()
	if member != nil {
		if member.State != domain.SubscriptionMemberRunning {
			return subscriptionDenied()
		}
		remaining--
	}
	if remaining != 0 {
		return domain.Fail(domain.Conflict, "Other execution members still use this account owner.", "Finish every member first; only the last member retires the owner.")
	}
	return nil
}
