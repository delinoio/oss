// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func newServerCredit(a domain.Account, input domain.SubscriptionObservationOperation, epoch domain.ID) domain.ServerCreditOperation {
	return domain.ServerCreditOperation{AccessOnly: true, ID: input.ID, Epoch: epoch, AttemptID: domain.NewID(), AttemptEpoch: epoch, FinishID: domain.NewID(), ConnectionID: input.ConnectionID, Generation: input.Generation, Actor: input.Actor, Phase: domain.SubscriptionObservationQueued, RequestedAt: input.RequestedAt, CreditID: input.CreditID, NextCredit: input.NextCredit, CreditsObservationID: input.CreditsObservationID}
}
func acceptServerCredit(tx *store.Tx, r store.Record, a domain.Account, op domain.ServerCreditOperation) error {
	if !serverQuotaReady(a) || op.Validate() != nil || op.ConnectionID != a.Connection.ID || op.Generation != a.Subscription.Generation || subscriptionActorValid(tx, op.Actor) != nil {
		return subscriptionDenied()
	}
	a.Subscription.ServerCredit = &op
	_, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	return err
}
func serverCreditAuthority(tx *store.Tx, a domain.Account, original domain.ServerCreditOperation) bool {
	if !quotaAccountReady(a) {
		return false
	}
	st := a.Subscription
	o := st.ServerCredit
	if original.AccessOnly {
		if !sameServerCreditProof(o, original) || st.Pending != nil || st.ServerQuotaActive() || st.Observation != nil && st.Observation.Active() || st.ServerOperation != nil && (st.ServerOperation.Active() || st.ServerOperation.NativeStarted) || st.Lease != nil && st.Lease.Action != domain.SubscriptionExecute || a.Connection.ID != original.ConnectionID || st.Generation != original.Generation || subscriptionActorValid(tx, original.Actor) != nil {
			return false
		}
		return original.Automatic == nil || frozenAutomaticCreditValid(st, *original.Automatic)
	}
	return o != nil && o.ID == original.ID && o.AttemptID == original.AttemptID && o.AttemptEpoch == original.AttemptEpoch && o.FinishID == original.FinishID && o.RequestedAt.Equal(original.RequestedAt) && o.Epoch == original.Epoch && o.Actor == original.Actor && o.CreditID == original.CreditID && o.NextCredit == original.NextCredit && o.CreditsObservationID == original.CreditsObservationID && o.ConnectionID == original.ConnectionID && o.Generation == original.Generation && a.Connection.ID == original.ConnectionID && st.Generation == original.Generation && st.ServerQuotaGeneration == original.Generation && st.OwnerMachineID == "" && st.Lease == nil && st.Pending == nil && !st.ServerQuotaActive() && (st.Observation == nil || !st.Observation.Active()) && (st.ServerOperation == nil || !st.ServerOperation.Active() && !st.ServerOperation.NativeStarted) && subscriptionActorValid(tx, original.Actor) == nil
}
func reconcileServerCredit(tx *store.Tx, r store.Record, a domain.Account, connection, generation domain.ID, actor domain.Principal, epoch domain.ID) error {
	o := a.Subscription.ServerCredit
	if o == nil || o.Phase != domain.SubscriptionObservationUncertain || !o.CleanupConfirmed || !o.EverSent || o.Outcome != "" || connection != o.ConnectionID || generation != o.Generation || actor != o.Actor || !serverCreditAuthority(tx, a, *o) {
		return subscriptionDenied()
	}
	// Explicit acceptance changes only the attempt. Provider idempotency and
	// original selection, actor, connection, generation and epoch stay immutable.
	o.AttemptID, o.AttemptEpoch, o.FinishID = domain.NewID(), epoch, domain.NewID()
	o.Phase, o.SendClaimed, o.CleanupConfirmed = domain.SubscriptionObservationQueued, false, false
	o.ErrorCode, o.QuotaErrorCode = "", ""
	_, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	return err
}
func (s *Service) initializeServerCredits(ctx context.Context) error {
	_, err := s.Store.Mutate(ctx, domain.NewID(), "subscription.server.credit.restart", struct{}{}, func(tx *store.Tx) (any, error) {
		rows, err := all(tx, domain.AccountKind)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			a, err := store.Decode[domain.Account](r)
			if err != nil {
				return nil, err
			}
			if a.Subscription == nil || a.Subscription.ServerCredit == nil {
				continue
			}
			o := a.Subscription.ServerCredit
			if !o.Active() || o.AttemptEpoch == s.subscriptionServerEpoch() {
				continue
			}
			if o.AccessOnly {
				// Keep the independent reference fence until checked retirement completes.
				if o.Phase == domain.SubscriptionObservationQueued {
					o.CleanupConfirmed = true
				}
				o.Phase, o.ErrorCode = domain.SubscriptionObservationUncertain, domain.RecoveryRequired
			} else if o.Phase == domain.SubscriptionObservationQueued {
				o.CleanupConfirmed = true
				if o.EverSent {
					o.Phase, o.ErrorCode = domain.SubscriptionObservationUncertain, domain.RecoveryRequired
				} else {
					o.Phase, o.ErrorCode = domain.SubscriptionObservationFailed, domain.Canceled
				}
			} else if !o.EverSent && o.CleanupConfirmed {
				o.Phase, o.ErrorCode = domain.SubscriptionObservationFailed, domain.Canceled
			} else if o.CleanupConfirmed && o.Outcome.Valid() {
				o.Phase = domain.SubscriptionObservationSucceeded
			} else {
				o.Phase, o.ErrorCode = domain.SubscriptionObservationUncertain, domain.RecoveryRequired
				if !o.CleanupConfirmed {
					if !o.AccessOnly {
						a.Subscription.RecoveryRequired, a.Health = true, domain.AccountFailed
					}
				}
			}
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
		}
		return struct{}{}, nil
	})
	if err != nil {
		return err
	}
	return s.reconcileRetainedServerCredits(ctx)
}
func creditCode(err error) domain.Code {
	if err == nil {
		return ""
	}
	code := domain.SafeError(err).Code
	if !domain.ValidSubscriptionObservationCode(code) {
		return domain.Internal
	}
	return code
}
func (s *Service) runServerCredit(parent context.Context, id domain.ID) {
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
		if o.Phase != domain.SubscriptionObservationQueued || o.AttemptEpoch != s.subscriptionServerEpoch() {
			return nil, subscriptionDenied()
		}
		original = *o
		if !serverCreditAuthority(tx, a, original) {
			o.CleanupConfirmed = true
			if o.AccessOnly {
				o.Phase = domain.SubscriptionObservationSending
				original = *o
			} else if o.EverSent {
				o.Phase, o.ErrorCode = domain.SubscriptionObservationUncertain, domain.RecoveryRequired
			} else {
				o.Phase, o.ErrorCode = domain.SubscriptionObservationFailed, domain.Canceled
			}
		} else {
			o.Phase = domain.SubscriptionObservationSending
			original = *o
			claimed = true
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err != nil {
		return
	}
	if !claimed {
		if original.AccessOnly && original.CleanupConfirmed {
			bounded, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 30*time.Second)
			defer stop()
			if err := s.finishServerCredit(bounded, id, original, "", domain.SubscriptionQuotaObservation{}, domain.Canceled, ""); err != nil {
				s.logger.WarnContext(bounded, "server_credit_rejected_cleanup_retained", "operation_id", original.ID, "code", creditCode(err))
			}
		}
		return
	}
	s.logger.InfoContext(ctx, "server_credit_claimed", "operation_id", original.ID)
	checked := make(chan struct{})
	// Keep the watcher snapshot immutable while the sending goroutine records
	// its local send proof for the native request and independent quota read.
	watchOriginal := original
	go func() {
		defer close(checked)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			err := s.Store.Read(ctx, func(tx *store.Tx) error {
				_, a, err := subscriptionAccount(tx, id, 0)
				if err != nil {
					return err
				}
				if !serverCreditAuthority(tx, a, watchOriginal) {
					return subscriptionDenied()
				}
				return nil
			})
			if err != nil {
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
	var bundle []byte
	cleanup := true
	var observed domain.SubscriptionQuotaObservation
	var outcome domain.SubscriptionResetOutcome
	var quotaErr error
	_, route, consumeErr := s.readNetworkRoute(ctx, "")
	if consumeErr == nil && route.Profile.Mode != domain.ProxyDirect {
		consumeErr = domain.Fail(domain.Unsupported, "Server reset-credit consumption requires Direct native routing.", "Select Direct explicitly; this native profile has no proxy adapter.")
	}
	if consumeErr == nil {
		vault, vaultErr := s.secrets()
		consumeErr = vaultErr
		if consumeErr == nil {
			bundle, consumeErr = s.readCreditBundle(ctx, vault, id, original)
		}
	}
	if consumeErr == nil {
		native, openErr := s.openCreditNative(ctx, id, original, bundle)
		consumeErr = openErr
		if native == nil {
			cleanup = openErr != nil && domain.SafeError(openErr).Code != domain.RecoveryRequired
		} else {
			// The initial claim reserves ownership, but cannot send. Recheck the
			// configured route and original authority after actual process readiness.
			var currentRoute domain.NetworkRoute
			_, currentRoute, consumeErr = s.readNetworkRoute(ctx, "")
			if consumeErr == nil && !reflect.DeepEqual(currentRoute, route) {
				consumeErr = subscriptionDenied()
			}
			if consumeErr == nil {
				result, claimErr := s.Store.Mutate(ctx, original.AttemptID, "subscription.server.credit.send", original, func(tx *store.Tx) (any, error) {
					r, a, err := subscriptionAccount(tx, id, 0)
					if err != nil {
						return nil, err
					}
					o := a.Subscription.ServerCredit
					if !serverCreditAuthority(tx, a, original) || o.Phase != domain.SubscriptionObservationSending || o.SendClaimed {
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
				if consumeErr == nil && !result.Replayed {
					original.SendClaimed, original.EverSent = true, true
					outcome, consumeErr = native.ConsumeServerResetCredit(ctx, original)
				} else if consumeErr == nil {
					consumeErr = subscriptionDenied()
				}
			}
			if outcome.Valid() {
				// Checkpoint the provider outcome before the independent read and
				// cleanup; a read failure or lost finish cannot erase consumption.
				bounded, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 30*time.Second)
				_, checkpointErr := s.Store.Mutate(bounded, domain.NewID(), "subscription.server.credit.outcome", original.AttemptID, func(tx *store.Tx) (any, error) {
					r, a, err := subscriptionAccount(tx, id, 0)
					if err != nil {
						return nil, err
					}
					o := a.Subscription.ServerCredit
					if o == nil || o.ID != original.ID || o.AttemptID != original.AttemptID || !o.SendClaimed {
						return nil, subscriptionDenied()
					}
					o.Outcome = outcome
					_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
					return struct{}{}, err
				})
				stop()
				if checkpointErr != nil {
					consumeErr = checkpointErr
				}
			}
			if original.SendClaimed {
				observed, quotaErr = native.ReadManagedQuota(ctx, domain.NewID())
				if original.AccessOnly && quotaErr == nil {
					parsed, identity, err := subscription.Parse(bundle)
					quotaErr = err
					if err == nil {
						quotaErr = codex.ValidateQuotaSecrets(observed, parsed.Tokens.Access, parsed.Tokens.ID, parsed.Tokens.Refresh, identity.Account, identity.User, identity.Email)
						if quotaErr == nil {
							quotaErr = codex.ValidateQuotaDisplayName(observed, identity.DisplayName)
						}
					}
				}
			}
			cleanup = native.Close(bundle) == nil
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
		if o == nil || o.ID != original.ID || o.AttemptID != original.AttemptID || o.Phase != domain.SubscriptionObservationSending {
			return nil, subscriptionDenied()
		}
		o.CleanupConfirmed = cleanup
		if !cleanup {
			o.Phase, o.ErrorCode = domain.SubscriptionObservationUncertain, domain.RecoveryRequired
			if !o.AccessOnly {
				a.Subscription.RecoveryRequired, a.Health = true, domain.AccountFailed
			}
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err == nil && cleanup {
		err = s.finishServerCredit(bounded, id, original, outcome, observed, creditCode(consumeErr), creditCode(quotaErr))
	}
	if err != nil {
		s.logger.WarnContext(bounded, "server_credit_finish_failed", "operation_id", original.ID, "code", creditCode(err), "cleanup_confirmed", cleanup)
	} else {
		s.logger.InfoContext(bounded, "server_credit_finished", "operation_id", original.ID, "code", creditCode(consumeErr), "cleanup_confirmed", cleanup)
	}
}
func (s *Service) finishServerCredit(ctx context.Context, id domain.ID, original domain.ServerCreditOperation, outcome domain.SubscriptionResetOutcome, observed domain.SubscriptionQuotaObservation, code, quotaCode domain.Code) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.retireServerCreditReference(ctx, id, original); err != nil {
		return err
	}
	_, err = s.Store.Mutate(ctx, original.FinishID, "subscription.server.credit.finish", struct {
		Account, Attempt domain.ID
		Outcome          domain.SubscriptionResetOutcome
		Code, QuotaCode  domain.Code
	}{id, original.AttemptID, outcome, code, quotaCode}, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		o := a.Subscription.ServerCredit
		if o == nil || o.ID != original.ID || o.AttemptID != original.AttemptID || o.Phase != domain.SubscriptionObservationSending || !o.CleanupConfirmed {
			return nil, subscriptionDenied()
		}
		authorized := serverCreditAuthority(tx, a, original)
		if outcome.Valid() {
			o.Outcome = outcome
		}
		if o.Outcome.Valid() {
			o.Phase, o.ErrorCode = domain.SubscriptionObservationSucceeded, ""
		} else if o.EverSent {
			o.Phase, o.ErrorCode = domain.SubscriptionObservationUncertain, domain.RecoveryRequired
		} else {
			o.Phase, o.ErrorCode = domain.SubscriptionObservationFailed, code
		}
		recovered := false
		if o.SendClaimed && authorized {
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
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		if err != nil {
			return nil, err
		}
		if recovered {
			_, err = tx.CreateSubscriptionRecoveryInbox(id, original.ID, original.ConnectionID, observed.ObservedAt)
		}
		return accountReceipt{ID: id}, err
	})
	return err
}

func sameServerCreditProof(o *domain.ServerCreditOperation, original domain.ServerCreditOperation) bool {
	return o != nil && o.ID == original.ID && o.AttemptID == original.AttemptID && o.AttemptEpoch == original.AttemptEpoch && o.FinishID == original.FinishID && o.RequestedAt.Equal(original.RequestedAt) && o.Epoch == original.Epoch && o.Actor == original.Actor && o.Generation == original.Generation && o.ConnectionID == original.ConnectionID && o.CreditID == original.CreditID && o.NextCredit == original.NextCredit && o.CreditsObservationID == original.CreditsObservationID && o.AccessOnly == original.AccessOnly && reflect.DeepEqual(o.Automatic, original.Automatic)
}
func frozenAutomaticCreditValid(st *domain.SubscriptionState, proof domain.ServerAutomaticCreditProof) bool {
	c := st.AutomaticCreditConsent
	e := st.AutomaticCreditEpisode
	return proof.Validate() == nil && c != nil && *c == proof.Consent && e != nil && e.ID == proof.EpisodeID && reflect.DeepEqual(e.ServerProof, &proof)
}

// Capture the reference under the same short serialization used by Execute Finish.
func (s *Service) readCreditBundle(ctx context.Context, vault accountSecrets, id domain.ID, o domain.ServerCreditOperation) ([]byte, error) {
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
		if !serverCreditAuthority(tx, a, o) {
			return subscriptionDenied()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return vault.Get(ctx, credentials.Ref{Owner: id, ID: o.Generation, Purpose: credentials.AccountLogin})
}

type creditConsumptionNative interface {
	ConsumeServerResetCredit(context.Context, domain.ServerCreditOperation) (domain.SubscriptionResetOutcome, error)
	ReadManagedQuota(context.Context, domain.ID) (domain.SubscriptionQuotaObservation, error)
	Close([]byte) error
}
type accessCreditConsumption struct {
	checkpoint func(domain.SubscriptionResetOutcome) error
	native     serverCreditAccessNative
	auth       codex.QuotaAuthentication
	observed   domain.SubscriptionQuotaObservation
	quotaErr   error
}

func (c *accessCreditConsumption) ConsumeServerResetCredit(ctx context.Context, o domain.ServerCreditOperation) (domain.SubscriptionResetOutcome, error) {
	outcome, observed, err, quotaErr := c.native.ConsumeExternalResetCredit(ctx, o, c.auth, c.checkpoint)
	c.observed, c.quotaErr = observed, quotaErr
	return outcome, err
}
func (c *accessCreditConsumption) ReadManagedQuota(context.Context, domain.ID) (domain.SubscriptionQuotaObservation, error) {
	return c.observed, c.quotaErr
}
func (c *accessCreditConsumption) Close([]byte) error {
	c.auth = codex.QuotaAuthentication{}
	return c.native.Close()
}
func (s *Service) openCreditNative(ctx context.Context, id domain.ID, o domain.ServerCreditOperation, bundle []byte) (creditConsumptionNative, error) {
	if !o.AccessOnly {
		opener := s.subscriptionOpen
		if opener == nil {
			opener = openServerSubscription
		}
		return opener(ctx, s.Store.Root(), o.AttemptID, bundle, s.logger)
	}
	parsed, identity, err := subscription.Parse(bundle)
	if err != nil {
		return nil, err
	}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(struct{ Account, User string }{identity.Account, identity.User})
		defer clear(raw)
		if !serverCreditAuthority(tx, a, o) || a.Subscription.IdentityCommitment != s.accountCommitment(s.Identity.ServerID, raw) {
			return subscriptionDenied()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	auth := codex.QuotaAuthentication{Access: parsed.Tokens.Access, Account: identity.Account, Plan: identity.Plan}
	opener := s.creditOpen
	if opener == nil {
		opener = openServerCreditAccess
	}
	native, err := opener(ctx, s.Store.Root(), o.AttemptID, auth, s.logger)
	if native == nil {
		return nil, err
	}
	return &accessCreditConsumption{native: native, auth: auth, checkpoint: func(outcome domain.SubscriptionResetOutcome) error {
		bounded, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 30*time.Second)
		defer stop()
		_, err := s.Store.Mutate(bounded, domain.NewID(), "subscription.server.credit.outcome", o.AttemptID, func(tx *store.Tx) (any, error) {
			r, a, err := subscriptionAccount(tx, id, 0)
			if err != nil {
				return nil, err
			}
			current := a.Subscription.ServerCredit
			if !sameServerCreditProof(current, o) || !current.SendClaimed {
				return nil, subscriptionDenied()
			}
			current.Outcome = outcome
			_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
			return struct{}{}, err
		})
		return err
	}}, err
}

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
		if credit.ID == o.CreditID && credit.ResetType == domain.CodexRateLimitsReset && credit.Status == domain.SubscriptionCreditAvailable && (credit.ExpiresAt == nil || credit.ExpiresAt.After(now)) {
			return true
		}
	}
	return false
}
