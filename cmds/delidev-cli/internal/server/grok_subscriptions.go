// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// grokIdentityCommitment derives the identity commitment persisted on a Grok
// account's SubscriptionState. The bundle's unkeyed identity key is guessable
// from a user and principal ID, so the stored value is a server-keyed,
// domain-separated HMAC over it, matching the Codex commitment strategy. The
// result is 64 hex characters, accepted by the existing commitment validation
// and by uniqueSubscriptionIdentity. It returns "" for a bundle with no identity.
func (s *Service) grokIdentityCommitment(bundle subscription.GrokBundle) string {
	key := bundle.IdentityKey()
	if key == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(s.Identity.Token))
	mac.Write([]byte("delidev/subscription/grok-identity/v1\x00" + string(s.Identity.ServerID) + "\x00" + key))
	return hex.EncodeToString(mac.Sum(nil))
}

// Grok Take/Finish carries the lifecycle (login, refresh, logout) of a
// service-native Grok account. The credential moves only as the sealed
// subscription.GrokBundle envelope under credentials.AccountLogin, bound to this
// server, the account and one credential generation. Execution is a separate
// contract: a Grok execute Take is refused here, and the Codex lifecycle keeps
// refusing every Grok account through subscriptionAccount.

// grokLifecycleActions are the only actions a Grok lease can carry today.
func grokLifecycleAction(action domain.SubscriptionAction) bool {
	return action == domain.SubscriptionLogin || action == domain.SubscriptionRefresh || action == domain.SubscriptionLogout
}

func grokUnsupported() *domain.Error {
	return domain.Fail(domain.Unsupported, "This Grok operation is not available.", "Use login, refresh or logout; Grok execution is not activated.")
}

func (s *Service) isGrokSubscription(ctx context.Context, id string) (bool, error) {
	if domain.ID(id).Validate() != nil {
		return false, subscriptionDenied()
	}
	var matched bool
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := accountFromTx(tx, domain.ID(id), 0)
		matched = err == nil && a.Type == domain.SubscriptionAccount && a.SubscriptionService == domain.SubscriptionGrok
		return err
	})
	return matched, err
}

func grokSubscriptionAccount(tx *store.Tx, id domain.ID, revision uint64) (store.Record, domain.Account, error) {
	r, a, err := accountFromTx(tx, id, revision)
	if err != nil {
		return r, a, err
	}
	if a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionGrok || a.ProviderID != "" {
		return r, a, domain.Fail(domain.Unsupported, "This operation requires a Grok subscription account.", "Select a Grok service account for the Grok lifecycle.")
	}
	return r, a, nil
}

// grokSubscriptionInstallation requires exactly one verified Grok Build
// installation on the selected Runner Device whose version is at or above the
// subscription baseline. The legacy discovery baseline is deliberately not used:
// it admits every release from 1.0.41, while a managed credential may be
// delivered only to an installation that meets the subscription profile. The managed-Grok Worker capability
// (Worker 21) is reserved but not yet declared, so admission is bound to the
// verified installation until that capability activates.
func grokSubscriptionInstallation(tx *store.Tx, machine domain.ID) (domain.Installation, error) {
	_, m, err := activeMachine(tx, machine)
	if err != nil {
		return domain.Installation{}, err
	}
	var found *domain.Installation
	for i := range m.Installations {
		if m.Installations[i].Harness == domain.GrokBuild {
			if found != nil {
				return domain.Installation{}, subscriptionDenied()
			}
			found = &m.Installations[i]
		}
	}
	if found == nil || !domain.GrokSubscriptionVersionAllowed(found.Version) || found.State != domain.InstallationDetected || !found.ProtocolVerified || found.Protocol == nil || found.Protocol.Protocol != domain.ProtocolFor(domain.GrokBuild) || found.Protocol.State != domain.ProtocolVerified || found.Protocol.Problem != nil || found.Problem != nil || found.ResolvedPath == "" || found.ObservedAt == nil || found.ObservedAt.IsZero() || found.ObservedAt.After(time.Now().UTC().Add(time.Second)) {
		return domain.Installation{}, domain.Fail(domain.Unsupported, "Grok subscription authentication requires a verified, supported Grok Build installation.", "Discover the installed Grok Build on the selected Runner Device and install the pinned supported release yourself.")
	}
	return *found, nil
}

