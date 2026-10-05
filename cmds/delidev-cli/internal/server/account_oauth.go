// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type oauthLive struct {
	verifier      []byte
	authorization string
	expires       time.Time
}
type oauthExchange interface {
	Exchange(context.Context, []byte, []byte) ([]byte, error)
}
type oauthReceipt struct {
	AttemptID domain.ID `json:"attempt_id"`
}

func oauthProblem() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The OAuth exchange or local connection has an uncertain outcome.", "Inspect the OpenRouter keys dashboard. Retry only the original local completion if its credential is already protected; never resend an exchange.")
}
func requireOAuthActor(ctx context.Context) (domain.Principal, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return actor, domain.Fail(domain.PermissionDenied, "OAuth requires an owner or paired client.", "Use an authenticated product client.")
	}
	return actor, nil
}
func (s *Service) oauthCommitment(kind string, id domain.ID, value []byte) string {
	m := hmac.New(sha256.New, []byte(s.Identity.Token))
	m.Write([]byte("delidev/account-oauth/" + kind + "/v1\x00" + string(s.Identity.ServerID) + "\x00" + string(id) + "\x00"))
	m.Write(value)
	return hex.EncodeToString(m.Sum(nil))
}

// accountGate owns ephemeral state. Initialization never recovers a verifier or
// sends HTTP; a new lifetime only interrupts prior private dispatch authority.
func (s *Service) initializeOAuthLocked(ctx context.Context) error {
	if s.oauthGeneration != "" {
		return nil
	}
	generation := domain.NewID()
	owner := domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice})
	_, err := s.Store.Mutate(owner, domain.NewID(), "oauth.new-lifetime", generation, func(tx *store.Tx) (any, error) {
		return nil, tx.InterruptAccountOAuth(generation, time.Now().UTC().Truncate(time.Millisecond))
	})
	if err != nil {
		return err
	}
	s.oauthGeneration = generation
	s.oauthLive = map[domain.ID]*oauthLive{}
	return nil
}
func (s *Service) initializeOAuth(ctx context.Context) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return s.initializeOAuthLocked(ctx)
}
func oauthProvider(tx *store.Tx, id domain.ID, revision uint64) error {
	if err := tx.Authorize(); err != nil {
		return err
	}
	row, err := tx.Get(domain.ProviderKind, id)
	if err != nil {
		return err
	}
	p, err := store.Decode[domain.Provider](row)
	if err != nil || row.Revision != revision || p.Validate() != nil || p.PresetID == nil || *p.PresetID != domain.PresetOpenRouter || !p.EnabledValue() || p.Endpoint != "https://openrouter.ai/api/v1" || p.Protocol != domain.OpenAIChat || p.Authentication != domain.BearerAuth {
		return domain.Fail(domain.Unsupported, "OAuth requires the enabled saved official OpenRouter preset.", "Select the original managed OpenRouter provider or use its manual API-key flow.")
	}
	return nil
}
func (s *Service) oauthRead(ctx context.Context, id domain.ID) (domain.AccountOAuthAttempt, error) {
	var a domain.AccountOAuthAttempt
	actor, err := requireOAuthActor(ctx)
	if err != nil {
		return a, err
	}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var err error
		a, err = tx.AccountOAuth(id)
		if err == nil && (a.Actor != actor || a.ServerID != s.Identity.ServerID) {
			err = domain.Fail(domain.PermissionDenied, "OAuth attempt belongs to another initiating client.", "Use its original authenticated client.")
		}
		return err
	})
	return a, err
}
func (s *Service) clearOAuthLive(id domain.ID) {
	if live := s.oauthLive[id]; live != nil {
		clear(live.verifier)
		live.authorization = ""
		delete(s.oauthLive, id)
	}
}

// This cause is issued only inside a new Start's rolled-back admission
// transaction. Replay/post-commit failures never grant permission to abandon it.
func oauthStartNotAdmitted(err error) error {
	safe := *domain.SafeError(err)
	switch safe.Code {
	case domain.Unsupported, domain.Conflict, domain.NotFound, domain.ResourceExhausted, domain.PermissionDenied:
		safe.Cause = "oauth_start_not_admitted"
	}
	return &safe
}

