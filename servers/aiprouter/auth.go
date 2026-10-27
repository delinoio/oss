// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

var errAuthentication = errors.New("authentication required")

type Identity struct{ UserID, Issuer, Subject, Email string }
type LoginState struct{ Verifier, Nonce, Binding, ReturnTo string }
type AuthStore interface {
	SaveLogin(context.Context, string, LoginState) error
	ConsumeLogin(context.Context, string, string) (LoginState, error)
	Login(context.Context, string, string, string) (string, error)
	Session(context.Context, string) (Identity, error)
	Logout(context.Context, string) error
}
type tokenVerifier interface {
	Verify(context.Context, string) (*oidc.IDToken, error)
}
type Auth struct {
	config   Config
	store    AuthStore
	oauth    oauth2.Config
	verifier tokenVerifier
}

func newAuth(ctx context.Context, c Config, s AuthStore) (*Auth, error) {
	p, e := oidc.NewProvider(ctx, c.OIDCIssuer)
	if e != nil {
		return nil, errors.New("OIDC discovery failed")
	}
	return &Auth{config: c, store: s, oauth: oauth2.Config{ClientID: c.OIDCClientID, ClientSecret: c.OIDCClientSecret, RedirectURL: c.PublicURL + "/auth/callback", Endpoint: p.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "email", "profile"}}, verifier: p.Verifier(&oidc.Config{ClientID: c.OIDCClientID})}, nil
}
func randomToken() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func hashToken(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
func (a *Auth) cookieName() string {
	if a.secure() {
		return "__Host-aiprouter-session"
	}
	return "aiprouter-session"
}
func (a *Auth) secure() bool {
	return len(a.config.PublicURL) >= 8 && a.config.PublicURL[:8] == "https://"
}
func (a *Auth) cookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: a.secure(), SameSite: http.SameSiteLaxMode, MaxAge: age})
}
func (a *Auth) login(w http.ResponseWriter, r *http.Request) {
	state, nonce, verifier := randomToken(), randomToken(), oauth2.GenerateVerifier()
	binding := randomToken()
	if e := a.store.SaveLogin(r.Context(), hashToken(state), LoginState{Verifier: verifier, Nonce: nonce, Binding: binding, ReturnTo: safeReturnTo(r.URL.Query().Get("return_to"))}); e != nil {
		a.loginError(w, r, "authentication_unavailable")
		return
	}
	a.cookie(w, a.loginCookieName(), state+"."+binding, 600)
	http.Redirect(w, r, a.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce), oauth2.SetAuthURLParam("prompt", "login")), http.StatusFound)
}
func (a *Auth) callback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	c, e := r.Cookie(a.loginCookieName())
	state := r.URL.Query().Get("state")
	var parts []string
	if e == nil {
		parts = strings.Split(c.Value, ".")
	}
	if e != nil || len(parts) != 2 || state == "" || subtle.ConstantTimeCompare([]byte(parts[0]), []byte(state)) != 1 {
		a.loginError(w, r, "login_expired")
		return
	}
	a.cookie(w, a.loginCookieName(), "", -1)
	saved, e := a.store.ConsumeLogin(r.Context(), hashToken(state), parts[1])
	if e != nil {
		if errors.Is(e, errAuthentication) {
			a.loginError(w, r, "login_expired")
		} else {
			a.loginError(w, r, "authentication_unavailable")
		}
		return
	}
	if r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
		a.loginError(w, r, "login_denied")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	token, e := a.oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(saved.Verifier))
	if e != nil {
		a.loginError(w, r, "authentication_unavailable")
		return
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		a.loginError(w, r, "login_denied")
		return
	}
	id, e := a.verifier.Verify(ctx, raw)
	if e != nil || id.Nonce != saved.Nonce || id.Subject == "" || id.Issuer != a.config.OIDCIssuer {
		a.loginError(w, r, "login_denied")
		return
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if id.Claims(&claims) != nil {
		a.loginError(w, r, "login_denied")
		return
	}
	if claims.Email == "" {
		a.loginError(w, r, "missing_email")
		return
	}
	if !claims.EmailVerified {
		a.loginError(w, r, "unverified_email")
		return
	}
	session, e := a.store.Login(ctx, id.Issuer, id.Subject, claims.Email)
	if e != nil {
		a.loginError(w, r, "authentication_unavailable")
		return
	}
	a.cookie(w, a.cookieName(), session, 7*24*60*60)
	http.Redirect(w, r, safeReturnTo(saved.ReturnTo), http.StatusSeeOther)
}
func (a *Auth) authenticate(r *http.Request) (Identity, error) {
	c, e := r.Cookie(a.cookieName())
	if e != nil || c.Value == "" {
		return Identity{}, errAuthentication
	}
	return a.store.Session(r.Context(), c.Value)
}

// Require an exact Origin on cookie-authenticated RPCs, including reads. This also
// prevents cross-site form submissions and does not trust forwarded host headers.
func (a *Auth) sameOrigin(r *http.Request) bool {
	return r.Header.Get("Origin") == a.config.PublicURL && r.Header.Get("Sec-Fetch-Site") != "cross-site"
}
func (a *Auth) logout(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		http.Error(w, "origin rejected", 403)
		return
	}
	c, e := r.Cookie(a.cookieName())
	if e == nil {
		if e = a.store.Logout(r.Context(), c.Value); e != nil {
			http.Error(w, "logout unavailable", 503)
			return
		}
	}
	a.cookie(w, a.cookieName(), "", -1)
	w.WriteHeader(204)
}

func (a *Auth) loginCookieName() string {
	if a.secure() {
		return "__Host-aiprouter-login"
	}
	return "aiprouter-login"
}

func (a *Auth) loginError(w http.ResponseWriter, r *http.Request, code string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, "/sign-in?error="+code, http.StatusSeeOther)
}
