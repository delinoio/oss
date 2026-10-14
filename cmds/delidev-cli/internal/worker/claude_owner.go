// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Device-owned Claude accounts negotiate ClaudeDeviceOwnedMembersV1. One Worker
// process then holds one account owner per connected account: the owner keeps the
// canonical profile lock for its whole lifetime (account lifecycle exclusion) and
// every session is an execution member with its own process owner, workspace and
// publication journal. Native code alone reads and renews the authentication in
// the canonical secure-storage directory; this file never opens it.

type claudeMemberState string

const (
	claudeMemberRunning  claudeMemberState = "running"
	claudeMemberReleased claudeMemberState = "released"
	// claudeMemberRetained reported unproven native or descendant closure. Its
	// cleanup ownership waits for the final owner containment join.
	claudeMemberRetained claudeMemberState = "retained"
)

// claudeProfileVersion names the history representation of one execution.
type claudeProfileVersion uint32

const (
	// claudeProfileV1 keeps a retained history in the canonical profile directory.
	claudeProfileV1 claudeProfileVersion = 1
	// claudeProfileV2 keeps session-private configuration and history and names
	// the canonical profile only as the secure-storage namespace.
	claudeProfileV2 claudeProfileVersion = 2
)

// claudeOwnerRegistry is created once per Worker connection.
type claudeOwnerRegistry struct {
	mu       sync.Mutex
	accounts map[domain.ID]*claudeAccountGate
}

func newClaudeOwnerRegistry() *claudeOwnerRegistry {
	return &claudeOwnerRegistry{accounts: map[domain.ID]*claudeAccountGate{}}
}

func (r *claudeOwnerRegistry) gate(account domain.ID) *claudeAccountGate {
	r.mu.Lock()
	defer r.mu.Unlock()
	g := r.accounts[account]
	if g == nil {
		g = &claudeAccountGate{slot: make(chan struct{}, 1), legacy: make(chan struct{}, 1)}
		r.accounts[account] = g
	}
	return g
}

// claudeAccountGate serializes admission and retirement of one account's owner.
// The slot is held only around one Take request plus local registration, and
// around owner retirement, never across a busy wait.
type claudeAccountGate struct {
	slot chan struct{}
	// legacy serializes executions whose retained history lives in the canonical
	// profile directory (version 1), exactly like the historical exclusive lease.
	legacy chan struct{}
	mu     sync.Mutex
	owner  *claudeSharedOwner
}

func (g *claudeAccountGate) acquire(ctx context.Context) error {
	select {
	case g.slot <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *claudeAccountGate) release() { <-g.slot }

// acquireBounded is the finish-side acquisition. Completion cannot be abandoned
// by the cancellation that is stopping its session, so it waits independently but
// not forever.
func (g *claudeAccountGate) acquireBounded() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return g.acquire(ctx)
}

type claudeSharedOwner struct {
	config   Config
	lease    *managedSubscriptionLease // founding Take: owner identifier and revision
	profile  *nativeClaudeProfile      // holds the canonical profile lock
	members  map[domain.ID]*claudeSharedMember
	runnable int
	// uncertain records a member Finish the server definitively refused, so the
	// final owner Finish can never claim a clean containment.
	uncertain bool
	// closed records that retirement released the canonical profile lock.
	closed bool
	// containment is the owner Finish evidence computed by retirement. It is
	// fixed once the profile closes so an owner Finish retry reports the same
	// request, never a re-derived one.
	identity  *pb.NativeSubscriptionIdentity
	contained bool
	retained  int
	// resume continues a retirement that could not complete: a member Finish or
	// the owner Finish whose outcome stayed unknown after bounded retries. The
	// admission gate runs it before the next member can join.
	resume func() error
}

type claudeSharedMember struct {
	lease    *managedSubscriptionLease
	job      domain.ID
	state    claudeMemberState
	closeRPC func()
	// pendingFinish records a member Finish whose outcome is unknown after
	// bounded retries. The server may still count this member as runnable, so the
	// same idempotent Finish is retried before the owner can retire.
	pendingFinish bool
	pendingJoined bool
}