func (s *Service) StartAccountOAuth(ctx context.Context, req *connect.Request[pb.StartAccountOAuthRequest]) (*connect.Response[pb.StartAccountOAuthResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := requireOAuthActor(ctx)
	if err == nil {
		err = validateAccountMutation(req.Msg.Provider)
	}
	if err == nil {
		err = domain.ValidateOAuthCallback(req.Msg.CallbackUrl)
	}
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	if err = s.initializeOAuthLocked(ctx); err != nil {
		return nil, rpc.Error(err, c)
	}
	for id, live := range s.oauthLive {
		if !time.Now().Before(live.expires) {
			s.clearOAuthLive(id)
		}
	}
	m := req.Msg.Provider
	input := struct {
		Provider           domain.ID
		Revision           uint64
		Actor              domain.Principal
		CallbackCommitment string
	}{domain.ID(m.Id), m.ExpectedRevision, actor, s.oauthCommitment("callback", domain.ID(m.RequestId), []byte(req.Msg.CallbackUrl))}
	result, replayed, err := s.Store.Replay(ctx, domain.ID(m.RequestId), "oauth.start", input)
	var original domain.ID
	var live *oauthLive
	if err == nil && !replayed {
		verifierBytes := make([]byte, 32)
		if _, err = rand.Read(verifierBytes); err != nil {
			return nil, rpc.Error(domain.SafeError(err), c)
		}
		verifier := []byte(base64.RawURLEncoding.EncodeToString(verifierBytes))
		clear(verifierBytes)
		hash := sha256.Sum256(verifier)
		q := url.Values{"code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "code_challenge_method": {"S256"}, "key_label": {"DeliDev"}}
		if req.Msg.CallbackUrl != "" {
			q.Set("callback_url", req.Msg.CallbackUrl)
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		live = &oauthLive{verifier: verifier, authorization: "https://openrouter.ai/auth?" + q.Encode(), expires: now.Add(10 * time.Minute)}
		a := domain.AccountOAuthAttempt{Version: 1, ID: domain.NewID(), Revision: 1, ServerID: s.Identity.ServerID, ProviderID: input.Provider, ProviderRevision: input.Revision, Actor: actor, Generation: s.oauthGeneration, StartRequestID: domain.ID(m.RequestId), AccountID: domain.NewID(), CreateRequestID: domain.NewID(), ConnectRequestID: domain.NewID(), State: domain.OAuthAwaiting, StartedAt: now, ExpiresAt: now.Add(10 * time.Minute), UpdatedAt: now, CallbackCommitment: input.CallbackCommitment}
		result, err = s.Store.Mutate(ctx, domain.ID(m.RequestId), "oauth.start", input, func(tx *store.Tx) (any, error) {
			if err := oauthProvider(tx, input.Provider, input.Revision); err != nil {
				return nil, oauthStartNotAdmitted(err)
			}
			if err := tx.ExpireAccountOAuth(now); err != nil {
				return nil, err
			}
			n, err := tx.AccountOAuthPending()
			if err != nil {
				return nil, err
			}
			if n >= 32 {
				return nil, oauthStartNotAdmitted(domain.Fail(domain.ResourceExhausted, "The server has too many unresolved OAuth attempts.", "Cancel or reconcile original attempts before starting another."))
			}
			if err := tx.PutAccountOAuth(a, 0); err != nil {
				return nil, err
			}
			return oauthReceipt{a.ID}, nil
		})
		if err == nil {
			original = a.ID
			s.oauthLive[a.ID] = live
		} else {
			clear(live.verifier)
		}
	}
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	var receipt oauthReceipt
	if domain.Decode(result.Data, &receipt) != nil {
		return nil, rpc.Error(oauthProblem(), c)
	}
	if original != "" && original != receipt.AttemptID {
		return nil, rpc.Error(oauthProblem(), c)
	}
	a, err := s.oauthRead(ctx, receipt.AttemptID)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	// Replay preserves the original attempt but cannot recover browser authority
	// after its provider was edited or disabled.
	if a.State == domain.OAuthAwaiting {
		if providerErr := s.Store.Read(ctx, func(tx *store.Tx) error { return oauthProvider(tx, a.ProviderID, a.ProviderRevision) }); providerErr != nil {
			switch domain.SafeError(providerErr).Code {
			case domain.Unsupported, domain.NotFound, domain.PermissionDenied:
				s.clearOAuthLive(a.ID)
				// Admission already committed. Return its original identity so
				// a lost Start reply cannot strand explicit cancellation when
				// the provider changes; never recreate browser/exchange authority.
				a, err = s.oauthUpdate(ctx, a, domain.OAuthInterrupted, func(v *domain.AccountOAuthAttempt) {
					v.Problem = domain.Fail(domain.Conflict, "The provider changed before authorization completed.", "Cancel this original attempt and refresh the saved provider.")
				})
				if err != nil {
					return nil, rpc.Error(err, c)
				}
			default:
				return nil, rpc.Error(providerErr, c)
			}
		}
	}
	response := &pb.StartAccountOAuthResponse{Attempt: oauthProjection(a), RequestId: m.RequestId, Replayed: result.Replayed}
	if a.State == domain.OAuthAwaiting && a.Generation == s.oauthGeneration && time.Now().Before(a.ExpiresAt) {
		if live := s.oauthLive[a.ID]; live != nil {
			response.AuthorizationUrl = live.authorization
		}
	}
	s.logger.InfoContext(ctx, "account_oauth_started", "attempt_id", a.ID, "state", a.State, "replayed", result.Replayed, "correlation_id", c)
	r := connect.NewResponse(response)
	rpc.CopyCorrelation(r, req.Header())
	return r, nil
}
func oauthProjection(a domain.AccountOAuthAttempt) *pb.AccountOAuthAttempt {
	states := map[domain.AccountOAuthState]pb.AccountOAuthState{domain.OAuthAwaiting: pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_AWAITING_AUTHORIZATION, domain.OAuthExchanging: pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_EXCHANGING, domain.OAuthSaving: pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_SAVING, domain.OAuthConnected: pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_CONNECTED, domain.OAuthCanceled: pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_CANCELED, domain.OAuthExpired: pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_EXPIRED, domain.OAuthFailed: pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_FAILED, domain.OAuthInterrupted: pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_INTERRUPTED, domain.OAuthRecovery: pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_RECOVERY_REQUIRED}
	state := states[a.State]
	if a.State == domain.OAuthAwaiting && !time.Now().Before(a.ExpiresAt) {
		state = pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_EXPIRED
	}
	r := &pb.AccountOAuthAttempt{Id: string(a.ID), Revision: a.Revision, State: state, ExpiresAt: a.ExpiresAt.Format(time.RFC3339Nano), ProviderId: string(a.ProviderID)}
	if a.Problem != nil {
		r.Problem = &pb.ErrorDetail{Code: string(a.Problem.Code), Guidance: a.Problem.Guidance, Cause: a.Problem.Cause}
	}
	return r
}
func (s *Service) oauthResponse(ctx context.Context, a domain.AccountOAuthAttempt, request string, replayed bool) (*pb.CompleteAccountOAuthResponse, error) {
	r := &pb.CompleteAccountOAuthResponse{Attempt: oauthProjection(a), RequestId: request, Replayed: replayed}
	if a.State == domain.OAuthConnected || a.StagingClaimed {
		row, err := s.accountRecord(ctx, a.AccountID)
		if err == nil {
			r.Account = rpc.Resource(row)
		} else if domain.SafeError(err).Code != domain.NotFound {
			return nil, err
		}
	}
	return r, nil
}
func (s *Service) GetAccountOAuthStatus(ctx context.Context, req *connect.Request[pb.GetAccountOAuthStatusRequest]) (*connect.Response[pb.GetAccountOAuthStatusResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	if err := domain.ID(req.Msg.AttemptId).Validate(); err != nil {
		return nil, rpc.Error(err, c)
	}
	if _, err := requireOAuthActor(ctx); err != nil {
		return nil, rpc.Error(err, c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	if err = s.initializeOAuthLocked(ctx); err != nil {
		return nil, rpc.Error(err, c)
	}
	a, err := s.oauthRead(ctx, domain.ID(req.Msg.AttemptId))
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if a.State != domain.OAuthAwaiting || !time.Now().Before(a.ExpiresAt) {
		s.clearOAuthLive(a.ID)
	}
	value, err := s.oauthResponse(ctx, a, "", false)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	r := connect.NewResponse(&pb.GetAccountOAuthStatusResponse{Attempt: value.Attempt, Account: value.Account, RequestId: value.RequestId, Replayed: value.Replayed})
	rpc.CopyCorrelation(r, req.Header())
	return r, nil
}

// This writes only sanitized outcome metadata with server authority even when
// the initiating RPC was canceled/revoked. It cannot create/connect an account.
func (s *Service) oauthRecoveryLocked(a domain.AccountOAuthAttempt, problem *domain.Error) {
	ctx, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 5*time.Second)
	defer cancel()
	_, err := s.Store.Mutate(ctx, domain.NewID(), "oauth.recovery", a.ID, func(tx *store.Tx) (any, error) {
		current, err := tx.AccountOAuth(a.ID)
		if err != nil {
			return nil, err
		}
		if current.State == domain.OAuthConnected || current.State == domain.OAuthCanceled {
			return nil, nil
		}
		rev := current.Revision
		current.Revision++
		current.State = domain.OAuthRecovery
		current.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		current.Problem = problem
		return nil, tx.PutAccountOAuth(current, rev)
	})
	s.clearOAuthLive(a.ID)
	s.logger.WarnContext(ctx, "account_oauth_recovery_required", "attempt_id", a.ID, "code", domain.SafeError(err).Code)
}

type oauthCompleteInput struct {
	Attempt        domain.ID
	Revision       uint64
	Actor          domain.Principal
	CodeCommitment string
}

func oauthSameActor(a domain.AccountOAuthAttempt, actor domain.Principal, server domain.ID) error {
	if a.Actor != actor || a.ServerID != server {
		return domain.Fail(domain.PermissionDenied, "OAuth attempt belongs to another initiating client.", "Use the original authenticated client.")
	}
	return nil
}

func (s *Service) oauthLocalAuthority(tx *store.Tx, a domain.AccountOAuthAttempt, actor domain.Principal) error {
	if err := tx.Authorize(); err != nil {
		return err
	}
	if err := oauthSameActor(a, actor, s.Identity.ServerID); err != nil {
		return err
	}
	if a.State != domain.OAuthSaving && a.State != domain.OAuthRecovery {
		return oauthProblem()
	}
	return oauthProvider(tx, a.ProviderID, a.ProviderRevision)
}

func (s *Service) oauthUpdate(ctx context.Context, a domain.AccountOAuthAttempt, state domain.AccountOAuthState, apply func(*domain.AccountOAuthAttempt)) (domain.AccountOAuthAttempt, error) {
	_, err := s.Store.Mutate(ctx, domain.NewID(), "oauth.local-stage", struct {
		ID       domain.ID
		Revision uint64
		State    domain.AccountOAuthState
	}{a.ID, a.Revision, state}, func(tx *store.Tx) (any, error) {
		if err := tx.Authorize(); err != nil {
			return nil, err
		}
		current, err := tx.AccountOAuth(a.ID)
		if err != nil {
			return nil, err
		}
		if current.Revision != a.Revision {
			return nil, domain.Fail(domain.Conflict, "OAuth ownership changed.", "Read the original attempt.")
		}
		current.Revision++
		current.State = state
		current.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		if apply != nil {
			apply(&current)
		}
		if err := tx.PutAccountOAuth(current, a.Revision); err != nil {
			return nil, err
		}
		a = current
		return oauthReceipt{current.ID}, nil
	})
	return a, err
}

// The original protected reference is the sole recovery authority. Neither a
// supplied code nor a new request can replace its missing/tombstoned payload.
func (s *Service) oauthFinishLocalLocked(ctx context.Context, a domain.AccountOAuthAttempt, actor domain.Principal, key []byte) (domain.AccountOAuthAttempt, error) {
	err := s.Store.Read(ctx, func(tx *store.Tx) error { return s.oauthLocalAuthority(tx, a, actor) })
	if err != nil {
		return a, err
	}
	if domain.ValidateAPIKey(key, false) != nil {
		return a, oauthProblem()
	}
	if !a.Sealed {
		a, err = s.oauthUpdate(ctx, a, domain.OAuthSaving, func(v *domain.AccountOAuthAttempt) { v.Sealed = true; v.Problem = nil })
		if err != nil {
			return a, err
		}
	}
	account := domain.Account{Alias: "OpenRouter", ProviderID: a.ProviderID, Type: domain.APIAccount, Enabled: true, RecoveryNotifications: true, Health: domain.AccountDisconnected}
	// Reuse ordinary configuration admission/validation and the reserved exact
	// creation receipt. A deleted or edited account never becomes a fresh create.
	raw, _ := json.Marshal(account)
	_, err = SaveConfiguration(ctx, s.Store, ConfigurationMutation{RequestID: a.CreateRequestID, ID: a.AccountID, Kind: domain.AccountKind, Document: raw})
	if err != nil {
		return a, err
	}
	input := connectAccountInput{ID: a.AccountID, Revision: 1, Commitment: s.accountCommitment(a.ConnectRequestID, key)}
	_, err = s.Store.Mutate(ctx, a.ConnectRequestID, "account.connect", input, func(tx *store.Tx) (any, error) {
		current, err := tx.AccountOAuth(a.ID)
		if err != nil {
			return nil, err
		}
		if err := s.oauthLocalAuthority(tx, current, actor); err != nil {
			return nil, err
		}
		if current.CompletionRequestID != a.CompletionRequestID || !current.Sealed || !current.StagingClaimed {
			return nil, oauthProblem()
		}
		result, err := commitAccountConnection(tx, input, a.ConnectRequestID)
		if err != nil {
			return nil, err
		}
		rev := current.Revision
		current.Revision++
		current.State = domain.OAuthConnected
		current.CleanupPending = false
		current.Problem = nil
		current.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		if err := tx.PutAccountOAuth(current, rev); err != nil {
			return nil, err
		}
		return result, nil
	})
	if err != nil {
		return a, err
	}
	return s.oauthRead(ctx, a.ID)
}

func (s *Service) CompleteAccountOAuth(ctx context.Context, req *connect.Request[pb.CompleteAccountOAuthRequest]) (*connect.Response[pb.CompleteAccountOAuthResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	defer clear(req.Msg.AuthorizationCode)
	actor, err := requireOAuthActor(ctx)
	if err == nil {
		err = validateAccountMutation(req.Msg.Mutation)
	}
	if err == nil && len(req.Msg.AuthorizationCode) > 0 {
		err = domain.ValidateOAuthCode(req.Msg.AuthorizationCode)
	}
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	if err = s.initializeOAuthLocked(ctx); err != nil {
		return nil, rpc.Error(err, c)
	}
	m := req.Msg.Mutation
	a, err := s.oauthRead(ctx, domain.ID(m.Id))
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	commitment := a.CodeCommitment
	if len(req.Msg.AuthorizationCode) > 0 {
		commitment = s.oauthCommitment("code", domain.ID(m.RequestId), req.Msg.AuthorizationCode)
	}
	input := oauthCompleteInput{a.ID, m.ExpectedRevision, actor, commitment}
	result, replayed, err := s.Store.Replay(ctx, domain.ID(m.RequestId), "oauth.complete", input)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	respond := func(current domain.AccountOAuthAttempt, replay bool) (*connect.Response[pb.CompleteAccountOAuthResponse], error) {
		value, err := s.oauthResponse(ctx, current, m.RequestId, replay)
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		r := connect.NewResponse(value)
		rpc.CopyCorrelation(r, req.Header())
		return r, nil
	}
	if replayed {
		var accepted oauthReceipt
		if domain.Decode(result.Data, &accepted) != nil || accepted.AttemptID != a.ID || a.CompletionRequestID != domain.ID(m.RequestId) || a.CompletionRevision != m.ExpectedRevision {
			return nil, rpc.Error(oauthProblem(), c)
		}
		if a.State == domain.OAuthExchanging {
			check, active := s.accountChecks[a.AccountID][a.CompletionRequestID]
			if a.Generation != s.oauthGeneration || !active || check.operation != oauthInspection {
				// A committed dispatch claim without its original live owner is
				// uncertain even in this process. Observation cannot dispatch it.
				s.oauthRecoveryLocked(a, oauthProblem())
				var err error
				a, err = s.oauthRead(ctx, a.ID)
				if err != nil {
					return nil, rpc.Error(err, c)
				}
			}
		}
		if a.State == domain.OAuthConnected || a.State == domain.OAuthCanceled || a.State == domain.OAuthExchanging && a.Generation == s.oauthGeneration || !a.StagingClaimed {
			return respond(a, true)
		}
		if a.State != domain.OAuthSaving && a.State != domain.OAuthRecovery {
			return respond(a, true)
		}
		if err := s.Store.Read(ctx, func(tx *store.Tx) error { return s.oauthLocalAuthority(tx, a, actor) }); err != nil {
			return nil, rpc.Error(err, c)
		}
		vault, err := s.secrets()
		var key []byte
		if err == nil {
			key, err = vault.Get(ctx, credentials.Ref{Owner: a.AccountID, ID: a.ConnectRequestID, Purpose: credentials.AccountAPI})
		}
		defer clear(key)
		if err == nil {
			a, err = s.oauthFinishLocalLocked(ctx, a, actor, key)
		}
		if err != nil {
			s.oauthRecoveryLocked(a, oauthProblem())
			a, _ = s.oauthRead(ctx, a.ID)
		}
		return respond(a, true)
	}
	if len(req.Msg.AuthorizationCode) == 0 || a.State != domain.OAuthAwaiting || a.Revision != m.ExpectedRevision || a.Generation != s.oauthGeneration || !time.Now().Before(a.ExpiresAt) || s.oauthLive[a.ID] == nil {
		return nil, rpc.Error(domain.Fail(domain.Conflict, "The original live authorization is unavailable.", "Read the original attempt. Only an already claimed original completion permits code-free local recovery."), c)
	}
	checkCtx, finish, err := s.startAccountCheck(ctx, a.AccountID, domain.ID(m.RequestId), a.ProviderID, oauthInspection)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer func() {
		if locked {
			unlock()
			locked = false
		}
		finish()
	}()
	result, err = s.Store.Mutate(checkCtx, domain.ID(m.RequestId), "oauth.complete", input, func(tx *store.Tx) (any, error) {
		if err := oauthProvider(tx, a.ProviderID, a.ProviderRevision); err != nil {
			return nil, err
		}
		current, err := tx.AccountOAuth(a.ID)
		if err != nil {
			return nil, err
		}
		if current.Revision != a.Revision || current.State != domain.OAuthAwaiting || !time.Now().Before(current.ExpiresAt) {
			return nil, domain.Fail(domain.Conflict, "Authorization is no longer awaiting completion.", "Read the original attempt.")
		}
		current.Revision++
		current.State = domain.OAuthExchanging
		current.CompletionRequestID = domain.ID(m.RequestId)
		current.CompletionRevision = m.ExpectedRevision
		current.CodeCommitment = commitment
		current.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		if err := tx.PutAccountOAuth(current, a.Revision); err != nil {
			return nil, err
		}
		a = current
		return oauthReceipt{a.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if result.Replayed {
		return nil, rpc.Error(oauthProblem(), c)
	}
	verifier := bytes.Clone(s.oauthLive[a.ID].verifier)
	s.clearOAuthLive(a.ID)
	defer clear(verifier)
	exchange := s.oauthExchange
	if exchange == nil {
		exchange = ownedOAuthExchange{route: s.outboundResolver()}
	}
	unlock()
	locked = false
	s.logger.InfoContext(ctx, "account_oauth_exchange_claimed", "attempt_id", a.ID, "request_id", m.RequestId, "correlation_id", c)
	key, exchangeErr := exchange.Exchange(checkCtx, req.Msg.AuthorizationCode, verifier)
	clear(verifier)
	clear(req.Msg.AuthorizationCode)
	defer clear(key)
	// A canceled HTTP/RPC still needs bounded durable uncertainty bookkeeping.
	// Original actor authority is independently checked before protected writes.
	settleCtx, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), actor), 5*time.Second)
	defer cancel()
	unlock, err = s.lockAccounts(settleCtx)
	if err != nil {
		return nil, rpc.Error(oauthProblem(), c)
	}
	locked = true
	a, err = s.oauthRead(settleCtx, a.ID)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if a.State == domain.OAuthCanceled {
		return respond(a, false)
	}
	if exchangeErr != nil || domain.ValidateAPIKey(key, false) != nil {
		s.oauthRecoveryLocked(a, oauthProblem())
		a, err = s.oauthRead(settleCtx, a.ID)
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		return respond(a, false)
	}
	err = s.Store.Read(settleCtx, func(tx *store.Tx) error {
		if a.State != domain.OAuthExchanging {
			return oauthProblem()
		}
		return oauthProvider(tx, a.ProviderID, a.ProviderRevision)
	})
	if err == nil {
		a, err = s.oauthUpdate(settleCtx, a, domain.OAuthSaving, func(v *domain.AccountOAuthAttempt) { v.StagingClaimed = true; v.CleanupPending = true })
	}
	if err == nil {
		vault, e := s.secrets()
		err = e
		if err == nil {
			_, err = vault.Put(settleCtx, credentials.Ref{Owner: a.AccountID, ID: a.ConnectRequestID, Purpose: credentials.AccountAPI}, key)
		}
	}
	if err == nil {
		a, err = s.oauthFinishLocalLocked(settleCtx, a, actor, key)
	}
	clear(key)
	if err != nil {
		s.oauthRecoveryLocked(a, oauthProblem())
		a, err = s.oauthRead(settleCtx, a.ID)
	}
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(settleCtx, "account_oauth_completion_finished", "attempt_id", a.ID, "state", a.State, "correlation_id", c)
	return respond(a, false)
}

func (s *Service) CancelAccountOAuth(ctx context.Context, req *connect.Request[pb.CancelAccountOAuthRequest]) (*connect.Response[pb.CancelAccountOAuthResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := requireOAuthActor(ctx)
	if err == nil {
		err = validateAccountMutation(req.Msg.Mutation)
	}
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	if err = s.initializeOAuthLocked(ctx); err != nil {
		return nil, rpc.Error(err, c)
	}
	m := req.Msg.Mutation
	input := struct {
		Attempt  domain.ID
		Revision uint64
		Actor    domain.Principal
	}{domain.ID(m.Id), m.ExpectedRevision, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "oauth.cancel", input, func(tx *store.Tx) (any, error) {
		if err := tx.Authorize(); err != nil {
			return nil, err
		}
		a, err := tx.AccountOAuth(input.Attempt)
		if err != nil {
			return nil, err
		}
		if err = oauthSameActor(a, actor, s.Identity.ServerID); err != nil {
			return nil, err
		}
		// A connection committed first cannot be undone by this cancellation.
		if a.State == domain.OAuthConnected {
			return oauthReceipt{a.ID}, nil
		}
		if a.Revision != input.Revision {
			return nil, domain.Fail(domain.Conflict, "OAuth attempt revision changed.", "Read its current state before cancellation.")
		}
		rev := a.Revision
		a.Revision++
		a.State = domain.OAuthCanceled
		a.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		if a.CompletionRequestID != "" {
			a.Problem = oauthProblem()
		}
		if a.StagingClaimed {
			a.CleanupPending = true
		}
		if err := tx.PutAccountOAuth(a, rev); err != nil {
			return nil, err
		}
		return oauthReceipt{a.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	a, err := s.oauthRead(ctx, input.Attempt)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.clearOAuthLive(a.ID)
	if a.State == domain.OAuthCanceled {
		s.cancelAccountChecks(a.AccountID)
		if a.CleanupPending {
			vault, e := s.secrets()
			err = e
			if err == nil {
				err = vault.Delete(ctx, credentials.Ref{Owner: a.AccountID, ID: a.ConnectRequestID, Purpose: credentials.AccountAPI})
			}
			if err == nil {
				a, err = s.oauthUpdate(ctx, a, domain.OAuthCanceled, func(v *domain.AccountOAuthAttempt) { v.CleanupPending = false })
			}
			if err != nil {
				return nil, rpc.Error(oauthProblem(), c)
			}
		}
	}
	value, err := s.oauthResponse(ctx, a, m.RequestId, result.Replayed)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "account_oauth_canceled", "attempt_id", a.ID, "state", a.State, "replayed", result.Replayed, "correlation_id", c)
	r := connect.NewResponse(&pb.CancelAccountOAuthResponse{Attempt: value.Attempt, Account: value.Account, RequestId: value.RequestId, Replayed: value.Replayed})
	rpc.CopyCorrelation(r, req.Header())
	return r, nil
}
