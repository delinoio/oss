// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type codexOwnerRegistry struct {
	mu       sync.Mutex
	accounts map[domain.ID]*codexAccountGate
}

func newCodexOwnerRegistry() *codexOwnerRegistry {
	return &codexOwnerRegistry{accounts: make(map[domain.ID]*codexAccountGate)}
}

func (r *codexOwnerRegistry) gate(account domain.ID) *codexAccountGate {
	r.mu.Lock()
	defer r.mu.Unlock()
	gate := r.accounts[account]
	if gate == nil {
		gate = &codexAccountGate{slot: make(chan struct{}, 1)}
		r.accounts[account] = gate
	}
	return gate
}

// parkUnverifiedRelease retains a shared account home hold whose release could
// not be verified after its Finish was published. The local owner or
// continuation is gone by then, so without this the next holder of the account
// would meet profile_busy with nothing left that could retry the release.
func (r *codexOwnerRegistry) parkUnverifiedRelease(account domain.ID, hold *sharedHomeHold) {
	if r == nil || hold == nil {
		return
	}
	gate := r.gate(account)
	gate.mu.Lock()
	gate.unverified = hold
	gate.mu.Unlock()
}

// settleUnverifiedRelease verifies a parked release again and forgets it once
// the lock is proven free. It reports the failure while the lock stays
// unverified, which blocks the next holder instead of letting it fail on a lock
// whose owner is gone.
func (r *codexOwnerRegistry) settleUnverifiedRelease(account domain.ID) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	gate := r.accounts[account]
	r.mu.Unlock()
	if gate == nil {
		return nil
	}
	gate.mu.Lock()
	hold := gate.unverified
	gate.mu.Unlock()
	if hold == nil {
		return nil
	}
	if err := hold.release(); err != nil {
		return &sharedHomeReleaseError{err}
	}
	gate.mu.Lock()
	if gate.unverified == hold {
		gate.unverified = nil
	}
	gate.mu.Unlock()
	return nil
}

// sharedHomeReleaseError reports that the shared account home lock could not be
// released and verified. When a member Finish returns it, the Finish itself was
// published and the unverified release is retained for the next holder. The
// closed profile cause wins when the release error carries one; otherwise the
// failure is a generic recovery-required one.
type sharedHomeReleaseError struct{ err error }

var errSharedHomeUnverified = domain.Fail(domain.RecoveryRequired, "The shared Codex account home release could not be verified.", "Inspect the account history home without deleting retained history, then retry.")

func (e *sharedHomeReleaseError) Error() string   { return errSharedHomeUnverified.Error() }
func (e *sharedHomeReleaseError) Unwrap() []error { return []error{e.err, errSharedHomeUnverified} }

type codexAccountGate struct {
	slot      chan struct{}
	mu        sync.Mutex
	owner     *codexSharedOwner
	uncertain bool
	// unverified is a shared account home hold whose release failed after its
	// Finish was published; see parkUnverifiedRelease.
	unverified *sharedHomeHold
}

func (g *codexAccountGate) acquire(ctx context.Context) error {
	select {
	case g.slot <- struct{}{}:
	case <-ctx.Done():
		return domain.SafeError(ctx.Err())
	}
	g.mu.Lock()
	owner := g.owner
	uncertain := g.uncertain
	g.mu.Unlock()
	if uncertain {
		g.release()
		return sharedOwnerUncertain()
	}
	if owner != nil {
		if err := owner.settlePending(); err != nil {
			g.release()
			return err
		}
		if owner.retiring && !owner.closed && owner.last != nil {
			if err := owner.retire(owner.last); err != nil {
				g.release()
				return err
			}
		}
		if owner.uncertain {
			g.release()
			return sharedOwnerUncertain()
		}
	}
	return nil
}

func (g *codexAccountGate) acquireForFinish() {
	// A member must remain queued until its Finish has settled. A timed wait
	// can expire behind another member's bounded Finish retries or owner
	// retirement, leaving this member marked running with nobody to retire it.
	g.slot <- struct{}{}
}