// claudeFinishAttempts bounds the idempotent retry of one Finish whose outcome
// is unknown. Every attempt reuses the same request identifier and revision, so
// the server either replays the committed result or commits it exactly once.
const claudeFinishAttempts = 4

var claudeFinishRetryDelay = time.Second

// claudeFinishOutcomeUnknown reports a transport-level failure: the server may
// or may not have committed the Finish. A typed server refusal is definite.
func claudeFinishOutcomeUnknown(err error) bool {
	var uncertain *managedExecutionUncertain
	if err == nil || errors.As(err, &uncertain) {
		return false
	}
	return domain.SafeError(err).Code == domain.ServerUnavailable
}

// finishClaudeScopedRetry reports one scoped Finish and retries only an unknown
// outcome with the identical idempotent request.
func (l *managedSubscriptionLease) finishClaudeScopedRetry(identity *pb.NativeSubscriptionIdentity, cleanup, success bool, scope pb.SubscriptionFinishScope, revision uint64) error {
	delay := claudeFinishRetryDelay
	for attempt := 1; ; attempt++ {
		err := l.finishClaudeScoped(identity, cleanup, success, scope, revision)
		if !claudeFinishOutcomeUnknown(err) || attempt >= claudeFinishAttempts {
			return err
		}
		if l.logger != nil {
			l.logger.Warn("claude_finish_retrying", "account_id", l.account, "lease_id", l.response.LeaseId, "finish_scope", scope, "attempt", attempt)
		}
		time.Sleep(delay)
		delay = min(delay*2, 8*time.Second)
	}
}

func (m *claudeSharedMember) close() {
	if m.closeRPC != nil {
		m.closeRPC()
		m.closeRPC = nil
	}
}

// claudeProfileVersionFor decides which history representation one execution
// uses. A fresh session, or a continuation of a version 2 history, is version 2;
// a continuation of a version 1 history keeps its canonical-profile history.
func claudeProfileVersionFor(root string, input domain.ExecutionJobInput) (claudeProfileVersion, error) {
	if input.Continuation == nil {
		return claudeProfileV2, nil
	}
	ref, err := claudeContinuationReference(root, input)
	if err != nil {
		return 0, err
	}
	checkpoint, err := readClaudeExecutionCheckpoint(root, ref)
	if err != nil {
		return 0, err
	}
	if checkpoint.ProfileVersion == uint32(claudeProfileV2) {
		return claudeProfileV2, nil
	}
	return claudeProfileV1, nil
}

// beginClaudeSharedExecution admits one device-owned Claude session as an
// execution member. The first session of an idle account founds the owner and
// opens the canonical profile under its lock; later sessions join it and open
// nothing shared.
func beginClaudeSharedExecution(ctx context.Context, config Config, jobOwner domain.ID, input domain.ExecutionJobInput) (*nativeClaudeProfile, *managedClaudeToken, func(bool) error, error) {
	connection := config.execution
	version, err := claudeProfileVersionFor(config.Root, input)
	if err != nil {
		return nil, nil, nil, err
	}
	client, closeHTTP, err := subscriptionRPC(ctx, config, connection.Credential)
	if err != nil {
		return nil, nil, nil, err
	}
	gate := config.claudeOwners.gate(input.AccountID)
	releaseHistory := func() {}
	if version == claudeProfileV1 {
		// A retained canonical-profile history runs one session at a time, as it
		// always has; session-private histories run concurrently.
		release, err := gate.acquireLegacyHistory(ctx)
		if err != nil {
			closeHTTP()
			return nil, nil, nil, err
		}
		releaseHistory = release
	}
	lease, release, err := takeManagedSubscriptionAdmitted(ctx, config, client, connection.Credential, connection.Instance, input.AccountID, jobOwner, connection.Assignment.Revision, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE, gate)
	defer release()
	if err != nil {
		closeHTTP()
		releaseHistory()
		return nil, nil, nil, err
	}
	if len(lease.response.Bundle) != 0 {
		// A server-owned token account stays exclusive and never joins an owner.
		releaseHistory()
		return claudeTokenExecution(config, input, lease, closeHTTP)
	}
	if domain.ID(lease.response.NativeProfileId).Validate() != nil {
		closeHTTP()
		releaseHistory()
		return nil, nil, nil, subscription.Invalid()
	}
	member := &claudeSharedMember{lease: lease, job: jobOwner, state: claudeMemberRunning, closeRPC: closeHTTP}
	var profile *nativeClaudeProfile
	var finish func(bool) error
	if lease.response.OwnerLeaseId == "" {
		profile, _, finish, err = gate.found(ctx, config, input, member, version)
	} else {
		profile, _, finish, err = gate.join(config, member, version)
	}
	if err != nil {
		releaseHistory()
		return nil, nil, nil, err
	}
	return profile, nil, func(joined bool) error {
		defer releaseHistory()
		return finish(joined)
	}, nil
}