// grokSubscriptionLeaseView resolves the exact singleton Grok lease for the
// authenticated Worker. Every owner binding (Runner, device, instance, epoch)
// is checked against the stored lease. Installation admission belongs to Take
// alone: harness discovery may replace the Runner Device's installation list at
// any time, and the Worker may already have joined the original native process
// and removed its local credential, so a claimed lease is never re-gated on
// mutable installation metadata and the original owner can always report Finish.
func (s *Service) grokSubscriptionLeaseView(ctx context.Context, tx *store.Tx, id, lease, machine, instance domain.ID) (store.Record, domain.Account, domain.SubscriptionLease, error) {
	r, a, err := grokSubscriptionAccount(tx, id, 0)
	if err != nil {
		return r, a, domain.SubscriptionLease{}, err
	}
	st := a.Subscription
	actor, _ := domain.PrincipalFrom(ctx)
	if st == nil || st.Lease == nil || st.Lease.ID != lease || st.Lease.Shared || len(st.Members) != 0 {
		return r, a, domain.SubscriptionLease{}, subscriptionDenied()
	}
	l := st.Lease
	if actor.Type != domain.WorkerDevice || actor.DeviceID != l.DeviceID || l.MachineID != machine || l.InstanceID != instance || l.Epoch != s.subscriptionServerEpoch() {
		return r, a, domain.SubscriptionLease{}, subscriptionDenied()
	}
	if err := currentInstance(tx, machine, instance); err != nil {
		return r, a, *l, err
	}
	return r, a, *l, nil
}

