// SPDX-License-Identifier: Apache-2.0
package devin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

// State limits are enforced by the encrypted server/store backend per owner and
// namespace. The adapter additionally validates each entry and its expiry.
const StateTTL = 24 * time.Hour
const StateMaxEntries = 256
const StateMaxBytes = 256 << 10
const StateMaxEntryBytes = 4096

type attemptKey struct{}
type attemptState struct {
	mu                 sync.Mutex
	identity           string
	options            Options
	response           *ResponseContext
	cascade            string
	prepared, complete bool
	prepareDone        chan struct{}
	prepareErr         error
}

var (
	_ inference.ContextPreparer   = (*Adapter)(nil)
	_ inference.ContextualAdapter = (*Adapter)(nil)
	_ inference.PreparingAdapter  = (*Adapter)(nil)
	_ inference.StatefulAdapter   = (*Adapter)(nil)
)

func (*Adapter) PrepareContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, attemptKey{}, &attemptState{})
}
func attempt(ctx context.Context) *attemptState {
	s, _ := ctx.Value(attemptKey{}).(*attemptState)
	return s
}
func identity(in inference.AdapterInput) string {
	data, _ := json.Marshal([]any{in.Principal.OrganizationID, in.Principal.UserID, in.Principal.KeyID, in.Principal.MembershipGeneration, in.Target.Provider, in.Target.BaseURL, in.Target.Model, in.Credential.AccountID, in.Credential.Secret})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// bind is called with the attempt lock held. Reusing a context across an account
// rotation cannot reuse the old account's catalog, aliases or cascade.
func (s *attemptState) bind(in inference.AdapterInput) error {
	key := identity(in)
	if s.identity != "" && s.identity != key {
		return inference.ErrContinuation
	}
	s.identity = key
	return nil
}

func (a *Adapter) DecodeWithInput(ctx context.Context, in inference.AdapterInput, response *http.Response) (*http.Response, error) {
	s := attempt(ctx)
	if s == nil {
		// Standalone callers still receive request-scoped restoration. Core always
		// calls PrepareContext, so this branch never creates a process-wide cache.
		_, state, err := a.BuildPrepared(ctx, in, Options{})
		if err != nil {
			return nil, err
		}
		return a.DecodePrepared(ctx, response, state)
	}
	s.mu.Lock()
	err := s.bind(in)
	state := s.response
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if in.Operation == inference.Generate && state == nil {
		return nil, &inference.Error{Status: 502, Code: "devin_missing_response_context"}
	}
	return a.DecodePrepared(ctx, response, state)
}

// Prepare makes at most two unary preflight sends. Only the supplied executor
// may send them; its authorization, origin and budget errors always propagate.
func (a *Adapter) Prepare(ctx context.Context, in inference.AdapterInput, x inference.AdapterExecutor) (result json.RawMessage, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if in.Operation != inference.Generate {
		return in.Body, nil
	}
	s := attempt(ctx)
	if s == nil {
		return nil, &inference.Error{Status: 500, Code: "devin_attempt_context_required"}
	}
	s.mu.Lock()
	if err := s.bind(in); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if s.prepared {
		done := s.prepareDone
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-done:
		}
		s.mu.Lock()
		err := s.prepareErr
		s.mu.Unlock()
		return in.Body, err
	}
	// Latch before I/O: a repeated Prepare on this physical attempt cannot send
	// another unaccounted preflight pair after an error or concurrent invocation.
	s.prepared = true
	s.prepareDone = make(chan struct{})
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.prepareErr = resultErr; close(s.prepareDone); s.mu.Unlock() }()
	if _, _, err := a.BuildPrepared(ctx, in, Options{}); err != nil {
		return nil, err
	}
	base, err := baseURL(in.Target.BaseURL)
	if err != nil {
		return nil, err
	}
	meta, err := metadata(in.Credential.Secret, false)
	if err != nil {
		return nil, err
	}
	jwtBody, ok, err := preflight(ctx, x, base+"/exa.auth_pb.AuthService/GetUserJwt", blob(nil, 1, meta), 30*time.Second, 64<<10)
	if err != nil {
		return nil, err
	}
	if !ok {
		return in.Body, nil
	}
	jwt, err := parseJWT(jwtBody)
	if err != nil {
		return in.Body, nil
	}
	meta = textField(meta, 21, jwt)
	body, ok, err := preflight(ctx, x, base+"/exa.api_server_pb.ApiServerService/GetCascadeModelConfigs", blob(nil, 1, meta), 10*time.Second, maxFrame)
	if err != nil {
		return nil, err
	}
	if !ok {
		return in.Body, nil
	}
	catalog, err := ParseCatalog(body)
	if err != nil {
		return in.Body, nil
	}
	s.mu.Lock()
	s.options.Catalog = catalog
	s.mu.Unlock()
	return in.Body, nil
}