func (g *codexAccountGate) release() { <-g.slot }

func (g *codexAccountGate) fence() {
	g.mu.Lock()
	g.uncertain = true
	g.mu.Unlock()
}

type codexSharedOwner struct {
	gate    *codexAccountGate
	config  Config
	lease   *managedSubscriptionLease
	runtime *codex.SharedRuntime
	home    string
	// profile is the registry lock of the shared account history home. The
	// founding owner holds it until the owner retires and releases it, verified,
	// before the owner Finish is published.
	profile                *sharedHomeHold
	initial                []byte
	latest                 []byte
	unregisterObservations func()
	members                map[domain.ID]*codexSharedMember
	runnable               int
	retiring               bool
	closed                 bool
	clean                  bool
	nativeUncertain        bool
	uncertain              bool
	// pendingOwner retains the last member whose owner Finish outcome may be
	// committed. No new member runs until the same request is acknowledged.
	pendingOwner *codexSharedMember
	last         *codexSharedMember
}

type codexSharedMember struct {
	lease         *managedSubscriptionLease
	job           domain.ID
	workspace     *workspace.ExecutionLease
	closeRPC      func()
	running       bool
	proven        bool
	pendingFinish bool
}

func (m *codexSharedMember) close() {
	if m.closeRPC != nil {
		m.closeRPC()
		m.closeRPC = nil
	}
}

// register is called while the Take admission gate remains held. A founder
// releases that gate only after its app-server has started or its original
// credential has been settled. A joined member receives no credential.
func (g *codexAccountGate) register(config Config, lease *managedSubscriptionLease, closeRPC func(), job domain.ID, workspaceLease *workspace.ExecutionLease) (*codexSharedOwner, *codexSharedMember, bool, error) {
	member := &codexSharedMember{lease: lease, job: job, workspace: workspaceLease, closeRPC: closeRPC, running: true}
	g.mu.Lock()
	defer g.mu.Unlock()
	if lease.response.OwnerLeaseId == "" {
		if g.owner != nil || len(lease.response.Bundle) == 0 {
			return nil, nil, false, &managedExecutionUncertain{subscription.Invalid()}
		}
		owner := &codexSharedOwner{gate: g, config: config, lease: lease, initial: bytes.Clone(lease.response.Bundle), members: map[domain.ID]*codexSharedMember{lease.leaseID(): member}, runnable: 1}
		g.owner = owner
		if config.Logger != nil {
			config.Logger.Info("codex_shared_owner_founded", "account_id", lease.account, "owner_lease_id", lease.leaseID(), "operation_id", job)
		}
		return owner, member, true, nil
	}
	owner := g.owner
	if owner == nil || owner.lease.response.LeaseId != lease.response.OwnerLeaseId || owner.runtime == nil || owner.retiring || owner.uncertain || len(lease.response.Bundle) != 0 {
		return nil, nil, false, &managedExecutionUncertain{subscription.Invalid()}
	}
	owner.members[lease.leaseID()] = member
	owner.runnable++
	if config.Logger != nil {
		config.Logger.Info("codex_shared_member_joined", "account_id", lease.account, "owner_lease_id", owner.lease.leaseID(), "lease_id", lease.leaseID(), "operation_id", job)
	}
	return owner, member, false, nil
}

func (o *codexSharedOwner) ready(runtime *codex.SharedRuntime, home string) error {
	o.gate.mu.Lock()
	defer o.gate.mu.Unlock()
	if o.gate.owner != o || o.runtime != nil || o.retiring || runtime == nil || home == "" {
		return sharedOwnerUncertain()
	}
	o.runtime, o.home = runtime, home
	if o.config.Logger != nil {
		o.config.Logger.Info("codex_shared_owner_native_ready", "account_id", o.lease.account, "owner_lease_id", o.lease.leaseID())
	}
	return nil
}

func sharedOwnerUncertain() error {
	return &managedExecutionUncertain{domain.Fail(domain.RecoveryRequired, "The shared Codex account owner is unconfirmed.", "Fence its members and reconcile the original native process and authentication before another execution.")}
}

