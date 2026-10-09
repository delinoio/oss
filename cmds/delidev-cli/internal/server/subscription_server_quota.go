// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"sync"
	"time"

	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func serverQuotaReady(a domain.Account) bool {
	if !quotaAccountReady(a) {
		return false
	}
	s := a.Subscription
	return s.Pending == nil && !s.ServerObservationActive() && (s.Lease == nil || s.Lease.Action == domain.SubscriptionExecute) && (s.Observation == nil || !s.Observation.Active()) && (s.ServerOperation == nil || !s.ServerOperation.Active() && !s.ServerOperation.NativeStarted)
}
func acceptServerQuota(tx *store.Tx, r store.Record, a domain.Account, op domain.ServerQuotaOperation) error {
	if !serverQuotaReady(a) || op.Validate() != nil || op.ConnectionID != a.Connection.ID || op.Generation != a.Subscription.Generation || subscriptionActorValid(tx, op.Actor) != nil {
		return subscriptionDenied()
	}
	a.Subscription.ServerQuota = &op
	_, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	return err
}
func newServerQuota(a domain.Account, id, epoch domain.ID, actor domain.Principal, now time.Time) domain.ServerQuotaOperation {
	return domain.ServerQuotaOperation{AccessOnly: true, ID: id, Epoch: epoch, FinishID: domain.NewID(), ConnectionID: a.Connection.ID, Generation: a.Subscription.Generation, Actor: actor, Phase: domain.SubscriptionObservationQueued, RequestedAt: now}
}