func (s *Service) takeGrokSubscription(ctx context.Context, req *connect.Request[pb.TakeSubscriptionRequest]) (response *connect.Response[pb.TakeSubscriptionResponse], returned error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	action := subscriptionAction(req.Msg.Action)
	input := struct {
		Account, Lease, Machine, Instance, Operation domain.ID
		Revision                                     uint64
		Action                                       domain.SubscriptionAction
		Actor                                        domain.Principal
	}{domain.ID(m.Id), domain.ID(m.RequestId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.OperationId), m.ExpectedRevision, action, actor}
	if input.Operation.Validate() != nil || action == "" {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	if !grokLifecycleAction(action) {
		s.logger.WarnContext(ctx, "grok_subscription_take_unsupported_action", "account_id", input.Account, "action", action)
		return nil, rpc.Error(grokUnsupported(), c)
	}
	owned := false
	// admitted is the installation verified in the claiming transaction. It is
	// the only installation observation this lease ever uses.
	var admitted domain.Installation
	defer func() {
		if returned != nil && owned {
			if err := s.markSubscriptionRecovery(input.Account); err != nil {
				s.logger.Warn("grok_subscription_recovery_unconfirmed", "account_id", input.Account, "operation_id", input.Operation, "code", domain.SafeError(err).Code)
			}
		}
	}()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, err := s.Store.Mutate(ctx, input.Lease, "grok.subscription.take", input, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, input.Account, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.RecoveryRequired {
			return nil, subscriptionDenied()
		}
		if input.Revision > r.Revision {
			return nil, domain.Fail(domain.Conflict, "The account revision has not been observed.", "Retain the original queued operation and its observed revision.")
		}
		if err := currentInstance(tx, input.Machine, input.Instance); err != nil {
			return nil, err
		}
		installation, err := grokSubscriptionInstallation(tx, input.Machine)
		if err != nil {
			return nil, err
		}
		if err := tx.WorkerUpdateAdmission(input.Machine); err != nil {
			return nil, err
		}
		op := st.Pending
		// A missing or replaced original is classified before any field of the
		// current Pending operation, which belongs to someone else's claim.
		if op == nil || op.ID != input.Operation {
			return nil, subscriptionTakeNotAdmitted()
		}
		original := op.Action == action && op.MachineID == input.Machine && op.Phase == domain.SubscriptionQueued
		if original && op.Canceled {
			return nil, subscriptionTakeNotAdmitted()
		}
		if st.Lease != nil {
			return nil, domain.Fail(domain.ResourceExhausted, "The selected account is exclusively leased.", "Wait for its original cleanup; never switch accounts automatically.")
		}
		if !original {
			return nil, subscriptionDenied()
		}
		// Lifecycle shape: login starts from no credential, refresh and logout
		// require the connected account's current generation.
		if action == domain.SubscriptionLogin && (a.Connection != nil || st.Generation != "") || action != domain.SubscriptionLogin && (a.Connection == nil || st.Generation == "") {
			return nil, subscriptionDenied()
		}
		if err := subscriptionActorValid(tx, op.Actor); err != nil {
			return nil, err
		}
		op.Phase = domain.SubscriptionClaimed
		st.Lease = &domain.SubscriptionLease{ID: input.Lease, OperationID: input.Operation, Revision: r.Revision + 1, Action: action, MachineID: input.Machine, InstanceID: input.Instance, DeviceID: actor.DeviceID, Epoch: s.subscriptionServerEpoch(), Generation: st.Generation, StartedAt: time.Now().UTC()}
		admitted = installation
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	// A receipt never redistributes a secret; an uncertain first delivery keeps
	// the lease and requires recovery instead of a second native owner. The first
	// response may have been lost after the lease committed, in which case the
	// Worker lacks the generation and lease revision it needs to Finish, so the
	// exact unfinished lease is fenced for recovery before the denial.
	if result.Replayed {
		if err := s.fenceReplayedGrokTake(ctx, input.Account, input.Lease, input.Operation, input.Machine, input.Instance, action, actor); err != nil {
			s.logger.WarnContext(ctx, "grok_subscription_replay_fence_unconfirmed", "account_id", input.Account, "lease_id", input.Lease, "operation_id", input.Operation, "code", domain.SafeError(err).Code)
		}
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	owned = true
	var generation domain.ID
	var leaseRevision uint64
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, _, view, err := s.grokSubscriptionLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance)
		if err == nil {
			generation, leaseRevision = view.Generation, view.Revision
		}
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	var bundle []byte
	if generation != "" {
		vault, err := s.secrets()
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		bundle, err = vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: generation, Purpose: credentials.AccountLogin})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		// Only this server's exact, unrebound envelope may cross the boundary.
		opened, err := subscription.OpenGrokBundle(bundle, s.Identity.ServerID, input.Account, generation)
		if err != nil {
			clear(bundle)
			s.logger.WarnContext(ctx, "grok_subscription_bundle_invalid", "account_id", input.Account, "lease_id", input.Lease, "operation_id", input.Operation)
			return nil, rpc.Error(err, c)
		}
		opened.Clear()
	}
	// Authorization after vault I/O prevents revocation or cancellation from
	// winning during Get.
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		_, a, _, err := s.grokSubscriptionLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance)
		if err != nil || a.Subscription.RecoveryRequired {
			return subscriptionDenied()
		}
		op := a.Subscription.Pending
		if op == nil || op.Canceled || subscriptionActorValid(tx, op.Actor) != nil {
			return subscriptionDenied()
		}
		return nil
	})
	if err != nil {
		clear(bundle)
		return nil, rpc.Error(err, c)
	}
	installationJSON, _ := json.Marshal(admitted)
	s.logger.InfoContext(ctx, "grok_subscription_lease_granted", "account_id", input.Account, "lease_id", input.Lease, "operation_id", input.Operation, "action", action, "bundle_delivered", len(bundle) != 0)
	return connect.NewResponse(&pb.TakeSubscriptionResponse{LeaseId: string(input.Lease), GenerationId: string(generation), Bundle: bundle, LeaseRevision: leaseRevision, InstallationJson: installationJSON}), nil
}