// finishMember is called exactly once for every locally registered execution.
// A proven non-final member releases only itself. Every other workspace stays
// attached to the owner until its original process and helpers are contained.
func (g *codexAccountGate) finishMember(owner *codexSharedOwner, member *codexSharedMember, proven bool) error {
	defer member.close()
	g.acquireForFinish()
	defer g.release()
	g.mu.Lock()
	if g.owner != owner || !member.running || owner.runnable < 1 {
		g.mu.Unlock()
		return sharedOwnerUncertain()
	}
	member.running, member.proven = false, proven
	owner.runnable--
	last := owner.runnable == 0
	if last {
		owner.retiring = true
		owner.last = member
	}
	g.mu.Unlock()
	if !last {
		err := member.lease.finishCodexScopedRetry(nil, proven, proven, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, member.lease.response.LeaseRevision)
		if err != nil {
			if codexFinishOutcomeUnknown(err) {
				g.mu.Lock()
				member.pendingFinish = true
				g.mu.Unlock()
			} else {
				g.mu.Lock()
				owner.uncertain = true
				g.mu.Unlock()
			}
			return err
		}
		if proven {
			g.mu.Lock()
			delete(owner.members, member.lease.leaseID())
			g.mu.Unlock()
		}
		if owner.config.Logger != nil {
			owner.config.Logger.Info("codex_shared_member_finished", "account_id", member.lease.account, "owner_lease_id", owner.lease.leaseID(), "lease_id", member.lease.leaseID(), "released", proven)
		}
		return nil
	}
	return owner.retire(member)
}

func (o *codexSharedOwner) settlePending() error {
	o.gate.mu.Lock()
	var pending []*codexSharedMember
	for _, member := range o.members {
		if member.pendingFinish {
			pending = append(pending, member)
		}
	}
	ownerPending := o.pendingOwner
	o.gate.mu.Unlock()
	for _, member := range pending {
		err := withFreshClient(o.config, member.lease, func() error {
			return member.lease.finishCodexScopedRetry(nil, member.proven, member.proven, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, member.lease.response.LeaseRevision)
		})
		if err != nil {
			return err
		}
		o.gate.mu.Lock()
		member.pendingFinish = false
		if member.proven {
			delete(o.members, member.lease.leaseID())
		}
		o.gate.mu.Unlock()
	}
	if ownerPending != nil {
		if err := o.reportOwner(ownerPending); err != nil {
			return err
		}
	}
	return nil
}

func (o *codexSharedOwner) retire(last *codexSharedMember) error {
	if err := o.settlePending(); err != nil {
		return err
	}
	if !o.closed {
		clean := !o.nativeUncertain && !o.uncertain
		nativeJoined := o.runtime == nil
		if o.unregisterObservations != nil {
			o.unregisterObservations()
			o.unregisterObservations = nil
		}
		if o.runtime != nil {
			if err := o.runtime.Health(); err != nil {
				clean = false
			}
			bounded, stop := context.WithTimeout(context.Background(), 5*time.Second)
			bundle, err := o.runtime.ManagedBundle(bounded)
			stop()
			if err != nil {
				clean = false
			} else {
				o.latest = bundle
			}
			nativeErr, helperErr := o.runtime.Close()
			nativeJoined = nativeErr == nil
			if nativeErr != nil || helperErr != nil {
				clean = false
			}
		}
		if nativeJoined && o.runtime != nil {
			bounded, stop := context.WithTimeout(context.Background(), 15*time.Second)
			if err := process.ReconcileOwnerContext(bounded, filepath.Join(o.config.Root, "processes"), o.ownerProcessID()); err != nil {
				clean = false
				nativeJoined = false
			}
			stop()
		}
		if nativeJoined {
			for _, member := range o.members {
				if member.workspace != nil {
					if err := member.workspace.Close(); err != nil {
						clean = false
					}
				}
			}
		} else {
			clean = false
		}
		if o.home != "" && !o.nativeUncertain {
			if o.runtime != nil {
				if nativeJoined && clean {
					if err := o.lease.cleanupAuthentication(o.config.cleanupContext(), o.home, o.latest, o.initial); err != nil {
						clean = false
					}
				} else {
					clean = false
				}
			} else {
				if clean {
					removed, err := cleanupPreNativeAuthentication(o.config.cleanupContext(), o.home, o.initial, false)
					clean = removed && err == nil
				}
			}
		}
		o.clean, o.closed = clean, true
		if o.config.Logger != nil {
			o.config.Logger.Info("codex_shared_owner_contained", "account_id", o.lease.account, "owner_lease_id", o.lease.leaseID(), "cleanup_confirmed", clean)
		}
	}
	return o.reportOwner(last)
}

