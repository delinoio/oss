// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// The System 101 lane consumes a reset credit with the sealed generation's
// access token. It never takes the credential writer, so it can coexist with an
// original Execute lease and never changes that lease, native process or its
// write-back. Historical (non access-only) server credits keep the managed lane
// in subscription_server_credit.go.

func sameAccessCredit(o *domain.ServerCreditOperation, original domain.ServerCreditOperation) bool {
	return o != nil && o.AccessOnly && original.AccessOnly && o.ID == original.ID && o.AttemptID == original.AttemptID && o.AttemptEpoch == original.AttemptEpoch && o.FinishID == original.FinishID && o.RequestedAt.Equal(original.RequestedAt) && o.Epoch == original.Epoch && o.Actor == original.Actor && o.Generation == original.Generation && o.ConnectionID == original.ConnectionID && o.CreditID == original.CreditID && o.NextCredit == original.NextCredit && o.CreditsObservationID == original.CreditsObservationID && reflect.DeepEqual(o.Automatic, original.Automatic)
}

// automaticEpisodeAuthority validates the frozen proof of one episode for a
// server-owned operation bound to generation. Before the original lease
// finishes, the exact consent snapshot, lease and generation must still be
// current. Afterwards only the generation produced by that first Finish (which
// also cleared the consent) continues the claim; a later rotation, revocation or
// replacement can never adopt it.
func automaticEpisodeAuthority(st *domain.SubscriptionState, episode *domain.AutomaticResetCreditEpisode, proof domain.ServerAutomaticCreditProof, generation domain.ID) bool {
	if episode == nil || episode.ServerProof == nil || episode.ID != proof.EpisodeID || !reflect.DeepEqual(*episode.ServerProof, proof) || generation != proof.Consent.Generation {
		return false
	}
	if episode.ServerFinishGeneration != "" {
		return st.Generation == episode.ServerFinishGeneration && st.Pending == nil
	}
	c := st.AutomaticCreditConsent
	l, owned := automaticCreditLease(st, proof.LeaseID)
	return c != nil && *c == proof.Consent && st.Generation == generation && owned && l.ID == proof.LeaseID && l.Action == domain.SubscriptionExecute && l.Epoch == proof.Epoch && l.MachineID == proof.MachineID && l.InstanceID == proof.InstanceID && l.DeviceID == proof.DeviceID && l.Revision == proof.LeaseRevision && l.OperationID == proof.OperationID && l.Generation == generation
}

// quotaGenerationBound reports whether a pending access-only quota read is still
// bound to its confirmed generation. A frozen automatic refresh additionally
// survives the original lease's first Finish through its frozen episode proof.
func quotaGenerationBound(st *domain.SubscriptionState, o domain.ServerQuotaOperation) bool {
	if o.AutomaticEpisodeID == "" {
		return st.Generation == o.Generation
	}
	e := st.AutomaticCreditEpisode
	return e != nil && e.ID == o.AutomaticEpisodeID && e.RefreshOperationID == o.ID && e.ServerProof != nil && automaticEpisodeAuthority(st, e, *e.ServerProof, o.Generation)
}

// accessCreditAuthority is evaluated in one account transaction. Before any
// send, the exact manual generation (or the frozen automatic claim) must remain
// bound. After possible send, only the original actor, connection and operation
// remain relevant: explicit same-key reconciliation follows the captured
// generation through Execute rotations and never needs live consent.
func accessCreditAuthority(tx *store.Tx, a domain.Account, original domain.ServerCreditOperation) bool {
	if !quotaAccountReady(a) {
		return false
	}
	st := a.Subscription
	if !sameAccessCredit(st.ServerCredit, original) || a.Connection.ID != original.ConnectionID || st.Pending != nil || st.ServerQuotaActive() || st.Observation != nil && st.Observation.Active() || st.ServerOperation != nil && (st.ServerOperation.Active() || st.ServerOperation.NativeStarted) || st.Lease != nil && st.Lease.Action != domain.SubscriptionExecute || subscriptionActorValid(tx, original.Actor) != nil {
		return false
	}
	if original.EverSent {
		return true
	}
	if original.Automatic != nil {
		episode := st.AutomaticCreditEpisode
		return episode != nil && episode.OperationID == original.ID && automaticEpisodeAuthority(st, episode, *original.Automatic, original.Generation) && subscriptionActorValid(tx, original.Automatic.Consent.Actor) == nil
	}
	return st.Generation == original.Generation
}