// Old claims remain evidence. A restart can settle independently checkpointed
// cleanup, but cannot launch an earlier queued or sending native operation.
func (s *Service) initializeServerQuotas(ctx context.Context) error {
	if err := s.initializeServerCredits(ctx); err != nil {
		return err
	}
	if err := s.initializeServerQuotaGenerations(ctx); err != nil {
		return err
	}
	_, err := s.Store.Mutate(ctx, domain.NewID(), "subscription.server.quota.restart", struct{}{}, func(tx *store.Tx) (any, error) {
		rows, err := all(tx, domain.AccountKind)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			a, err := store.Decode[domain.Account](r)
			if err != nil {
				return nil, err
			}
			if a.Subscription == nil || a.Subscription.ServerQuota == nil {
				continue
			}
			o := a.Subscription.ServerQuota
			if !o.Active() || o.Epoch == s.subscriptionServerEpoch() {
				continue
			}
			if o.AccessOnly && (o.Phase == domain.SubscriptionObservationQueued || o.CleanupConfirmed) {
				// Queued claims never opened native work; confirmed cleanup
				// still retains its reference until retirement is checked.
				o.CleanupConfirmed = true
				o.Phase = domain.SubscriptionObservationUncertain
				o.ErrorCode = domain.RecoveryRequired
				a.Subscription.QuotaState = domain.ObservationFailed
			} else if o.Phase == domain.SubscriptionObservationQueued || o.CleanupConfirmed {
				o.Phase = domain.SubscriptionObservationFailed
				o.ErrorCode = domain.Canceled
				a.Subscription.QuotaState = domain.ObservationFailed
			} else {
				o.Phase = domain.SubscriptionObservationUncertain
				o.ErrorCode = domain.RecoveryRequired
				a.Subscription.QuotaState = domain.ObservationFailed
				if !o.AccessOnly {
					a.Subscription.RecoveryRequired = true
					a.Health = domain.AccountFailed
				}
			}
			if _, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
		}
		return struct{}{}, nil
	})
	if err != nil {
		return err
	}
	return s.reconcileRetainedServerQuotas(ctx)
}
func (s *Service) runServerQuotas(ctx context.Context) {
	ctx = domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice})
	child, stop := context.WithCancel(ctx)
	defer stop()
	var workers sync.WaitGroup
	done := make(chan domain.ID, 16)
	running := map[domain.ID]bool{}
	defer func() { stop(); workers.Wait() }()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := s.queueDueSubscriptionQuotas(child, time.Now().UTC()); err != nil && child.Err() == nil {
			s.logger.WarnContext(ctx, "server_quota_queue_failed", "code", domain.SafeError(err).Code)
		}
		var ids []domain.ID
		err := s.Store.Read(child, func(tx *store.Tx) error {
			rows, err := all(tx, domain.AccountKind)
			if err != nil {
				return err
			}
			for _, r := range rows {
				a, err := store.Decode[domain.Account](r)
				if err != nil {
					return err
				}
				if a.Subscription != nil && a.Subscription.ServerCredit != nil && a.Subscription.ServerCredit.Phase == domain.SubscriptionObservationQueued && a.Subscription.ServerCredit.AttemptEpoch == s.subscriptionServerEpoch() {
					ids = append(ids, r.ID)
				}
				if a.Subscription != nil && a.Subscription.ServerQuota != nil && a.Subscription.ServerQuota.Phase == domain.SubscriptionObservationQueued && a.Subscription.ServerQuota.Epoch == s.subscriptionServerEpoch() {
					ids = append(ids, r.ID)
				}
			}
			return nil
		})
		if err != nil && child.Err() == nil {
			s.logger.WarnContext(ctx, "server_quota_scan_failed", "code", domain.SafeError(err).Code)
		}
		for _, id := range ids {
			if running[id] || len(running) >= 16 {
				continue
			}
			running[id] = true
			workers.Add(1)
			go func() { defer workers.Done(); s.runServerCredit(child, id); s.runServerQuota(child, id); done <- id }()
		}
		select {
		case <-child.Done():
			return
		case id := <-done:
			delete(running, id)
		case <-ticker.C:
		}
	}
}
func (s *Service) runServerQuota(parent context.Context, id domain.ID) {
	ctx, cancel := context.WithTimeout(domain.WithPrincipal(parent, domain.Principal{Type: domain.OwnerDevice}), time.Minute)
	defer cancel()
	var original domain.ServerQuotaOperation
	claimed := false
	_, err := s.Store.Mutate(ctx, domain.NewID(), "subscription.server.quota.claim", id, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerQuota == nil || st.ServerQuota.Phase != domain.SubscriptionObservationQueued || st.ServerQuota.Epoch != s.subscriptionServerEpoch() {
			return nil, subscriptionDenied()
		}
		o := st.ServerQuota
		original = *o
		if !o.AccessOnly || !quotaAccountReady(a) || st.Pending != nil || st.Lease != nil && st.Lease.Action != domain.SubscriptionExecute || st.Generation != o.Generation || a.Connection.ID != o.ConnectionID || st.ServerOperation != nil && (st.ServerOperation.Active() || st.ServerOperation.NativeStarted) || st.Observation != nil && st.Observation.Active() || subscriptionActorValid(tx, o.Actor) != nil {
			if o.AccessOnly {
				// This rejected queued claim opened no native owner. Keep its
				// reference fence through the same checked finish path.
				o.Phase = domain.SubscriptionObservationSending
				o.CleanupConfirmed = true
				original = *o
			} else {
				o.Phase = domain.SubscriptionObservationFailed
				o.ErrorCode = domain.Canceled
			}
		} else {
			o.Phase = domain.SubscriptionObservationSending
			original = *o
			claimed = true
		}
		if _, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	if err != nil {
		return
	}
	if !claimed {
		if original.AccessOnly && original.CleanupConfirmed {
			bounded, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 30*time.Second)
			defer stop()
			if err := s.finishServerQuota(bounded, id, original, domain.SubscriptionQuotaObservation{}, domain.Canceled); err != nil {
				s.logger.WarnContext(bounded, "server_quota_rejected_cleanup_retained", "operation_id", original.ID, "code", domain.SafeError(err).Code)
			}
		}
		return
	}
	s.logger.InfoContext(ctx, "server_quota_claimed", "operation_id", original.ID)
	// Cancellation is checked independently of the read. Revocation and ownership
	// changes cancel the original process; they never substitute another account.
	checked := make(chan struct{})
	go func() {
		defer close(checked)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			err := s.checkQuotaBinding(ctx, id, original)
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
	_, route, readErr := s.readNetworkRoute(ctx, "")
	if readErr == nil && route.Profile.Mode != domain.ProxyDirect {
		readErr = domain.Fail(domain.Unsupported, "Server quota observation requires Direct native routing.", "Select Direct explicitly; this native profile has no proxy adapter.")
	}
	var vault accountSecrets
	if readErr == nil {
		vault, readErr = s.secrets()
	}
	var bundle []byte
	if readErr == nil {
		bundle, readErr = s.readQuotaBundle(ctx, vault, id, original)
	}
	cleanup := true
	var observed domain.SubscriptionQuotaObservation
	if readErr == nil {
		parsed, identity, parseErr := subscription.Parse(bundle)
		readErr = parseErr
		if readErr == nil {
			readErr = s.Store.Read(ctx, func(tx *store.Tx) error {
				_, a, err := subscriptionAccount(tx, id, 0)
				if err != nil {
					return err
				}
				raw, _ := json.Marshal(struct{ Account, User string }{identity.Account, identity.User})
				defer clear(raw)
				if a.Subscription.IdentityCommitment != s.accountCommitment(s.Identity.ServerID, raw) || a.Subscription.Generation != original.Generation || a.Connection.ID != original.ConnectionID || subscriptionActorValid(tx, original.Actor) != nil {
					return subscriptionDenied()
				}
				return nil
			})
		}
		if readErr == nil {
			opener := s.quotaOpen
			if opener == nil {
				opener = openServerQuota
			}
			auth := codex.QuotaAuthentication{Access: parsed.Tokens.Access, Account: identity.Account, Plan: identity.Plan}
			native, openErr := opener(ctx, s.Store.Root(), original.ID, auth, s.logger)
			readErr = openErr
			if native == nil {
				cleanup = openErr != nil && domain.SafeError(openErr).Code != domain.RecoveryRequired
			} else {
				if openErr == nil {
					if readErr = s.checkQuotaBinding(ctx, id, original); readErr == nil {
						observed, readErr = native.ReadExternalQuota(ctx, original.ID, auth)
						if readErr == nil {
							readErr = codex.ValidateQuotaSecrets(observed, parsed.Tokens.Access, parsed.Tokens.ID, parsed.Tokens.Refresh, identity.Account, identity.User, identity.Email)
						}
					}
				}
				cleanup = native.Close() == nil
				if !cleanup {
					readErr = subscriptionDenied()
				}
			}
		}
	}

	clear(bundle)
	cancel()
	<-checked
	bounded, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 30*time.Second)
	defer stop()
	code := domain.Code("")
	if readErr != nil {
		code = domain.SafeError(readErr).Code
		if !domain.ValidSubscriptionObservationCode(code) {
			code = domain.Internal
		}
	}
	// Persist cleanup before result publication so lost publication or restart
	// cannot turn a joined failed read into another native send.
	_, err = s.Store.Mutate(bounded, domain.NewID(), "subscription.server.quota.cleanup", struct {
		Account, Operation domain.ID
		Confirmed          bool
	}{id, original.ID, cleanup}, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		o := a.Subscription.ServerQuota
		if o == nil || o.ID != original.ID || o.Epoch != original.Epoch || o.Actor != original.Actor || o.Generation != original.Generation || o.ConnectionID != original.ConnectionID || o.AccessOnly != original.AccessOnly || o.Phase != domain.SubscriptionObservationSending {
			return nil, subscriptionDenied()
		}
		o.CleanupConfirmed = cleanup
		if !cleanup {
			o.Phase = domain.SubscriptionObservationUncertain
			o.ErrorCode = domain.RecoveryRequired
			a.Subscription.QuotaState = domain.ObservationFailed
			if !o.AccessOnly {
				a.Subscription.RecoveryRequired = true
				a.Health = domain.AccountFailed
			}
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err == nil && cleanup {
		err = s.finishServerQuota(bounded, id, original, observed, code)
	}
	if err != nil {
		s.logger.WarnContext(bounded, "server_quota_finish_failed", "operation_id", original.ID, "code", domain.SafeError(err).Code, "cleanup_confirmed", cleanup)
	} else {
		s.logger.InfoContext(bounded, "server_quota_finished", "operation_id", original.ID, "code", code, "cleanup_confirmed", cleanup)
	}
}
func (s *Service) finishServerQuota(ctx context.Context, id domain.ID, original domain.ServerQuotaOperation, observed domain.SubscriptionQuotaObservation, code domain.Code) error {
	input := struct {
		Account, Operation domain.ID
		Code               domain.Code
	}{id, original.ID, code}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if _, replayed, err := s.Store.Replay(ctx, original.FinishID, "subscription.server.quota.finish", input); err != nil || replayed {
		return err
	}
	if err := s.retireServerQuotaReference(ctx, id, original); err != nil {
		return err
	}
	_, err = s.Store.Mutate(ctx, original.FinishID, "subscription.server.quota.finish", input, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		o := st.ServerQuota
		if o == nil || o.ID != original.ID || o.Epoch != original.Epoch || o.Generation != original.Generation || o.ConnectionID != original.ConnectionID || o.Actor != original.Actor || o.AccessOnly != original.AccessOnly || o.Phase != domain.SubscriptionObservationSending || !o.CleanupConfirmed {
			return nil, subscriptionDenied()
		}
		authorized := quotaAccountReady(a) && st.Pending == nil && (st.Lease == nil || st.Lease.Action == domain.SubscriptionExecute) && st.Generation == original.Generation && a.Connection.ID == original.ConnectionID && subscriptionActorValid(tx, original.Actor) == nil
		if !authorized {
			code = domain.Canceled
		}
		beforeQuota := a
		recovered := false
		if code == "" {
			// Projection can update an earlier window before rejecting a later
			// window at the retained inventory bound. Publish only a complete
			// projection; confirmed native cleanup still settles a failed read.
			projected := a
			projectedState := *st
			projected.Subscription = &projectedState
			projected.Quota = append([]domain.QuotaWindow(nil), a.Quota...)
			recovered, err = domain.ApplySubscriptionQuota(&projected, observed, time.Now().UTC())
			if err != nil {
				code = domain.SafeError(err).Code
				recovered = false
			} else {
				a = projected
				st = a.Subscription
			}
		}
		if code == "" {
			o.Phase = domain.SubscriptionObservationSucceeded
		} else {
			o.Phase = domain.SubscriptionObservationFailed
			st.QuotaState = domain.ObservationFailed
		}
		o.ErrorCode = code
		if _, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		if code == "" {
			if err := tx.ObserveQuotaNotification(beforeQuota, a, id, original.ID, observed); err != nil {
				return nil, err
			}
		}
		if recovered {
			_, err = tx.CreateSubscriptionRecoveryInbox(id, original.ID, original.ConnectionID, observed.ObservedAt)
			if err != nil {
				return nil, err
			}
		}
		return accountReceipt{ID: id}, nil
	})
	return err
}

// An upgrade can enable a previously settled server login without reauthentication.
// Only the original successful generation and independently checked protected
// references supply this proof. Missing or uncertain owners remain ineligible.
func (s *Service) initializeServerQuotaGenerations(ctx context.Context) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var rows []store.Record
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		allRows, err := all(tx, domain.AccountKind)
		if err != nil {
			return err
		}
		for _, r := range allRows {
			a, err := store.Decode[domain.Account](r)
			if err != nil {
				return err
			}
			if legacyServerQuotaGeneration(a, s.subscriptionServerEpoch()) {
				rows = append(rows, r)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	vault, err := s.secrets()
	if err != nil {
		s.logger.WarnContext(ctx, "server_quota_upgrade_unavailable", "code", domain.SafeError(err).Code)
		return nil
	}
	for _, r := range rows {
		a, err := store.Decode[domain.Account](r)
		if err != nil {
			return err
		}
		// Confirm the immutable current reference exists before retiring old refs.
		bundle, err := vault.Get(ctx, credentials.Ref{Owner: r.ID, ID: a.Subscription.Generation, Purpose: credentials.AccountLogin})
		clear(bundle)
		if err == nil {
			err = cleanupSubscriptionReferences(ctx, vault, r.ID, a.Subscription.Generation)
		}
		if err != nil {
			s.logger.WarnContext(ctx, "server_quota_upgrade_unavailable", "account_id", r.ID, "code", domain.SafeError(err).Code)
			continue
		}
		_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.server.quota.upgrade", struct {
			Account, Generation domain.ID
			Revision            uint64
		}{r.ID, a.Subscription.Generation, r.Revision}, func(tx *store.Tx) (any, error) {
			current, account, err := subscriptionAccount(tx, r.ID, r.Revision)
			if err != nil {
				return nil, err
			}
			if !legacyServerQuotaGeneration(account, s.subscriptionServerEpoch()) || account.Subscription.Generation != a.Subscription.Generation {
				return nil, subscriptionDenied()
			}
			account.Subscription.ServerQuotaGeneration = account.Subscription.Generation
			_, err = tx.Put(domain.AccountKind, current.ID, current.Revision, "", "", account)
			return struct{}{}, err
		})
		if err != nil {
			return err
		}
	}
	return nil
}
func legacyServerQuotaGeneration(a domain.Account, epoch domain.ID) bool {
	if !quotaAccountReady(a) {
		return false
	}
	st := a.Subscription
	o := st.ServerOperation
	return st.ServerQuotaGeneration == "" && st.OwnerMachineID == "" && st.Lease == nil && st.Pending == nil && !st.ServerObservationActive() && (st.Observation == nil || !st.Observation.Active()) && o != nil && o.Epoch != epoch && o.State == domain.SubscriptionSucceeded && !o.NativeStarted && o.FinishID == st.Generation && (o.Action == domain.SubscriptionLogin || o.Action == domain.SubscriptionRefresh)
}

// Account serialization protects reference capture only, never native/network
// work. The durable original quota record retains that reference through rotation.
func (s *Service) readQuotaBundle(ctx context.Context, vault accountSecrets, id domain.ID, o domain.ServerQuotaOperation) ([]byte, error) {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := s.checkQuotaBinding(ctx, id, o); err != nil {
		return nil, err
	}
	return vault.Get(ctx, credentials.Ref{Owner: id, ID: o.Generation, Purpose: credentials.AccountLogin})
}
func (s *Service) checkQuotaBinding(ctx context.Context, id domain.ID, o domain.ServerQuotaOperation) error {
	return s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return err
		}
		st := a.Subscription
		if !quotaAccountReady(a) || st.Pending != nil || st.Generation != o.Generation || a.Connection.ID != o.ConnectionID || st.ServerQuota == nil || st.ServerQuota.ID != o.ID || st.ServerQuota.Epoch != o.Epoch || st.ServerQuota.Phase != domain.SubscriptionObservationSending || st.ServerQuota.Actor != o.Actor || subscriptionActorValid(tx, o.Actor) != nil {
			return subscriptionDenied()
		}
		return nil
	})
}
