// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// Terminal original server LOGOUT cleanup recovery.
//
// A server LOGOUT publishes its disconnected metadata (no connection, no
// generation, released identity) before it removes the account's protected
// credential references. When that removal fails, markServerSubscriptionRecovery
// retains the account with RecoveryRequired and an operation fence. The shape
// checked below is the only durable evidence of that outcome: a native LOGOUT
// that failed or was interrupted keeps its connection, generation and
// NativeStarted flag, and a LOGIN never records an operation generation without
// a credential generation. RecoveryRequired alone never grants cleanup
// authority; the original process/profile ownership is proven separately and
// the proof is persisted in the private cleanup child before any vault work.

// serverLogoutCleanupPhase is the closed set of phases used by structured logs.
type serverLogoutCleanupPhase string

const (
	serverLogoutCleanupPhaseBegin       serverLogoutCleanupPhase = "begin"
	serverLogoutCleanupPhaseNative      serverLogoutCleanupPhase = "native"
	serverLogoutCleanupPhaseCredentials serverLogoutCleanupPhase = "credentials"
	serverLogoutCleanupPhaseMetadata    serverLogoutCleanupPhase = "metadata"
)

// serverLogoutCleanupResult is the closed result vocabulary used by logs.
type serverLogoutCleanupResult string

const (
	serverLogoutCleanupConfirmed serverLogoutCleanupResult = "confirmed"
	serverLogoutCleanupRetained  serverLogoutCleanupResult = "retained"
)

func validOriginalOperationActor(actor domain.Principal) bool {
	switch actor.Type {
	case domain.OwnerDevice:
		return actor.DeviceID == "" && actor.MachineID == ""
	case domain.ClientDevice:
		return actor.DeviceID.Validate() == nil && actor.MachineID == ""
	default:
		return false
	}
}

// serverLogoutRecoveryOwner reports whether the account is exactly a terminal
// original server LOGOUT whose only remaining work is protected cleanup. A Grok
// account qualifies exactly like a ChatGPT account: its server-owned logout
// publishes the disconnected metadata only after the revocation was confirmed,
// and a failed vault removal leaves this shape.
func serverLogoutRecoveryOwner(a domain.Account, operation domain.ID) bool {
	st := a.Subscription
	if a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionChatGPT && a.SubscriptionService != domain.SubscriptionGrok || a.Health != domain.AccountFailed || a.Connection != nil || a.Removal != nil || st == nil {
		return false
	}
	if st.Generation != "" || st.IdentityCommitment != "" || st.OwnerMachineID != "" || st.NativeProfileID != "" || st.NativeOperation != nil || st.Lease != nil || st.ResetCredits != nil || !st.RecoveryRequired {
		return false
	}
	// Independent quota, reset-credit or observation work keeps its own fences.
	if st.ServerObservationActive() || st.Observation != nil && st.Observation.Active() {
		return false
	}
	o := st.ServerOperation
	if o == nil || o.ID != operation || o.Action != domain.SubscriptionLogout || o.NativeStarted || o.CleanupPhase != "" || o.Generation == "" || o.State != domain.SubscriptionRecovery && o.State != domain.SubscriptionSucceeded {
		return false
	}
	p := st.Pending
	if p == nil || p.ID != o.ID || p.Action != o.Action || p.Actor != o.Actor || p.MachineID != "" || p.Phase != domain.SubscriptionQueued || p.Canceled {
		return false
	}
	return st.Validate(a) == nil
}

func serverLogoutRecoveryCandidate(a domain.Account) bool {
	st := a.Subscription
	return st != nil && st.ServerOperation != nil && serverLogoutRecoveryOwner(a, st.ServerOperation.ID)
}

func frozenServerLogoutOperation(o *domain.ServerSubscriptionOperation, in failedCleanupAccount) bool {
	return o != nil && o.ID == in.OperationID && o.Action == domain.SubscriptionLogout && o.Generation == in.Generation && o.FinishID == in.FinishID && in.OriginalActor != nil && o.Actor == *in.OriginalActor
}

// The retained child binds the original operation, generation, finish identity
// and actor. A replacement connection or later operation can never match.
func serverLogoutRecoveryMatches(a domain.Account, in failedCleanupAccount) bool {
	return serverLogoutRecoveryOwner(a, in.OperationID) && frozenServerLogoutOperation(a.Subscription.ServerOperation, in)
}