// found opens the canonical profile for a newly granted owner. It runs under the
// admission gate, so no earlier owner can still hold the profile lock.
func (g *claudeAccountGate) found(ctx context.Context, config Config, input domain.ExecutionJobInput, member *claudeSharedMember, version claudeProfileVersion) (*nativeClaudeProfile, *managedClaudeToken, func(bool) error, error) {
	connection := config.execution
	lease := member.lease
	g.mu.Lock()
	existing := g.owner
	// An owner whose last member already finished is a retirement that never
	// received its Finish acknowledgment (a transport interruption, or recovery
	// that cleaned the server lease). The server grants a founding lease only on
	// an account with no lease, so this fresh founding Take proves the old owner
	// is settled. It was retired under this gate, so nothing else can be using it.
	stale := existing != nil && existing.runnable == 0
	if stale {
		g.owner = nil
	}
	g.mu.Unlock()
	if stale {
		if !existing.closed {
			// Retirement stopped before it released the profile lock (a member
			// Finish stayed unacknowledged). The fresh founding Take proves the
			// server settled the account, so release the lock before reopening.
			_ = existing.profile.close()
			existing.closed = true
		}
		if config.Logger != nil {
			config.Logger.WarnContext(ctx, "claude_shared_owner_stale_cleared", "account_id", input.AccountID, "stale_owner_lease_id", existing.lease.response.LeaseId, "owner_lease_id", lease.response.LeaseId)
		}
	} else if existing != nil {
		// The server granted a new owner while this Worker still tracks a live
		// one. Fence instead of guessing which representation is authoritative.
		member.close()
		return nil, nil, nil, &managedExecutionUncertain{subscription.Invalid()}
	}
	failFounding := func(problem error) (*nativeClaudeProfile, *managedClaudeToken, func(bool) error, error) {
		finishErr := lease.finishClaudeScopedRetry(nil, false, false, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER, lease.response.LeaseRevision)
		member.close()
		if claudeFinishOutcomeUnknown(finishErr) {
			// The server may still hold the founding lease. Track it as a retiring
			// owner so the gate resumes the same idempotent Finish before any join.
			pending := &claudeSharedOwner{config: config, lease: lease, members: map[domain.ID]*claudeSharedMember{}, closed: true, uncertain: true}
			pending.resume = func() error {
				err := withFreshClient(config, lease, func() error {
					return lease.finishClaudeScopedRetry(nil, false, false, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER, lease.response.LeaseRevision)
				})
				if err == nil {
					g.mu.Lock()
					if g.owner == pending {
						g.owner = nil
					}
					pending.resume = nil
					g.mu.Unlock()
				}
				return err
			}
			g.mu.Lock()
			g.owner = pending
			g.mu.Unlock()
		}
		return nil, nil, nil, problem
	}
	profile, err := openClaudeProfile(config, connection.Credential, input.AccountID, domain.ID(lease.response.NativeProfileId), input.Installation, domain.ID(lease.response.LeaseId), false)
	if err == nil {
		if err = security.PrivateDir(filepath.Join(config.Root, "processes", lease.response.LeaseId)); err == nil {
			lease.journal.NativeStarted = true
			err = writeJSON(lease.journalPath, lease.journal)
		}
	}
	if err == nil {
		err = claude.VerifyAuthVersion(ctx, profile.config)
	}
	if err == nil {
		_, err = profile.identity(ctx, config)
	}
	if err != nil {
		if profile != nil {
			_ = profile.close()
		}
		return failFounding(err)
	}
	owner := &claudeSharedOwner{config: config, lease: lease, profile: profile, members: map[domain.ID]*claudeSharedMember{lease.leaseID(): member}, runnable: 1}
	g.mu.Lock()
	g.owner = owner
	g.mu.Unlock()
	if config.Logger != nil {
		config.Logger.InfoContext(ctx, "claude_shared_owner_founded", "account_id", input.AccountID, "owner_lease_id", lease.response.LeaseId, "operation_id", member.job)
	}
	return owner.memberProfile(member, version), nil, g.finisher(owner, member), nil
}

