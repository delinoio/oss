// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/store"
	sessionauth "github.com/delinoio/oss/servers/aiprouter/internal/auth"
)

type persistentAuth struct{ s *store.Store }

func (p persistentAuth) SaveLogin(ctx context.Context, state string, l sessionauth.LoginState) error {
	payload, e := json.Marshal(struct{ Verifier, ReturnTo string }{l.Verifier, l.ReturnTo})
	if e != nil {
		return e
	}
	return p.s.SaveLoginState(ctx, store.LoginState{State: state, Binding: l.Binding, Nonce: l.Nonce, Verifier: string(payload)})
}
func (p persistentAuth) ConsumeLogin(ctx context.Context, state, binding string) (sessionauth.LoginState, error) {
	l, e := p.s.ConsumeLoginState(ctx, state, binding)
	if e != nil {
		if errors.Is(e, store.ErrNotFound) || errors.Is(e, store.ErrExpired) {
			return sessionauth.LoginState{}, sessionauth.ErrAuthentication
		}
		return sessionauth.LoginState{}, e
	}
	var payload struct{ Verifier, ReturnTo string }
	if json.Unmarshal([]byte(l.Verifier), &payload) != nil || payload.Verifier == "" {
		return sessionauth.LoginState{}, sessionauth.ErrAuthentication
	}
	return sessionauth.LoginState{Verifier: payload.Verifier, ReturnTo: payload.ReturnTo, Nonce: l.Nonce, Binding: l.Binding}, nil
}
func (p persistentAuth) Login(ctx context.Context, issuer, subject, email string) (string, error) {
	u, e := p.s.RegisterIdentity(ctx, issuer, subject, email, true)
	if e != nil {
		return "", e
	}
	return p.s.CreateSession(ctx, u.ID)
}
func (p persistentAuth) Session(ctx context.Context, raw string) (sessionauth.Identity, error) {
	u, e := p.s.AuthenticateSession(ctx, raw)
	return sessionauth.Identity{UserID: u.ID, Issuer: u.Issuer, Subject: u.Subject, Email: u.Email}, e
}
func (p persistentAuth) Logout(ctx context.Context, raw string) error { return p.s.Logout(ctx, raw) }