// After the credential checkpoint the matching recovery state is cleared and
// the account is an ordinary disconnected record that deletion may consume.
func serverLogoutRecoveryCleared(a domain.Account, in failedCleanupAccount) bool {
	st := a.Subscription
	return st != nil && frozenServerLogoutOperation(st.ServerOperation, in) && st.ServerOperation.State == domain.SubscriptionSucceeded && !st.ServerOperation.NativeStarted && disconnectedSubscription(a)
}

// serverLogoutCleanupChild binds the cleanup to the account revision it compares
// against (revision) and to the revision the public deletion request named
// (deleteRevision). They differ when the logout of a deletion advanced the
// account: the request digest keeps the revision the caller sent, so a retry of
// the same request still replays instead of conflicting with its own logout.
func serverLogoutCleanupChild(account domain.Account, id domain.ID, revision, deleteRevision uint64, request domain.ID) failedCleanupAccount {
	o := account.Subscription.ServerOperation
	actor := o.Actor
	return failedCleanupAccount{Version: 3, Target: subscriptionCleanupServerLogout, AccountID: id, Revision: revision, DeleteRevision: deleteRevision, OperationID: o.ID, DeleteRequestID: request, Alias: account.Alias, Generation: o.Generation, FinishID: o.FinishID, OriginalActor: &actor}
}

// updateCleanupCheckpoint advances only the private child. It is the single
// durable write between external effects, so a restart observes either the
// complete previous phase or the complete next phase.
func (s *Service) updateCleanupCheckpoint(ctx context.Context, name string, parent, child domain.ID, change func(*failedCleanupCheckpoint) error) (failedCleanupCheckpoint, error) {
	var updated failedCleanupCheckpoint
	_, err := s.Store.Mutate(ctx, domain.NewID(), name, child, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.JobKind, child)
		if err != nil {
			return nil, err
		}
		j, _, current, err := decodeFailedCleanupAccount(r, parent)
		if err != nil || j.State.Terminal() {
			return nil, failedCleanupUnavailable()
		}
		if err := change(&current); err != nil {
			return nil, err
		}
		j.Output, _ = json.Marshal(current)
		if _, err := tx.PutJob(child, r.Revision, "", "", j); err != nil {
			return nil, err
		}
		updated = current
		return struct{}{}, nil
	})
	return updated, err
}