// join registers one more member on the live owner. The member holds no profile
// lock and opens no authentication: it only receives the owner's directories.
func (g *claudeAccountGate) join(config Config, member *claudeSharedMember, version claudeProfileVersion) (*nativeClaudeProfile, *managedClaudeToken, func(bool) error, error) {
	g.mu.Lock()
	owner := g.owner
	if owner != nil && owner.lease.response.LeaseId == member.lease.response.OwnerLeaseId && owner.resume != nil {
		// A previous retirement could not finish: the server still holds the
		// owner. The member just admitted must not run on a retiring owner. Release
		// it alone, then finish the outstanding retirement before any later join.
		resume := owner.resume
		g.mu.Unlock()
		released := member.lease.finishClaudeScopedRetry(nil, true, false, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, member.lease.response.LeaseRevision)
		member.close()
		if released != nil {
			return nil, nil, nil, &managedExecutionUncertain{released}
		}
		err := resume()
		if config.Logger != nil {
			config.Logger.WarnContext(context.Background(), "claude_shared_retirement_resumed", "account_id", member.lease.account, "owner_lease_id", owner.lease.response.LeaseId, "completed", err == nil)
		}
		return nil, nil, nil, &managedTakeNotDelivered{domain.Fail(domain.Unavailable, "The Claude account owner was still retiring.", "Retry the session once the account owner has retired.")}
	}
	if owner == nil || owner.lease.response.LeaseId != member.lease.response.OwnerLeaseId || owner.uncertain {
		// The server and this Worker disagree about the owner. The member has
		// started nothing, so release it alone and report uncertainty. A release
		// whose outcome is unknown stays pending on a tracked owner so the owner
		// Finish cannot be refused later by a member the server still counts.
		sameOwner := owner != nil && owner.lease.response.LeaseId == member.lease.response.OwnerLeaseId
		g.mu.Unlock()
		if err := member.lease.finishClaudeScopedRetry(nil, true, false, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, member.lease.response.LeaseRevision); claudeFinishOutcomeUnknown(err) && sameOwner {
			member.state, member.pendingFinish, member.pendingJoined = claudeMemberReleased, true, true
			g.mu.Lock()
			owner.members[member.lease.leaseID()] = member
			g.mu.Unlock()
		}
		member.close()
		return nil, nil, nil, &managedExecutionUncertain{subscription.Invalid()}
	}
	owner.members[member.lease.leaseID()] = member
	owner.runnable++
	g.mu.Unlock()
	if config.Logger != nil {
		config.Logger.InfoContext(context.Background(), "claude_shared_member_joined", "account_id", member.lease.account, "owner_lease_id", owner.lease.response.LeaseId, "lease_id", member.lease.response.LeaseId, "operation_id", member.job)
	}
	return owner.memberProfile(member, version), nil, g.finisher(owner, member), nil
}

func (l *managedSubscriptionLease) leaseID() domain.ID { return domain.ID(l.response.LeaseId) }