// currentCreditInventory rechecks the confirmed selector at the first send.
func currentCreditInventory(a domain.Account, o domain.ServerCreditOperation, now time.Time) bool {
	inventory := a.Subscription.ResetCredits
	if inventory == nil || inventory.Validate() != nil || inventory.ObservationID != o.CreditsObservationID || inventory.ObservedAt.After(now) || now.Sub(inventory.ObservedAt) > 5*time.Minute || inventory.AvailableCount <= 0 {
		return false
	}
	if o.NextCredit {
		return inventory.Credits == nil
	}
	if inventory.Credits == nil {
		return false
	}
	for _, credit := range *inventory.Credits {
		if credit.ID == o.CreditID && credit.ResetType == domain.CodexRateLimitsReset && credit.Status == domain.SubscriptionCreditAvailable && !credit.GrantedAt.After(now) && (credit.ExpiresAt == nil || credit.ExpiresAt.After(now)) {
			return true
		}
	}
	return false
}

// accessCreditSettlePhase settles one joined attempt. A valid provider outcome
// is terminal success; never-sent attempts become terminal failures; possible
// sends keep their uncertainty and original key.
func accessCreditSettlePhase(o *domain.ServerCreditOperation, code domain.Code) {
	if o.Outcome.Valid() {
		o.Phase, o.ErrorCode = domain.SubscriptionObservationSucceeded, ""
	} else if o.EverSent {
		o.Phase, o.ErrorCode = domain.SubscriptionObservationUncertain, domain.RecoveryRequired
	} else {
		o.Phase, o.ErrorCode = domain.SubscriptionObservationFailed, code
	}
}

// Capture the reference under the same short serialization used by Execute
// Finish. Native and network work never runs under this lock.
func (s *Service) readAccessCreditBundle(ctx context.Context, vault accountSecrets, id domain.ID, o domain.ServerCreditOperation) ([]byte, error) {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return err
		}
		if !accessCreditAuthority(tx, a, o) {
			return subscriptionDenied()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return vault.Get(ctx, credentials.Ref{Owner: id, ID: o.Generation, Purpose: credentials.AccountLogin})
}