// fenceReplayedGrokTake marks the still-held original Grok lease
// recovery-required so the unfinished lease is never silently stranded. The
// caller holds the account gate. A lease that was already finished, replaced, or
// held by another operation, device or instance is left untouched, and the
// credential is never redistributed.
func (s *Service) fenceReplayedGrokTake(ctx context.Context, id, lease, operation, machine, instance domain.ID, action domain.SubscriptionAction, actor domain.Principal) error {
	fenced := false
	_, err := s.Store.Mutate(ctx, domain.NewID(), "grok.subscription.take-replay-fence", struct{ Account, Lease domain.ID }{id, lease}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.RecoveryRequired {
			return struct{}{}, nil
		}
		l := st.Lease
		if l == nil || l.ID != lease || l.OperationID != operation || l.Action != action || l.MachineID != machine || l.InstanceID != instance || l.DeviceID != actor.DeviceID {
			return struct{}{}, nil
		}
		st.RecoveryRequired = true
		a.Health = domain.AccountFailed
		fenced = true
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err == nil && fenced {
		s.logger.WarnContext(ctx, "grok_subscription_replayed_take_fenced", "account_id", id, "lease_id", lease, "operation_id", operation)
	}
	return err
}

func (s *Service) finishGrokSubscription(ctx context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (response *connect.Response[pb.FinishSubscriptionResponse], returned error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	owned := false
	defer func() {
		if returned != nil && owned {
			if err := s.markSubscriptionRecovery(domain.ID(m.Id)); err != nil {
				s.logger.Warn("grok_subscription_recovery_unconfirmed", "account_id", m.Id, "code", domain.SafeError(err).Code)
			}
		}
	}()
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	// Grok keeps one exclusive singleton lease; member and owner scopes belong to
	// shared execution owners.
	if req.Msg.FinishScope != pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_UNSPECIFIED {
		return nil, rpc.Error(grokUnsupported(), c)
	}
	// The request owns a copy of the sealed credential; clear it on every path.
	defer clear(req.Msg.Bundle)
	if len(req.Msg.Bundle) > subscription.MaxGrokBundleEnvelope {
		return nil, rpc.Error(subscription.GrokBundleInvalid(), c)
	}
	input := subscriptionFinishInput{domain.ID(m.Id), domain.ID(req.Msg.LeaseId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.GenerationId), s.accountCommitment(domain.ID(m.RequestId), req.Msg.Bundle), m.ExpectedRevision, req.Msg.CleanupConfirmed, req.Msg.RefreshConfirmed, req.Msg.Succeeded, domain.SubscriptionFinishLegacy}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, replayed, err := s.Store.Replay(ctx, domain.ID(m.RequestId), "grok.subscription.finish", input)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if !replayed {
		var original domain.Account
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := tx.Authorize(); err != nil {
				return err
			}
			_, original, _, err = s.grokSubscriptionLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance)
			return err
		})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		state := original.Subscription
		lease := state.Lease
		if state.RecoveryRequired || state.Generation != input.Generation || lease.Generation != input.Generation || lease.Revision != input.Revision {
			return nil, rpc.Error(subscriptionDenied(), c)
		}
		owned = true
		usable := input.Success && input.Cleanup && (lease.Action != domain.SubscriptionRefresh || input.Refresh)
		if state.Pending != nil && state.Pending.Canceled {
			usable = false
		}
		vault, err := s.secrets()
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		newGeneration := domain.ID(m.RequestId)
		var identityCommitment string
		if usable && lease.Action != domain.SubscriptionLogout {
			// The envelope is bound to the generation this Finish creates, so a
			// bundle taken for another account, server or generation is refused.
			bundle, err := subscription.OpenGrokBundle(req.Msg.Bundle, s.Identity.ServerID, input.Account, newGeneration)
			if err != nil {
				s.logger.WarnContext(ctx, "grok_subscription_finish_bundle_rejected", "account_id", input.Account, "lease_id", input.Lease, "operation_id", lease.OperationID)
				return nil, rpc.Error(err, c)
			}
			defer bundle.Clear()
			identityCommitment = s.grokIdentityCommitment(bundle)
			if state.IdentityCommitment != "" && identityCommitment != state.IdentityCommitment {
				s.logger.WarnContext(ctx, "grok_subscription_identity_changed", "account_id", input.Account, "lease_id", input.Lease)
				return nil, rpc.Error(subscriptionDenied(), c)
			}
			if lease.Action == domain.SubscriptionLogin {
				err := s.Store.Read(ctx, func(tx *store.Tx) error {
					return uniqueSubscriptionIdentity(tx, input.Account, identityCommitment)
				})
				if err != nil {
					s.logger.WarnContext(ctx, "grok_subscription_identity_duplicate", "account_id", input.Account, "lease_id", input.Lease, "code", domain.SafeError(err).Code)
					return nil, rpc.Error(err, c)
				}
			}
			if lease.Action == domain.SubscriptionRefresh {
				oldRaw, err := vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: state.Generation, Purpose: credentials.AccountLogin})
				if err != nil {
					return nil, rpc.Error(err, c)
				}
				old, err := subscription.OpenGrokBundle(oldRaw, s.Identity.ServerID, input.Account, state.Generation)
				clear(oldRaw)
				if err == nil {
					err = subscription.GrokRefreshed(old, bundle)
				}
				old.Clear()
				if err != nil {
					return nil, rpc.Error(err, c)
				}
			}
			// Only the exact authenticated Worker lease can stage this freshly
			// produced native bundle. No owner/client import endpoint exists.
			if _, err := vault.Put(ctx, credentials.Ref{Owner: input.Account, ID: newGeneration, Purpose: credentials.AccountLogin}, req.Msg.Bundle); err != nil {
				return nil, rpc.Error(err, c)
			}
		}
		// The superseded generation stays referenced and readable until the
		// account document durably publishes its replacement (or the logout), so a
		// failed or retried publication never points at a deleted generation.
		// committed is the one generation the account references after the commit.
		var committed domain.ID
		result, err = s.Store.Mutate(ctx, domain.ID(m.RequestId), "grok.subscription.finish", input, func(tx *store.Tx) (any, error) {
			if err := tx.Authorize(); err != nil {
				return nil, err
			}
			r, a, _, err := s.grokSubscriptionLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance)
			if err != nil {
				return nil, err
			}
			state := a.Subscription
			if state.RecoveryRequired || state.Generation != input.Generation {
				return nil, subscriptionDenied()
			}
			if state.Pending != nil {
				if err := subscriptionActorValid(tx, state.Pending.Actor); err != nil {
					return nil, err
				}
				if state.Pending.Canceled {
					usable = false
				}
			}
			if usable && lease.Action == domain.SubscriptionLogin {
				if err := uniqueSubscriptionIdentity(tx, r.ID, identityCommitment); err != nil {
					return nil, err
				}
			}
			if !input.Cleanup || !usable && lease.Action != domain.SubscriptionLogin {
				// Keep the lease and old generation denied. Neither an error nor a
				// process timeout establishes safe refresh-token redistribution.
				state.RecoveryRequired = true
				a.Health = domain.AccountFailed
			} else {
				state.Lease = nil
				state.Members = nil
				if state.Pending != nil && state.Pending.ID == lease.OperationID {
					state.Pending = nil
				}
				if usable && lease.Action != domain.SubscriptionLogout {
					state.Generation = newGeneration
					state.IdentityCommitment = identityCommitment
					state.OwnerMachineID = lease.MachineID
					if a.Connection == nil {
						a.Connection = &domain.AccountConnection{ID: newGeneration, Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}
					}
					if state.Pending == nil && a.Health != domain.AccountRevoked {
						a.Health = domain.AccountReady
					}
				} else if lease.Action == domain.SubscriptionLogout {
					a.Connection = nil
					a.Health = domain.AccountDisconnected
					a.Validation = nil
					a.Catalog = nil
					a.Quota = nil
					a.ConfirmedExhausted = false
					state.Generation = ""
					state.IdentityCommitment = ""
				}
			}
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
			committed = state.Generation
			return accountReceipt{ID: r.ID}, nil
		})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		if original.Subscription.Pending != nil {
			delete(s.subscriptionProgress, original.Subscription.Pending.ID)
		}
		// Only after the commit are the unreferenced generations removed: the
		// superseded one after a publication, the staged new one after a
		// cancellation that won during vault staging, and every generation after a
		// completed logout. A logout fenced for recovery still references its
		// generation, so that generation is kept. A failed removal is fenced for
		// recovery by the owned-error guard.
		if input.Cleanup {
			if err := cleanupSubscriptionReferences(ctx, vault, input.Account, committed, protectedGenerations(original.Subscription)...); err != nil {
				return nil, rpc.Error(err, c)
			}
		}
	}
	owned = false
	r, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		s.logger.WarnContext(ctx, "grok_subscription_completion_presentation_unavailable", "account_id", input.Account, "lease_id", input.Lease, "replayed", replayed, "code", domain.SafeError(err).Code)
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "grok_subscription_operation_finished", "account_id", input.Account, "lease_id", input.Lease, "cleanup_confirmed", input.Cleanup, "replayed", replayed)
	return connect.NewResponse(&pb.FinishSubscriptionResponse{Account: rpc.Resource(r), Replayed: result.Replayed}), nil
}