// Called under accountGate. The original native runner has already joined
// before the retained LOGOUT metadata was published, so this never replays
// logout, login or callbacks. It proves the original ownership journals, fences
// the vault attempt, removes only this account's AccountLogin references through
// the existing vault and OS policy, then clears the matching recovery metadata.
// An uncertain or failed external attempt is never repeated by the same child.
func (s *Service) cleanupServerLogoutLocked(ctx context.Context, parent, child domain.ID, in failedCleanupAccount, out failedCleanupCheckpoint, account domain.Account) (failedCleanupCheckpoint, error) {
	logger := s.logger.With("operation_id", in.OperationID, "cleanup_job_id", parent, "correlation_id", in.DeleteRequestID, "target", "server-logout")
	retain := func(phase serverLogoutCleanupPhase, err error) {
		logger.WarnContext(ctx, "server_logout_cleanup_step", "phase", phase, "result", serverLogoutCleanupRetained, "code", domain.SafeError(err).Code)
	}
	var err error
	if !out.Started {
		if out, err = s.updateCleanupCheckpoint(ctx, "subscription.cleanup.logout.begin", parent, child, func(c *failedCleanupCheckpoint) error {
			c.Started = true
			return nil
		}); err != nil {
			retain(serverLogoutCleanupPhaseBegin, err)
			return out, err
		}
	}
	if !out.NativeConfirmed {
		original := *account.Subscription.ServerOperation
		// Historical direct-OAuth Grok logouts had no native profile. Current
		// server CLI logouts must prove their original process journals too.
		native := account.SubscriptionService != domain.SubscriptionGrok
		if !native {
			scope, scopeErr := s.nativeScope(in.AccountID)
			if scopeErr != nil {
				return out, scopeErr
			}
			_, exists, lookupErr := scope.Profiles.Lookup(ctx, nativeprofile.Key{Harness: domain.GrokBuild, Purpose: nativeprofile.PurposeAuth, Account: in.AccountID})
			if lookupErr != nil {
				return out, lookupErr
			}
			native = exists
		}
		original.NativeStarted = true
		if native {
			if err := reconcileFailedServerLoginRuntime(ctx, s.scopeFor(in.AccountID), original); err != nil {
				retain(serverLogoutCleanupPhaseNative, err)
				return out, err
			}
		}
		if out, err = s.updateCleanupCheckpoint(ctx, "subscription.cleanup.logout.native", parent, child, func(c *failedCleanupCheckpoint) error {
			c.NativeConfirmed = true
			return nil
		}); err != nil {
			retain(serverLogoutCleanupPhaseNative, err)
			return out, err
		}
		logger.InfoContext(ctx, "server_logout_cleanup_step", "phase", serverLogoutCleanupPhaseNative, "result", serverLogoutCleanupConfirmed)
	}
	// Fence the vault attempt before external effects. An uncertain attempt is
	// retained even if recording its outcome later fails.
	if out, err = s.updateCleanupCheckpoint(ctx, "subscription.cleanup.logout.credentials.begin", parent, child, func(c *failedCleanupCheckpoint) error {
		if !c.Started || !c.NativeConfirmed || c.CredentialsStarted {
			return failedCleanupUnavailable()
		}
		c.CredentialsStarted = true
		return nil
	}); err != nil {
		retain(serverLogoutCleanupPhaseCredentials, err)
		return out, err
	}
	// Explicit logout owns the complete removal of the retained credential files
	// and of every AccountLogin generation, without the Worker Finish limit.
	if err := s.removeAccountCredentials(ctx, in.AccountID, in.OperationID, true); err != nil {
		retain(serverLogoutCleanupPhaseCredentials, err)
		if account.SubscriptionService == domain.SubscriptionGrok {
			logger.WarnContext(ctx, "grok_logout_cleanup_pending", "phase", grokCleanupPhase, "code", domain.SafeError(err).Code)
		}
		return out, err
	}
	if account.SubscriptionService == domain.SubscriptionGrok {
		logger.InfoContext(ctx, "grok_logout_cleanup_confirmed", "phase", grokCleanupPhase)
	}
	_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.cleanup.logout.credentials", child, func(tx *store.Tx) (any, error) {
		r, current, err := serverLoginAccount(tx, in.AccountID, 0)
		if err != nil {
			return nil, err
		}
		if r.Revision != out.Revision || !serverLogoutRecoveryMatches(current, in) {
			return nil, subscriptionDenied()
		}
		st := current.Subscription
		st.ServerOperation.State = domain.SubscriptionSucceeded
		st.ServerOperation.Diagnostic = nil
		// A Grok recovery carries its cleanup diagnostic in the Grok field, which
		// a settled operation must not keep.
		st.ServerOperation.GrokDiagnostic = nil
		st.Pending = nil
		st.RecoveryRequired = false
		current.Health = domain.AccountDisconnected
		current.Validation, current.Catalog, current.Quota = nil, nil, nil
		current.ConfirmedExhausted = false
		updated, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", current)
		if err != nil {
			return nil, err
		}
		jr, err := tx.Get(domain.JobKind, child)
		if err != nil {
			return nil, err
		}
		j, _, checkpoint, err := decodeFailedCleanupAccount(jr, parent)
		if err != nil || j.State.Terminal() || checkpoint.Revision != r.Revision || !checkpoint.CredentialsStarted {
			return nil, failedCleanupUnavailable()
		}
		checkpoint.Revision, checkpoint.CredentialsConfirmed = updated.Revision, true
		j.Output, _ = json.Marshal(checkpoint)
		if _, err := tx.PutJob(child, jr.Revision, "", "", j); err != nil {
			return nil, err
		}
		out = checkpoint
		return struct{}{}, nil
	})
	if err != nil {
		retain(serverLogoutCleanupPhaseMetadata, err)
		return out, err
	}
	delete(s.subscriptionProgress, in.OperationID)
	logger.InfoContext(ctx, "server_logout_cleanup_step", "phase", serverLogoutCleanupPhaseCredentials, "result", serverLogoutCleanupConfirmed)
	return out, nil
}