// memberProfile is the per-session view of the owner's canonical profile: the
// same directories and identity, no lock, and the member's own lease journal.
func (o *claudeSharedOwner) memberProfile(member *claudeSharedMember, version claudeProfileVersion) *nativeClaudeProfile {
	view := *o.profile
	view.lock = nil
	view.managedLease = member.lease
	view.historyVersion = version
	return &view
}

func (g *claudeAccountGate) finisher(owner *claudeSharedOwner, member *claudeSharedMember) func(bool) error {
	return func(joined bool) error { return g.finishMember(owner, member, joined) }
}

// finishMember settles one session. A non-final member only releases (or, for an
// unproven closure, retains) itself and never touches siblings or authentication.
// The last runnable member retires the owner: it joins every retained member,
// confirms the native identity through the CLI's own status, releases the
// canonical profile lock and only then reports the owner Finish.
func (g *claudeAccountGate) finishMember(owner *claudeSharedOwner, member *claudeSharedMember, joined bool) (returned error) {
	defer member.close()
	if err := g.acquireBounded(); err != nil {
		return &managedExecutionUncertain{domain.SafeError(err)}
	}
	defer g.release()
	g.mu.Lock()
	if member.state != claudeMemberRunning {
		g.mu.Unlock()
		return &managedExecutionUncertain{subscription.Invalid()}
	}
	if joined {
		member.state = claudeMemberReleased
	} else {
		member.state = claudeMemberRetained
	}
	owner.runnable--
	last := owner.runnable == 0
	revision := member.lease.response.LeaseRevision
	g.mu.Unlock()
	logger := owner.config.Logger
	if !last {
		err := member.lease.finishClaudeScopedRetry(nil, joined, joined, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, revision)
		if err != nil {
			g.mu.Lock()
			if claudeFinishOutcomeUnknown(err) {
				// The server may still count this member as runnable. Keep it as a
				// pending member Finish; the last member settles it before the owner
				// Finish, which the server would otherwise refuse as conflicting.
				member.pendingFinish, member.pendingJoined = true, joined
			} else {
				owner.uncertain = true
			}
			g.mu.Unlock()
			if logger != nil {
				logger.WarnContext(context.Background(), "claude_shared_member_finish_pending", "account_id", member.lease.account, "owner_lease_id", owner.lease.response.LeaseId, "lease_id", member.lease.response.LeaseId, "outcome_unknown", claudeFinishOutcomeUnknown(err), "code", domain.SafeError(err).Code)
			}
			return err
		}
		g.mu.Lock()
		if joined {
			delete(owner.members, member.lease.leaseID())
		}
		g.mu.Unlock()
		if logger != nil {
			logger.InfoContext(context.Background(), "claude_shared_member_finished", "account_id", member.lease.account, "owner_lease_id", owner.lease.response.LeaseId, "lease_id", member.lease.response.LeaseId, "released", joined)
		}
		return nil
	}
	return g.retire(owner, member, joined)
}

// withFreshClient runs one Finish attempt over a newly built transport. A member
// closes its own transport as soon as its session ends, so a Finish resumed
// later must not reuse it. A transport that cannot be built is an unknown
// outcome, never a definite refusal.
func withFreshClient(config Config, lease *managedSubscriptionLease, attempt func() error) error {
	if config.execution == nil {
		return attempt()
	}
	ctx, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	client, closeRPC, err := subscriptionRPC(ctx, config, config.execution.Credential)
	if err != nil {
		return domain.Fail(domain.ServerUnavailable, "The DeliDev server transport could not be opened.", "Retry once the DeliDev server is reachable.")
	}
	defer closeRPC()
	lease.client = client
	return attempt()
}

