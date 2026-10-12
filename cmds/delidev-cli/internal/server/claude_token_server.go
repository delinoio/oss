// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"slices"
	"time"
)

func (s *Service) managedClaude(ctx context.Context, id domain.ID) (bool, error) {
	var managed bool
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, e := claudeSubscriptionAccount(tx, id, 0)
		if e == nil {
			managed = a.Subscription != nil && a.Subscription.ManagedClaudeToken
		}
		return e
	})
	return managed, err
}
func (s *Service) requestClaudeToken(ctx context.Context, req *connect.Request[pb.RequestSubscriptionRequest]) (*connect.Response[pb.RequestSubscriptionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	actor, err := claudeActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	action := subscriptionAction(req.Msg.Action)
	if req.Msg.DeviceCode || req.Msg.MachineId != "" || action != domain.SubscriptionLogin && action != domain.SubscriptionLogout {
		return nil, rpc.Error(domain.Fail(domain.Unsupported, "Managed Claude tokens cannot be renewed.", "Create a new account and complete browser sign-in after rejection or expiry."), c)
	}
	input := struct {
		Account  domain.ID
		Revision uint64
		Actor    domain.Principal
		Action   domain.SubscriptionAction
	}{domain.ID(m.Id), m.ExpectedRevision, actor, action}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.token.request", input, func(tx *store.Tx) (any, error) {
		r, a, err := claudeSubscriptionAccount(tx, input.Account, input.Revision)
		if err != nil {
			return nil, err
		}
		if subscriptionActorValid(tx, actor) != nil {
			return nil, subscriptionDenied()
		}
		if a.Subscription == nil {
			a.Subscription = &domain.SubscriptionState{}
		}
		st := a.Subscription
		if st.Pending != nil || st.RecoveryRequired || a.Removal != nil || st.NativeProfileID != "" || st.OwnerMachineID != "" || st.NativeOperation != nil || st.Lease != nil && action != domain.SubscriptionLogout {
			return nil, subscriptionDenied()
		}
		if action == domain.SubscriptionLogin && (a.Connection != nil || st.Generation != "" || st.ManagedClaudeToken) || action == domain.SubscriptionLogout && !st.ManagedClaudeToken {
			return nil, subscriptionDenied()
		}
		st.ManagedClaudeToken = true
		now := time.Now().UTC()
		st.ServerOperation = &domain.ServerSubscriptionOperation{ID: domain.ID(m.RequestId), FinishID: domain.NewID(), Epoch: s.subscriptionServerEpoch(), Actor: actor, Action: action, State: domain.SubscriptionPreparing, StartedAt: now, ExpiresAt: now.Add(15 * time.Minute)}
		st.Pending = &domain.SubscriptionOperation{ID: domain.ID(m.RequestId), Actor: actor, Action: action, Phase: domain.SubscriptionQueued}
		if action == domain.SubscriptionLogout {
			a.Health = domain.AccountRevoked
			if err := cancelAccountExecutions(tx, r.ID); err != nil {
				return nil, err
			}
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	r, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "claude_token_operation_accepted", "operation_id", m.RequestId, "action", action, "replayed", result.Replayed)
	return connect.NewResponse(&pb.RequestSubscriptionResponse{Account: rpc.Resource(r), OperationId: m.RequestId, Replayed: result.Replayed}), nil
}
func (s *Service) getClaudeTokenProgress(ctx context.Context, req *connect.Request[pb.GetSubscriptionProgressRequest]) (*connect.Response[pb.GetSubscriptionProgressResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := claudeActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	response := &pb.GetSubscriptionProgressResponse{}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := claudeSubscriptionAccount(tx, domain.ID(req.Msg.AccountId), 0)
		if err != nil {
			return err
		}
		st := a.Subscription
		if st == nil || !st.ManagedClaudeToken || st.ServerOperation == nil {
			return subscriptionDenied()
		}
		o := st.ServerOperation
		if string(o.ID) != req.Msg.OperationId || o.Actor != actor || subscriptionActorValid(tx, actor) != nil {
			return subscriptionDenied()
		}
		state := o.State
		if st.RecoveryRequired || o.Epoch != s.subscriptionServerEpoch() && o.Active() {
			state = domain.SubscriptionRecovery
		}
		if !time.Now().Before(o.ExpiresAt) && (state == domain.SubscriptionPreparing || state == domain.SubscriptionWaiting) {
			state = domain.SubscriptionExpired
		}
		response.State = loginState(state)
		response.LoginMethod = pb.SubscriptionLoginMethod(o.LoginMethod)
		response.NativeDiagnostic = wireNativeSubscriptionDiagnostic(o.NativeDiagnostic)
		response.Canceled = st.Pending != nil && st.Pending.Canceled
		if state == domain.SubscriptionWaiting && !response.Canceled {
			response.Url = s.subscriptionProgress[o.ID].URL
		}
		if state == domain.SubscriptionSucceeded && a.Connection != nil && st.Generation == o.FinishID {
			response.Generation = string(st.Generation)
			response.SuggestedName = "Claude"
		}
		if state != domain.SubscriptionWaiting {
			s.clearClaudeLoginInput(o.ID)
		}
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	return connect.NewResponse(response), nil
}
func (s *Service) cancelClaudeToken(ctx context.Context, req *connect.Request[pb.CancelSubscriptionRequest]) (*connect.Response[pb.CancelSubscriptionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	actor, err := claudeActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	var operation domain.ID
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.token.cancel", struct {
		Account  domain.ID
		Revision uint64
		Actor    domain.Principal
	}{domain.ID(m.Id), m.ExpectedRevision, actor}, func(tx *store.Tx) (any, error) {
		r, a, e := claudeSubscriptionAccount(tx, domain.ID(m.Id), m.ExpectedRevision)
		if e != nil {
			return nil, e
		}
		st := a.Subscription
		if st == nil || st.Pending == nil || st.ServerOperation == nil || st.ServerOperation.Actor != actor || st.Pending.Action != domain.SubscriptionLogin {
			return nil, subscriptionDenied()
		}
		operation = st.Pending.ID
		st.Pending.Canceled = true
		if !st.ServerOperation.NativeStarted {
			st.ServerOperation.State = domain.SubscriptionCanceled
			st.Pending = nil
		}
		_, e = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, e
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.clearClaudeLoginInput(operation)
	r, err := s.accountRecord(ctx, domain.ID(m.Id))
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	return connect.NewResponse(&pb.CancelSubscriptionResponse{Account: rpc.Resource(r), Replayed: result.Replayed}), nil
}
func (s *Service) submitClaudeTokenCode(ctx context.Context, req *connect.Request[pb.SubmitSubscriptionLoginCodeRequest]) (*connect.Response[pb.SubmitSubscriptionLoginCodeResponse], error) {
	defer clear(req.Msg.Code)
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	actor, err := claudeActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	operation := domain.ID(req.Msg.OperationId)
	input := struct {
		Account, Operation domain.ID
		Revision           uint64
		Actor              domain.Principal
		Commitment         string
	}{domain.ID(m.Id), operation, m.ExpectedRevision, actor, s.accountCommitment(domain.ID(m.RequestId), req.Msg.Code)}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.token.code", input, func(tx *store.Tx) (any, error) {
		r, a, e := claudeSubscriptionAccount(tx, input.Account, input.Revision)
		if e != nil {
			return nil, e
		}
		st := a.Subscription
		if st == nil || !st.ManagedClaudeToken || st.RecoveryRequired || st.Pending == nil || st.Pending.Canceled || st.ServerOperation == nil {
			return nil, subscriptionDenied()
		}
		o := st.ServerOperation
		if o.ID != operation || o.Actor != actor || o.Epoch != s.subscriptionServerEpoch() || !o.NativeStarted || o.State != domain.SubscriptionWaiting || o.CodeSubmissionID != "" || !time.Now().Before(o.ExpiresAt) || subscriptionActorValid(tx, actor) != nil || !claude.SetupTokenCodeValid(req.Msg.Code, s.subscriptionProgress[operation].URL) {
			return nil, subscriptionDenied()
		}
		o.CodeSubmissionID = domain.ID(m.RequestId)
		_, e = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, e
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if !result.Replayed {
		if s.claudeLoginCodes == nil {
			s.claudeLoginCodes = map[domain.ID][]byte{}
		}
		s.claudeLoginCodes[operation] = slices.Clone(req.Msg.Code)
	}
	return connect.NewResponse(&pb.SubmitSubscriptionLoginCodeResponse{Accepted: true}), nil
}

func (s *Service) runServerClaudeToken(parent context.Context, id domain.ID) {
	var o domain.ServerSubscriptionOperation
	unlock, err := s.lockAccounts(parent)
	if err != nil {
		return
	}
	_, err = s.Store.Mutate(parent, domain.NewID(), "claude.token.claim", struct{ Account domain.ID }{id}, func(tx *store.Tx) (any, error) {
		r, a, e := claudeSubscriptionAccount(tx, id, 0)
		if e != nil {
			return nil, e
		}
		st := a.Subscription
		if st == nil || !st.ManagedClaudeToken || st.RecoveryRequired || st.Lease != nil || st.Pending == nil || st.ServerOperation == nil || st.Pending.Phase != domain.SubscriptionQueued || st.ServerOperation.NativeStarted || st.ServerOperation.Epoch != s.subscriptionServerEpoch() || st.ServerOperation.State != domain.SubscriptionPreparing {
			return nil, subscriptionDenied()
		}
		o = *st.ServerOperation
		if st.Pending.Canceled || subscriptionActorValid(tx, o.Actor) != nil || !time.Now().Before(o.ExpiresAt) {
			st.ServerOperation.State = domain.SubscriptionCanceled
			st.Pending = nil
		} else {
			st.ServerOperation.NativeStarted = true
			st.ServerOperation.Generation = st.Generation
			st.Pending.Phase = domain.SubscriptionClaimed
			o = *st.ServerOperation
		}
		_, e = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, e
	})
	unlock()
	if err != nil || !o.NativeStarted {
		return
	}
	ctx, cancel := context.WithDeadline(parent, o.ExpiresAt)
	defer cancel()
	cleanup, success := true, false
	var token []byte
	var native serverClaudeTokenNative
	if o.Action == domain.SubscriptionLogin {
		opener := s.claudeTokenOpen
		if opener == nil {
			opener = openServerClaudeToken
		}
		native, err = opener(ctx, s.Store.Root(), o.ID, s.logger)
		cleanup = err == nil || domain.SafeError(err).Code != domain.RecoveryRequired
		if native != nil {
			tick := time.NewTicker(100 * time.Millisecond)
			for err == nil && !success {
				progress, e := native.Progress()
				err = e
				if err != nil {
					break
				}
				var code []byte
				gate, e := s.lockAccounts(ctx)
				if e != nil {
					err = e
					break
				}
				needsMutation := false
				e = s.Store.Read(ctx, func(tx *store.Tx) error {
					_, a, e := claudeSubscriptionAccount(tx, id, 0)
					if e != nil {
						return e
					}
					st := a.Subscription
					current := st.ServerOperation
					if st.RecoveryRequired || current == nil || current.ID != o.ID || current.Epoch != o.Epoch || !current.NativeStarted || st.Pending == nil || st.Pending.Canceled || subscriptionActorValid(tx, o.Actor) != nil || !time.Now().Before(o.ExpiresAt) {
						return subscriptionDenied()
					}
					if progress.URL != "" {
						if claude.ValidateSetupTokenURL(progress.URL) != nil {
							return subscriptionDenied()
						}
						previous := s.subscriptionProgress[o.ID].URL
						if previous != "" && previous != progress.URL {
							return subscriptionDenied()
						}
						if s.subscriptionProgress == nil {
							s.subscriptionProgress = map[domain.ID]subscriptionProgress{}
						}
						s.subscriptionProgress[o.ID] = subscriptionProgress{URL: progress.URL}
					}
					needsMutation = progress.URL != "" && current.State != domain.SubscriptionWaiting || current.CodeSubmissionID != "" && !current.CodeConsumed
					return nil
				})
				// Idle observations retain private progress without durable receipt churn.
				if e == nil && needsMutation {
					_, e = s.Store.Mutate(ctx, domain.NewID(), "claude.token.observe", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
						r, a, e := claudeSubscriptionAccount(tx, id, 0)
						if e != nil {
							return nil, e
						}
						st := a.Subscription
						current := st.ServerOperation
						if st.RecoveryRequired || current == nil || current.ID != o.ID || current.Epoch != o.Epoch || !current.NativeStarted || st.Pending == nil || st.Pending.Canceled || subscriptionActorValid(tx, o.Actor) != nil || !time.Now().Before(o.ExpiresAt) {
							return nil, subscriptionDenied()
						}
						changed := false
						if progress.URL != "" {
							if claude.ValidateSetupTokenURL(progress.URL) != nil {
								return nil, subscriptionDenied()
							}
							previous := s.subscriptionProgress[o.ID].URL
							if previous != "" && previous != progress.URL {
								return nil, subscriptionDenied()
							}
							if current.State != domain.SubscriptionWaiting {
								current.State = domain.SubscriptionWaiting
								current.LoginMethod = domain.SubscriptionBrowserCode
								changed = true
							}
							if s.subscriptionProgress == nil {
								s.subscriptionProgress = map[domain.ID]subscriptionProgress{}
							}
							s.subscriptionProgress[o.ID] = subscriptionProgress{URL: progress.URL}
						}
						if current.CodeSubmissionID != "" && !current.CodeConsumed {
							current.CodeConsumed = true
							code = slices.Clone(s.claudeLoginCodes[o.ID])
							clear(s.claudeLoginCodes[o.ID])
							delete(s.claudeLoginCodes, o.ID)
							changed = true
							if len(code) == 0 {
								return nil, subscriptionDenied()
							}
						}
						if changed {
							_, e = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
						}
						return struct{}{}, e
					})
				}
				gate()
				if e != nil {
					clear(code)
					err = e
					break
				}
				if len(code) > 0 {
					err = native.Submit(code)
					if err != nil {
						break
					}
				}
				select {
				case <-ctx.Done():
					err = domain.SafeError(ctx.Err())
				case <-native.Done():
					token, err = native.Token()
					success = err == nil
				case <-tick.C:
				}
			}
			tick.Stop()
			closeErr := native.Close()
			cleanup = closeErr == nil
			if closeErr != nil {
				err = closeErr
				success = false
			}
		}
	} else {
		success = true
	}
	defer clear(token)
	finishCtx, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 30*time.Second)
	defer stop()
	if finishErr := s.finishServerClaudeToken(finishCtx, id, o, token, cleanup, success, err); finishErr != nil {
		s.logger.Warn("claude_token_finish_pending", "operation_id", o.ID, "code", domain.SafeError(finishErr).Code)
		_ = s.markClaudeTokenRecovery(finishCtx, id, o.ID)
	}
}
func (s *Service) markClaudeTokenRecovery(ctx context.Context, id, operation domain.ID) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	s.clearClaudeLoginInput(operation)
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.token.recovery", struct{ Account, Operation domain.ID }{id, operation}, func(tx *store.Tx) (any, error) {
		r, a, e := claudeSubscriptionAccount(tx, id, 0)
		if e != nil {
			return nil, e
		}
		if a.Subscription.ServerOperation == nil || a.Subscription.ServerOperation.ID != operation {
			return nil, subscriptionDenied()
		}
		a.Subscription.RecoveryRequired = true
		a.Health = domain.AccountFailed
		a.Subscription.ServerOperation.State = domain.SubscriptionRecovery
		_, e = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, e
	})
	return err
}
func (s *Service) finishServerClaudeToken(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, token []byte, cleanup, success bool, cause error) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	defer s.clearClaudeLoginInput(o.ID)
	var account domain.Account
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, e := claudeSubscriptionAccount(tx, id, 0)
		account = a
		if e != nil {
			return e
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || st.ServerOperation.Epoch != o.Epoch || !st.ServerOperation.NativeStarted || st.RecoveryRequired || st.Generation != o.Generation || st.Lease != nil || st.Pending == nil {
			return subscriptionDenied()
		}
		if st.Pending.Canceled || subscriptionActorValid(tx, o.Actor) != nil || !time.Now().Before(o.ExpiresAt) {
			success = false
		}
		return nil
	})
	if err != nil {
		return err
	}
	if cleanup {
		_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.token.native.cleanup", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
			r, a, e := claudeSubscriptionAccount(tx, id, 0)
			if e != nil {
				return nil, e
			}
			if a.Subscription.ServerOperation == nil || a.Subscription.ServerOperation.ID != o.ID {
				return nil, subscriptionDenied()
			}
			a.Subscription.ServerOperation.ClaudeCleanupConfirmed = true
			_, e = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
			return struct{}{}, e
		})
		if err != nil {
			return err
		}
	}
	vault, err := s.secrets()
	if err != nil {
		return err
	}
	state := domain.SubscriptionFailed
	if success {
		state = domain.SubscriptionSucceeded
	} else if !cleanup {
		state = domain.SubscriptionRecovery
	} else if account.Subscription.Pending.Canceled {
		state = domain.SubscriptionCanceled
	} else if !time.Now().Before(o.ExpiresAt) {
		state = domain.SubscriptionExpired
	} else if cause != nil && domain.SafeError(cause).Code == domain.Unsupported {
		state = domain.SubscriptionUnsupported
	}
	keep := o.Generation
	commitment := ""
	if success && o.Action == domain.SubscriptionLogin {
		raw, e := subscription.SealClaudeToken(s.Identity.ServerID, id, o.FinishID, token)
		if e != nil {
			return e
		}
		defer clear(raw)
		commitment = s.accountCommitment(o.FinishID, raw)
		if _, e = vault.Put(ctx, credentials.Ref{Owner: id, ID: o.FinishID, Purpose: credentials.AccountLogin}, raw); e != nil {
			return e
		}
		keep = o.FinishID
	} else if success && o.Action == domain.SubscriptionLogout {
		keep = ""
	}
	// Publication follows protected cleanup. A failed staging/removal leaves the
	// original NativeStarted operation fenced, never a falsely connected account.
	if cleanup {
		if err := cleanupSubscriptionReferences(ctx, vault, id, keep); err != nil {
			return err
		}
	}
	_, err = s.Store.Mutate(ctx, o.FinishID, "claude.token.finish", struct {
		Account, Operation domain.ID
		State              domain.SubscriptionLoginState
		Cleanup            bool
	}{id, o.ID, state, cleanup}, func(tx *store.Tx) (any, error) {
		r, a, e := claudeSubscriptionAccount(tx, id, 0)
		if e != nil {
			return nil, e
		}
		st := a.Subscription
		if st.ServerOperation == nil || st.ServerOperation.ID != o.ID || !st.ServerOperation.NativeStarted || st.Generation != o.Generation || st.Lease != nil || st.Pending == nil {
			return nil, subscriptionDenied()
		}
		if success && (st.Pending.Canceled || subscriptionActorValid(tx, o.Actor) != nil || !time.Now().Before(o.ExpiresAt) || st.ServerOperation.Epoch != o.Epoch) {
			return nil, subscriptionDenied()
		}
		st.ServerOperation.State = state
		if !cleanup {
			st.RecoveryRequired = true
			a.Health = domain.AccountFailed
		} else {
			st.ServerOperation.NativeStarted = false
			st.Pending = nil
			if success && o.Action == domain.SubscriptionLogin {
				st.Generation = o.FinishID
				st.IdentityCommitment = commitment
				a.Connection = &domain.AccountConnection{ID: o.FinishID, Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}
				a.Health = domain.AccountReady
			} else if success && o.Action == domain.SubscriptionLogout {
				st.Generation = ""
				st.IdentityCommitment = ""
				a.Connection = nil
				a.Health = domain.AccountDisconnected
			}
		}
		_, e = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, e
	})
	if err == nil {
		s.logger.InfoContext(ctx, "claude_token_operation_finished", "operation_id", o.ID, "state", state, "cleanup_confirmed", cleanup)
	}
	return err
}