func (s *Service) runAccessCredit(parent context.Context, id domain.ID) {
	ctx, cancel := context.WithTimeout(domain.WithPrincipal(parent, domain.Principal{Type: domain.OwnerDevice}), time.Minute)
	defer cancel()
	var original domain.ServerCreditOperation
	claimed := false
	_, err := s.Store.Mutate(ctx, domain.NewID(), "subscription.server.credit.claim", id, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if a.Subscription == nil || a.Subscription.ServerCredit == nil {
			return nil, subscriptionDenied()
		}
		o := a.Subscription.ServerCredit
		if !o.AccessOnly || o.Phase != domain.SubscriptionObservationQueued || o.AttemptEpoch != s.subscriptionServerEpoch() {
			return nil, subscriptionDenied()
		}
		original = *o
		o.Phase = domain.SubscriptionObservationSending
		if accessCreditAuthority(tx, a, original) {
			claimed = true
		} else {
			// This rejected claim opened no native owner. It still settles through
			// the checked retirement boundary of its captured reference.
			o.CleanupConfirmed = true
		}
		original = *o
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err != nil {
		return
	}
	if !claimed {
		bounded, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 30*time.Second)
		defer stop()
		s.logger.InfoContext(bounded, "server_credit_claim_rejected", "operation_id", original.ID, "lane", "access_only", "automatic", original.Automatic != nil)
		if err := s.finishAccessCredit(bounded, id, original, "", domain.SubscriptionQuotaObservation{}, domain.Canceled, ""); err != nil {
			s.logger.WarnContext(bounded, "server_credit_rejected_cleanup_retained", "operation_id", original.ID, "code", creditCode(err))
		}
		return
	}
	s.logger.InfoContext(ctx, "server_credit_claimed", "operation_id", original.ID, "lane", "access_only", "automatic", original.Automatic != nil)
	// Binding changes before the durable send claim cancel the original read.
	// After possible send the watcher stops: nothing can usefully cancel it.
	var sent atomic.Bool
	checked := make(chan struct{})
	watchOriginal := original
	go func() {
		defer close(checked)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			if sent.Load() {
				return
			}
			err := s.Store.Read(ctx, func(tx *store.Tx) error {
				_, a, err := subscriptionAccount(tx, id, 0)
				if err != nil {
					return err
				}
				if !accessCreditAuthority(tx, a, watchOriginal) {
					return subscriptionDenied()
				}
				return nil
			})
			if err != nil && !sent.Load() {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	var observed domain.SubscriptionQuotaObservation
	var outcome domain.SubscriptionResetOutcome
	var consumeErr, quotaErr error
	cleanup := true
	var bundle []byte
	_, route, consumeErr := s.readNetworkRoute(ctx, "")
	if consumeErr == nil && route.Profile.Mode != domain.ProxyDirect {
		consumeErr = domain.Fail(domain.Unsupported, "Server reset-credit consumption requires Direct native routing.", "Select Direct explicitly; this native profile has no proxy adapter.")
	}
	if consumeErr == nil {
		vault, vaultErr := s.secrets()
		consumeErr = vaultErr
		if consumeErr == nil {
			bundle, consumeErr = s.readAccessCreditBundle(ctx, vault, id, original)
		}
	}
	if consumeErr == nil {
		parsed, identity, parseErr := subscription.Parse(bundle)
		consumeErr = parseErr
		if consumeErr == nil {
			consumeErr = s.Store.Read(ctx, func(tx *store.Tx) error {
				_, a, err := subscriptionAccount(tx, id, 0)
				if err != nil {
					return err
				}
				raw, _ := json.Marshal(struct{ Account, User string }{identity.Account, identity.User})
				defer clear(raw)
				if a.Subscription.IdentityCommitment != s.accountCommitment(s.Identity.ServerID, raw) || !accessCreditAuthority(tx, a, original) {
					return subscriptionDenied()
				}
				return nil
			})
		}
		if consumeErr == nil {
			auth := codex.QuotaAuthentication{Access: parsed.Tokens.Access, Account: identity.Account, Plan: identity.Plan}
			opener := s.creditOpen
			if opener == nil {
				opener = openServerCreditAccess
			}
			native, openErr := opener(ctx, s.scopeFor(id), original.AttemptID, auth, s.logger)
			consumeErr = openErr
			if native == nil {
				cleanup = openErr != nil && domain.SafeError(openErr).Code != domain.RecoveryRequired
			} else {
				if consumeErr == nil {
					// Recheck the configured route and original authority after actual
					// process readiness; the open claim itself cannot send.
					var currentRoute domain.NetworkRoute
					_, currentRoute, consumeErr = s.readNetworkRoute(ctx, "")
					if consumeErr == nil && !reflect.DeepEqual(currentRoute, route) {
						consumeErr = subscriptionDenied()
					}
				}
				if consumeErr == nil {
					result, claimErr := s.Store.Mutate(ctx, original.AttemptID, "subscription.server.credit.send", original, func(tx *store.Tx) (any, error) {
						r, a, err := subscriptionAccount(tx, id, 0)
						if err != nil {
							return nil, err
						}
						o := a.Subscription.ServerCredit
						if !accessCreditAuthority(tx, a, original) || o.Phase != domain.SubscriptionObservationSending || o.SendClaimed {
							return nil, subscriptionDenied()
						}
						if !o.EverSent && !currentCreditInventory(a, *o, tx.ObservationTime()) {
							return nil, domain.InvalidSubscriptionObservation()
						}
						o.SendClaimed, o.EverSent = true, true
						_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
						return struct{}{}, err
					})
					consumeErr = claimErr
					if consumeErr == nil && result.Replayed {
						consumeErr = subscriptionDenied()
					}
					if consumeErr == nil {
						sent.Store(true)
						original.SendClaimed, original.EverSent = true, true
						outcome, observed, consumeErr, quotaErr = native.ConsumeExternalResetCredit(ctx, original, auth, func(v domain.SubscriptionResetOutcome) error {
							// Checkpoint the provider outcome before the independent
							// read and cleanup; neither can erase consumption.
							bounded, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 30*time.Second)
							defer stop()
							_, err := s.Store.Mutate(bounded, domain.NewID(), "subscription.server.credit.outcome", original.AttemptID, func(tx *store.Tx) (any, error) {
								r, a, err := subscriptionAccount(tx, id, 0)
								if err != nil {
									return nil, err
								}
								o := a.Subscription.ServerCredit
								if !sameAccessCredit(o, original) || !o.SendClaimed {
									return nil, subscriptionDenied()
								}
								o.Outcome = v
								_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
								return struct{}{}, err
							})
							return err
						})
						if quotaErr == nil && consumeErr == nil && outcome.Valid() {
							quotaErr = codex.ValidateQuotaSecrets(observed, parsed.Tokens.Access, parsed.Tokens.ID, parsed.Tokens.Refresh, identity.Account, identity.User, identity.Email)
							if quotaErr == nil {
								quotaErr = codex.ValidateQuotaDisplayName(observed, identity.DisplayName)
							}
							if quotaErr != nil {
								observed = domain.SubscriptionQuotaObservation{}
							}
						}
					}
				}
				cleanup = native.Close() == nil
			}
		}
	}
	clear(bundle)
	cancel()
	<-checked
	bounded, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 30*time.Second)
	defer stop()
	_, err = s.Store.Mutate(bounded, domain.NewID(), "subscription.server.credit.cleanup", struct {
		Account, Attempt domain.ID
		Confirmed        bool
	}{id, original.AttemptID, cleanup}, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		o := a.Subscription.ServerCredit
		if !sameAccessCredit(o, original) || o.Phase != domain.SubscriptionObservationSending {
			return nil, subscriptionDenied()
		}
		o.CleanupConfirmed = cleanup
		if !cleanup {
			// Cleanup uncertainty fences only this credit owner. It never fails or
			// releases the independent Execute lease or its credential writer.
			o.Phase, o.ErrorCode = domain.SubscriptionObservationUncertain, domain.RecoveryRequired
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err == nil && cleanup {
		err = s.finishAccessCredit(bounded, id, original, outcome, observed, creditCode(consumeErr), creditCode(quotaErr))
	}
	if err != nil {
		s.logger.WarnContext(bounded, "server_credit_finish_failed", "operation_id", original.ID, "code", creditCode(err), "cleanup_confirmed", cleanup, "lane", "access_only")
	} else {
		s.logger.InfoContext(bounded, "server_credit_finished", "operation_id", original.ID, "code", creditCode(consumeErr), "quota_code", creditCode(quotaErr), "cleanup_confirmed", cleanup, "lane", "access_only")
	}
}

// finishAccessCredit settles one joined access-only attempt. A terminal result
// first retires the obsolete captured reference through the checked boundary; an
// uncertain possible send keeps that reference for explicit same-key
// reconciliation.
func (s *Service) finishAccessCredit(ctx context.Context, id domain.ID, original domain.ServerCreditOperation, outcome domain.SubscriptionResetOutcome, observed domain.SubscriptionQuotaObservation, code, quotaCode domain.Code) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	input := struct {
		Account, Attempt domain.ID
		Outcome          domain.SubscriptionResetOutcome
		Code, QuotaCode  domain.Code
	}{id, original.AttemptID, outcome, code, quotaCode}
	if _, replayed, err := s.Store.Replay(ctx, original.FinishID, "subscription.server.credit.finish", input); err != nil || replayed {
		return err
	}
	var terminal bool
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return err
		}
		o := a.Subscription.ServerCredit
		if !sameAccessCredit(o, original) || o.Phase != domain.SubscriptionObservationSending || !o.CleanupConfirmed {
			return subscriptionDenied()
		}
		// Only a durably recorded outcome (or a never-sent attempt) authorizes
		// retiring the credential; an in-memory outcome whose checkpoint failed
		// does not.
		terminal = o.Outcome.Valid() || !o.EverSent
		return nil
	}); err != nil {
		return err
	}
	if !terminal && outcome.Valid() {
		// The provider returned a valid outcome but its checkpoint failed. Record
		// it before any credential retirement so a later failure or crash still
		// leaves the consumed outcome and the captured reference for reconciliation.
		if _, err := s.Store.Mutate(ctx, domain.NewID(), "subscription.server.credit.outcome.finish", struct {
			Account, Attempt domain.ID
			Outcome          domain.SubscriptionResetOutcome
		}{id, original.AttemptID, outcome}, func(tx *store.Tx) (any, error) {
			r, a, err := subscriptionAccount(tx, id, 0)
			if err != nil {
				return nil, err
			}
			o := a.Subscription.ServerCredit
			if !sameAccessCredit(o, original) || o.Phase != domain.SubscriptionObservationSending || !o.CleanupConfirmed {
				return nil, subscriptionDenied()
			}
			o.Outcome = outcome
			_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
			return struct{}{}, err
		}); err != nil {
			return err
		}
		terminal = true
	}
	if terminal {
		if err := s.retireServerCreditReference(ctx, id, original); err != nil {
			return err
		}
	}
	_, err = s.Store.Mutate(ctx, original.FinishID, "subscription.server.credit.finish", input, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		o := a.Subscription.ServerCredit
		if !sameAccessCredit(o, original) || o.Phase != domain.SubscriptionObservationSending || !o.CleanupConfirmed {
			return nil, subscriptionDenied()
		}
		if outcome.Valid() {
			o.Outcome = outcome
		}
		accessCreditSettlePhase(o, code)
		recovered := false
		// Consumption and quota recovery stay separate. Apply the independent read
		// only after a valid provider outcome, with the original actor and
		// connection still bound; a failed read retains the last quota.
		if o.Outcome.Valid() && o.SendClaimed && quotaAccountReady(a) && a.Connection.ID == original.ConnectionID && subscriptionActorValid(tx, original.Actor) == nil {
			if quotaCode == "" {
				projected, state := a, *a.Subscription
				projected.Subscription, projected.Quota = &state, append([]domain.QuotaWindow(nil), a.Quota...)
				recovered, err = domain.ApplySubscriptionQuota(&projected, observed, time.Now().UTC())
				if err != nil {
					quotaCode, recovered = creditCode(err), false
				} else {
					a = projected
					o = a.Subscription.ServerCredit
				}
			}
			if quotaCode != "" {
				a.Subscription.QuotaState = domain.ObservationFailed
			}
		}
		o.QuotaErrorCode = quotaCode
		if _, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		if recovered {
			_, err = tx.CreateSubscriptionRecoveryInbox(id, original.ID, original.ConnectionID, observed.ObservedAt)
		}
		return accountReceipt{ID: id}, err
	})
	return err
}

// retireServerCreditReference mirrors server quota retirement. The caller holds
// account serialization, native cleanup is confirmed, and the exact retained
// operation authorizes deleting only its obsolete captured AccountLogin reference.
func (s *Service) retireServerCreditReference(ctx context.Context, id domain.ID, original domain.ServerCreditOperation) error {
	var current domain.ID
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return err
		}
		st := a.Subscription
		o := st.ServerCredit
		if !sameAccessCredit(o, original) || o.Validate() != nil || !o.CleanupConfirmed || (o.Phase != domain.SubscriptionObservationSending && o.Phase != domain.SubscriptionObservationUncertain) {
			return subscriptionDenied()
		}
		current = st.Generation
		if current != original.Generation && (current.Validate() != nil || generationHeld(st, original.Generation, false, true)) {
			return subscriptionDenied()
		}
		return nil
	})
	if err != nil || current == original.Generation {
		return err
	}
	return s.deleteCapturedReference(ctx, id, original.Generation, current, "server_credit_reference_retained", original.ID)
}