// settlePendingMembers retries, with fresh transport, every member Finish whose
// outcome stayed unknown. It reports whether no unsettled member remains. The
// caller holds the admission gate.
func (g *claudeAccountGate) settlePendingMembers(owner *claudeSharedOwner) bool {
	g.mu.Lock()
	var pending []*claudeSharedMember
	for _, m := range owner.members {
		if m.pendingFinish {
			pending = append(pending, m)
		}
	}
	g.mu.Unlock()
	if len(pending) == 0 {
		return true
	}
	config := owner.config
	settled := true
	for _, m := range pending {
		err := withFreshClient(config, m.lease, func() error {
			return m.lease.finishClaudeScopedRetry(nil, m.pendingJoined, m.pendingJoined, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, m.lease.response.LeaseRevision)
		})
		if claudeFinishOutcomeUnknown(err) {
			settled = false
			continue
		}
		g.mu.Lock()
		m.pendingFinish = false
		if err != nil {
			owner.uncertain = true
		} else if m.pendingJoined {
			delete(owner.members, m.lease.leaseID())
		}
		g.mu.Unlock()
		if config.Logger != nil {
			config.Logger.InfoContext(context.Background(), "claude_shared_member_finish_settled", "account_id", m.lease.account, "owner_lease_id", owner.lease.response.LeaseId, "lease_id", m.lease.response.LeaseId, "acknowledged", err == nil)
		}
	}
	return settled
}

// retire runs under the admission gate, so no new member can join while the
// owner closes. A retirement that cannot finish records itself in owner.resume;
// the gate completes it before any later member joins.
func (g *claudeAccountGate) retire(owner *claudeSharedOwner, last *claudeSharedMember, joined bool) error {
	config := owner.config
	incomplete := func(err error) error {
		g.mu.Lock()
		owner.resume = func() error { return g.retire(owner, last, joined) }
		g.mu.Unlock()
		return err
	}
	if !owner.closed {
		// The server refuses the owner Finish while any member it still counts as
		// runnable exists, so every unacknowledged member Finish settles first. The
		// profile stays open (lock held) until they do.
		if !g.settlePendingMembers(owner) {
			if config.Logger != nil {
				config.Logger.WarnContext(context.Background(), "claude_shared_owner_retirement_blocked", "account_id", last.lease.account, "owner_lease_id", owner.lease.response.LeaseId)
			}
			return incomplete(domain.Fail(domain.ServerUnavailable, "A member Finish is still unacknowledged.", "Retry once the DeliDev server is reachable."))
		}
		contained := joined && !owner.uncertain
		g.mu.Lock()
		var retained []*claudeSharedMember
		for _, m := range owner.members {
			if m.state == claudeMemberRetained {
				retained = append(retained, m)
			}
		}
		g.mu.Unlock()
		for _, m := range retained {
			bounded, stop := context.WithTimeout(context.Background(), 30*time.Second)
			err := settleClaudeMemberProcesses(bounded, config, m.lease.leaseID(), m.job, &m.lease.journal)
			stop()
			if err != nil {
				contained = false
				if config.Logger != nil {
					config.Logger.WarnContext(context.Background(), "claude_shared_member_closure_unproven", "account_id", last.lease.account, "owner_lease_id", owner.lease.response.LeaseId, "lease_id", m.lease.response.LeaseId, "operation_id", m.job, "code", domain.SafeError(err).Code)
				}
			}
		}
		bounded, stop := context.WithTimeout(context.Background(), 30*time.Second)
		var identity *pb.NativeSubscriptionIdentity
		if contained {
			var err error
			if identity, err = owner.profile.identity(bounded, config); err != nil {
				contained, identity = false, nil
			}
		}
		stop()
		// Keep the server owner until the original local lock is released. A queued
		// logout can start as soon as the owner Finish commits and must not race the
		// closing profile.
		if err := owner.profile.close(); err != nil {
			contained, identity = false, nil
		}
		g.mu.Lock()
		owner.closed, owner.identity, owner.contained, owner.retained = true, identity, contained, len(retained)
		g.mu.Unlock()
	}
	err := withFreshClient(config, last.lease, func() error {
		return last.lease.finishClaudeScopedRetry(owner.identity, owner.contained, owner.contained, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER, owner.lease.response.LeaseRevision)
	})
	if err != nil {
		if claudeFinishOutcomeUnknown(err) {
			// The server may have committed the retirement. The profile is already
			// closed, so no member may join this owner again: resume the same
			// idempotent Finish before the next join.
			return incomplete(err)
		}
		return err
	}
	g.mu.Lock()
	g.owner = nil
	owner.resume = nil
	g.mu.Unlock()
	if config.Logger != nil {
		config.Logger.InfoContext(context.Background(), "claude_shared_owner_retired", "account_id", last.lease.account, "owner_lease_id", owner.lease.response.LeaseId, "retained_members", owner.retained, "contained", owner.contained)
	}
	return nil
}

