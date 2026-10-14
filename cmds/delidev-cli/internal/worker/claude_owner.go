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
	// uncertain records a member Finish whose acknowledgment was lost, so the
	// final owner Finish can never claim a clean containment.
	uncertain bool
}

type claudeSharedMember struct {
	lease    *managedSubscriptionLease
	job      domain.ID
	state    claudeMemberState
	closeRPC func()
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
		_ = lease.finishClaudeScoped(nil, false, false, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER, lease.response.LeaseRevision)
		member.close()
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
	if owner == nil || owner.lease.response.LeaseId != member.lease.response.OwnerLeaseId || owner.uncertain {
		g.mu.Unlock()
		// The server and this Worker disagree about the owner. The member has
		// started nothing, so release it alone and report uncertainty.
		_ = member.lease.finishClaudeScoped(nil, true, false, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, member.lease.response.LeaseRevision)
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
		err := member.lease.finishClaudeScoped(nil, joined, joined, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, revision)
		if err != nil {
			g.mu.Lock()
			owner.uncertain = true
			g.mu.Unlock()
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

// retire runs under the admission gate, so no new member can join while the
// owner closes.
func (g *claudeAccountGate) retire(owner *claudeSharedOwner, last *claudeSharedMember, joined bool) error {
	config := owner.config
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
	defer stop()
	var identity *pb.NativeSubscriptionIdentity
	if contained {
		var err error
		if identity, err = owner.profile.identity(bounded, config); err != nil {
			contained, identity = false, nil
		}
	}
	// Keep the server owner until the original local lock is released. A queued
	// logout can start as soon as the owner Finish commits and must not race the
	// closing profile.
	if err := owner.profile.close(); err != nil {
		contained, identity = false, nil
	}
	err := last.lease.finishClaudeScoped(identity, contained, contained, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER, owner.lease.response.LeaseRevision)
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.owner = nil
	g.mu.Unlock()
	if config.Logger != nil {
		config.Logger.InfoContext(context.Background(), "claude_shared_owner_retired", "account_id", last.lease.account, "owner_lease_id", owner.lease.response.LeaseId, "retained_members", len(retained), "contained", contained)
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
