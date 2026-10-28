// SPDX-License-Identifier: Apache-2.0
package service

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	"regexp"
)

var contextSessionPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,512}$`)

func contextError(e error) error {
	if errors.Is(e, store.ErrNotFound) || errors.Is(e, store.ErrForbidden) || errors.Is(e, store.ErrExpired) || errors.Is(e, store.ErrConflict) || errors.Is(e, store.ErrBusy) || errors.Is(e, store.ErrReauthentication) {
		return &inference.Error{Status: 409, Code: "context_account_unavailable"}
	}
	return inferenceError(e)
}
func (m *management) ConfigureClientContext(ctx context.Context, r *connect.Request[v1.ConfigureClientContextRequest]) (*connect.Response[v1.ConfigureClientContextResponse], error) {
	raw, _ := ctx.Value(bearerKey{}).(string)
	return connect.NewResponse(&v1.ConfigureClientContextResponse{}), rpcError(m.store.ConfigureClientContext(ctx, raw, r.Msg.Enabled))
}
func (m *management) BindClientContextSession(ctx context.Context, r *connect.Request[v1.BindClientContextSessionRequest]) (*connect.Response[v1.BindClientContextSessionResponse], error) {
	if !contextSessionPattern.MatchString(r.Msg.SessionId) {
		return nil, invalid("invalid context session")
	}
	raw, _ := ctx.Value(bearerKey{}).(string)
	b, e := m.store.BindClientContext(ctx, raw, r.Msg.SessionId)
	return connect.NewResponse(&v1.BindClientContextSessionResponse{ExpiresAt: timestamp(b.ExpiresAt)}), rpcError(e)
}
func (a *inferenceAdapter) captureContext(ctx context.Context, p inference.Principal, t inference.Target, c inference.Credential, status int) error {
	session := inference.AffinitySession(ctx)
	if status < 200 || status >= 300 || inference.InboundWire(ctx) != inference.Responses || !contextSessionPattern.MatchString(session) {
		return nil
	}
	a.mu.Lock()
	known, ok := a.connections[c.AccountID]
	a.mu.Unlock()
	if !ok || known.Metadata == nil || known.Metadata.ProviderId != "openai" || t.Auth != inference.OAuth {
		return nil
	}
	d, version, e := a.providerVersion(ctx, scopeOf(p), c.AccountID)
	if e != nil {
		return e
	}
	if d.Metadata.ProviderId != "openai" || t.Auth != inference.OAuth {
		return nil
	}
	l, e := a.current(p)
	if e != nil {
		return e
	}
	data, e := json.Marshal(t)
	if e != nil {
		return e
	}
	return inferenceError(a.s.CaptureClientContext(ctx, l, session, c.AccountID, version, data))
}
func (a *inferenceAdapter) ResolveContext(ctx context.Context, p inference.Principal, session string) (inference.ContextBinding, error) {
	var out inference.ContextBinding
	l, e := a.current(p)
	if e != nil {
		return out, e
	}
	b, e := a.s.ResolveClientContext(ctx, l, session)
	if e != nil {
		return out, contextError(e)
	}
	if json.Unmarshal(b.Data, &out.Target) != nil {
		return out, inference.ErrUnavailable
	}
	// Rebuild non-serializable compiled policy from the exact bound credential
	// generation, never from a new route/pool selection.
	d, version, e := a.providerVersion(ctx, scopeOf(p), b.AccountID)
	if e != nil {
		return out, contextError(e)
	}
	if version != b.CredentialVersion {
		return out, contextError(store.ErrConflict)
	}
	target, e := selectedTargetForContext(ctx, d, out.Target)
	if e != nil || target.BaseURL != out.Target.BaseURL || target.Model != out.Target.Model || target.Wire != out.Target.Wire || target.Auth != out.Target.Auth {
		return out, contextError(store.ErrConflict)
	}
	out.Target = target
	c, e := a.captureCredential(ctx, p, out.Target, b.AccountID)
	if e != nil {
		return out, contextError(e)
	}
	out.Credential = c
	out.Receipt = b.Receipt
	return out, nil
}
func (a *inferenceAdapter) ValidateContext(ctx context.Context, p inference.Principal, session string, b inference.ContextBinding) error {
	l, e := a.current(p)
	if e != nil {
		return e
	}
	current, e := a.s.ResolveClientContext(ctx, l, session)
	if e != nil {
		return contextError(e)
	}
	if current.Receipt != b.Receipt || current.AccountID != b.Credential.AccountID {
		return contextError(store.ErrConflict)
	}
	a.mu.Lock()
	a.contextSession = session
	a.contextReceipt = b.Receipt
	a.mu.Unlock()
	return nil
}

var _ inference.ContextResolver = (*inferenceAdapter)(nil)