// acquireLegacyHistory serializes executions whose retained history is in the
// canonical profile directory. The returned release must be called after the
// execution has joined its native process.
func (g *claudeAccountGate) acquireLegacyHistory(ctx context.Context) (func(), error) {
	select {
	case g.legacy <- struct{}{}:
		return func() { <-g.legacy }, nil
	case <-ctx.Done():
		return nil, domain.SafeError(ctx.Err())
	}
}

// claudeMemberExecutionClaimed reports whether a member's durable journal
// records that its native execution may have launched processes. The claim is
// written before the first launch, so a journal that says nothing started proves
// no process owner exists. A journal that cannot be read, or that names another
// lease or operation, proves nothing: the member is treated as started. The live
// journal, when the caller holds one, can only add to the claim.
func claudeMemberExecutionClaimed(config Config, leaseID, operation domain.ID, live *managedSubscriptionJournal) bool {
	if live != nil && live.ExecutionStarted {
		return true
	}
	raw, err := security.ReadPrivate(filepath.Join(config.Root, "managed-auth", string(leaseID)+".json"), 16<<10)
	var journal managedSubscriptionJournal
	if err != nil || domain.Decode(raw, &journal) != nil || journal.Lease != leaseID || journal.Operation != operation {
		return true
	}
	return journal.ExecutionStarted
}

// settleClaudeMemberProcesses joins one member's process owner. A missing owner
// directory is a never-started proof only for a member whose durable claim says
// no execution started; for a started member it is recovery uncertainty, because
// the descendants the directory indexed may still run. Joining a missing
// directory fails closed with the process package's ownership error.
func settleClaudeMemberProcesses(ctx context.Context, config Config, leaseID, operation domain.ID, live *managedSubscriptionJournal) error {
	root := filepath.Join(config.Root, "processes")
	if _, err := os.Lstat(filepath.Join(root, string(operation))); errors.Is(err, os.ErrNotExist) && !claudeMemberExecutionClaimed(config, leaseID, operation, live) {
		return nil
	}
	return process.ReconcileOwnerContext(ctx, root, operation)
}

// reconcileSharedClaudeMembers joins every process owner of a lost shared owner's
// members. A member whose durable claim says it never started a process has no
// owner directory; a started member must still have one.
func reconcileSharedClaudeMembers(ctx context.Context, config Config, members []domain.SubscriptionMember) error {
	for _, member := range members {
		if err := settleClaudeMemberProcesses(ctx, config, member.LeaseID, member.OperationID, nil); err != nil {
			return err
		}
	}
	return nil
}

// settleSharedClaudeMemberJournals marks every member's local claim reported
// after the whole owner/member set was cleaned by the owner Finish. It is a
// best-effort bookkeeping step: the server owner is already released.
func settleSharedClaudeMemberJournals(config Config, members []domain.SubscriptionMember) {
	for _, member := range members {
		path := filepath.Join(config.Root, "managed-auth", string(member.LeaseID)+".json")
		raw, err := security.ReadPrivate(path, 16<<10)
		var journal managedSubscriptionJournal
		if err != nil || domain.Decode(raw, &journal) != nil || journal.Lease != member.LeaseID {
			continue
		}
		journal.State, journal.Cleanup, journal.Success = managedReported, true, false
		if err := writeJSON(path, journal); err != nil && config.Logger != nil {
			config.Logger.Warn("claude_shared_member_journal_unsettled", "lease_id", member.LeaseID)
		}
	}
}