func (o *codexSharedOwner) reportOwner(last *codexSharedMember) error {
	bundle, success := o.latest, o.runtime != nil && o.clean && len(o.latest) != 0
	if o.runtime == nil && o.clean {
		bundle = o.initial
	}
	return o.finishOwner(func() error {
		return withFreshClient(o.config, last.lease, func() error {
			return last.lease.finishCodexScopedRetry(bundle, o.clean, success, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER, o.lease.response.LeaseRevision)
		})
	}, last)
}

// finishOwner releases the shared account home lock, publishes the owner Finish
// and settles the local owner. The server frees the account lease when it
// commits the owner Finish, which admits an exclusive continuation that is
// already retrying its Take, so the lock is released and verified first and the
// continuation never meets profile_busy. A retry of an uncertain or failed
// Finish finds the lock free, or verifies a failed release again. Outcomes:
//
//   - Finish failed or uncertain: the Finish error is returned and the owner stays
//     tracked, so the same request and the release are retried; an unverified
//     release is retried with it.
//   - Finish published and release verified: the local owner is cleared.
//   - Finish published and release unverified: the local owner is still cleared,
//     because nothing remains to publish, but the unverified hold moves to the
//     account gate and the failure is returned through the member outcome.
//     The next holder verifies the release again before it locks the home.
func (o *codexSharedOwner) finishOwner(publish func() error, last *codexSharedMember) error {
	releaseErr, err := publishAfterRelease(o.profile, publish)
	if releaseErr != nil && o.config.Logger != nil {
		o.config.Logger.Warn("codex_shared_owner_profile_release_failed", "account_id", o.lease.account, "owner_lease_id", o.lease.leaseID(), "finish_published", err == nil)
	}
	if err != nil {
		if codexFinishOutcomeUnknown(err) {
			o.gate.mu.Lock()
			o.pendingOwner = last
			o.gate.mu.Unlock()
		}
		return err
	}
	clear(o.initial)
	clear(o.latest)
	o.gate.mu.Lock()
	if o.gate.owner == o {
		o.gate.owner = nil
	}
	o.pendingOwner = nil
	if releaseErr != nil {
		o.gate.unverified = o.profile
	}
	o.gate.mu.Unlock()
	if o.config.Logger != nil {
		o.config.Logger.Info("codex_shared_owner_retired", "account_id", o.lease.account, "owner_lease_id", o.lease.leaseID(), "cleanup_confirmed", o.clean, "profile_release_verified", releaseErr == nil)
	}
	if releaseErr != nil {
		return &sharedHomeReleaseError{releaseErr}
	}
	return nil
}

// abortFounder is used before a native runtime was installed. An uncertain
// owner Finish remains tracked so the admission gate retries its same request.
func (o *codexSharedOwner) abortFounder(home string, member *codexSharedMember) error {
	o.home = home
	o.gate.mu.Lock()
	o.retiring, o.runnable, member.running = true, 0, false
	o.gate.mu.Unlock()
	return o.retire(member)
}

func (o *codexSharedOwner) accountHome(root string) (string, error) {
	if o == nil {
		return "", sharedOwnerUncertain()
	}
	return codexAccountHistoryHome(root, o.lease.account)
}

func (o *codexSharedOwner) ownerProcessID() domain.ID { return o.lease.leaseID() }