func preflight(ctx context.Context, x inference.AdapterExecutor, url string, body []byte, timeout time.Duration, limit int64) ([]byte, bool, error) {
	if x == nil {
		return nil, false, &inference.Error{Status: 500, Code: "devin_executor_required"}
	}
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	response, err := x.Do(bounded, inference.AdapterRequest{Method: http.MethodPost, URL: url, Headers: http.Header{"Content-Type": {"application/proto"}, "Connect-Protocol-Version": {"1"}}, Body: body, AttemptTimeout: timeout})
	if err != nil {
		return nil, false, err
	}
	if response == nil || response.Body == nil {
		return nil, false, errWire
	}
	defer response.Body.Close()
	stop := context.AfterFunc(bounded, func() { _ = response.Body.Close() })
	defer stop()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, false, nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	if err != nil || bounded.Err() != nil || int64(len(data)) > limit {
		return nil, false, nil
	}
	return data, true, nil
}

var mintedJWT = regexp.MustCompile(`^eyJ[A-Za-z0-9_-]{10,}={0,2}\.[A-Za-z0-9_-]+={0,2}\.[A-Za-z0-9_-]+={0,2}$`)

func parseJWT(b []byte) (string, error) {
	jwt := ""
	err := walk(b, func(f field) error {
		if f.num == 1 && f.wire == 2 && mintedJWT.Match(f.b) && jwt == "" {
			jwt = string(f.b)
		}
		return nil
	})
	if err != nil || jwt == "" {
		return "", errWire
	}
	return jwt, nil
}

type savedState struct {
	Version   int       `json:"version"`
	Identity  string    `json:"identity"`
	Cascade   string    `json:"cascade"`
	ExpiresAt time.Time `json:"expires_at"`
}

var uuidShape = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)

func (a *Adapter) RestoreState(ctx context.Context, in inference.AdapterInput, store inference.AdapterStateStore) (context.Context, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if attempt(ctx) == nil {
		ctx = a.PrepareContext(ctx)
	}
	s := attempt(ctx)
	s.mu.Lock()
	err := s.bind(in)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if in.Operation != inference.Generate {
		return ctx, nil
	}
	cascade, err := uuid()
	if err != nil {
		return nil, err
	}
	if in.PreviousResponseID != "" {
		if store == nil {
			return nil, inference.ErrContinuation
		}
		data, err := store.Load(ctx, in.PreviousResponseID)
		if err != nil {
			return nil, err
		}
		var saved savedState
		if len(data) > StateMaxEntryBytes || json.Unmarshal(data, &saved) != nil || saved.Version != 1 || saved.Identity != identity(in) || !saved.ExpiresAt.After(time.Now()) || !uuidShape.MatchString(saved.Cascade) {
			return nil, inference.ErrContinuation
		}
		cascade = saved.Cascade
	}
	s.mu.Lock()
	s.cascade = cascade
	s.options.cascadeID = cascade
	s.mu.Unlock()
	return ctx, nil
}

func (*Adapter) CaptureState(ctx context.Context, in inference.AdapterInput, responseID string, store inference.AdapterStateStore) error {
	if in.Operation != inference.Generate {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s := attempt(ctx)
	if s == nil {
		return inference.ErrContinuation
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.bind(in); err != nil {
		return err
	}
	if !s.complete || s.cascade == "" || responseID == "" || store == nil {
		return inference.ErrContinuation
	}
	data, err := json.Marshal(savedState{Version: 1, Identity: s.identity, Cascade: s.cascade, ExpiresAt: time.Now().Add(StateTTL)})
	if err != nil {
		return err
	}
	if len(data) > StateMaxEntryBytes {
		return errLimit
	}
	return store.Save(ctx, responseID, data, StateTTL)
}

func markComplete(ctx context.Context) {
	if s := attempt(ctx); s != nil {
		s.mu.Lock()
		s.complete = true
		s.mu.Unlock()
	}
}

// RecoveryPolicy refuses implicit Devin POST retries. The pinned caller's stated
// reset wait defaults to zero; enabling bounded cumulative waits needs a logical
// request policy, not a stateless per-attempt delay decision.
type RecoveryPolicy struct{}

var _ inference.HTTPRecoveryPolicy = RecoveryPolicy{}

func (RecoveryPolicy) RecoverHTTP(ctx context.Context, in inference.AdapterInput, f inference.HTTPFailure) (inference.RecoveryDecision, error) {
	if err := ctx.Err(); err != nil {
		return inference.RecoveryDecision{}, err
	}
	if f.Status >= 200 && f.Status < 300 {
		return inference.RecoveryDecision{Action: inference.RecoveryDefault}, nil
	}
	status := f.Status
	if status < 400 || status > 599 {
		status = 502
	}
	code := "devin_upstream_error"
	switch status {
	case 401:
		code = "devin_unauthenticated"
	case 403:
		code = "devin_permission_denied"
	case 429:
		code = "devin_resource_exhausted"
	}
	if status == 401 && in.Target.Auth == inference.OAuth {
		return inference.RecoveryDecision{Action: inference.RecoveryRotate, Error: &inference.Error{Status: status, Code: code}}, nil
	}
	// Account rotation and explicit route fallbacks are independent of the
	// disabled same-account stated-reset wait. Let core apply those policies.
	if status == 429 || status >= 500 {
		return inference.RecoveryDecision{Action: inference.RecoveryDefault}, nil
	}
	return inference.RecoveryDecision{Action: inference.RecoveryStop, Error: &inference.Error{Status: status, Code: code}}, nil
}
