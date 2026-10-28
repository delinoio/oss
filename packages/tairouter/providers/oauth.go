// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"github.com/delinoio/oss/packages/tairouter/core"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const openAIClient = "app_EMoamEEZ73f0CkXaXp7hrann"
const anthropicClient = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
const xaiClient = "b1a00492-073a-47ea-816f-4c329264a828"

type oauthSpec struct {
	client, authorize, token, scope string
	json                            bool
}

func spec(p string) oauthSpec {
	switch p {
	case "google-antigravity":
		return googleSpec()
	case "openai":
		return oauthSpec{openAIClient, "https://auth.openai.com/oauth/authorize", "https://auth.openai.com/oauth/token", "openid profile email offline_access api.connectors.read api.connectors.invoke", false}
	case "anthropic", "anthropic2":
		return oauthSpec{anthropicClient, "https://claude.ai/oauth/authorize", "https://api.anthropic.com/v1/oauth/token", "org:create_api_key user:profile user:inference", true}
	case "xai":
		return oauthSpec{xaiClient, "", "", "openid profile email offline_access grok-cli:access api:access", false}
	case "kimi", "kimi-responses":
		return oauthSpec{client: "17e5f671-d194-4dfb-9706-5516cb48c098", token: "https://auth.kimi.com/api/oauth/token"}
	case "nous":
		return oauthSpec{client: "hermes-cli", token: "https://portal.nousresearch.com/api/oauth/token", scope: "inference:invoke"}
	case "github-copilot":
		return oauthSpec{client: "Iv1.b507a08c87ecfe98", token: "https://github.com/login/oauth/access_token", scope: "read:user"}
	case "meta-muse":
		return oauthSpec{client: "1031625952748946", token: "https://auth.meta.com/oidc/device/token/"}
	}
	return oauthSpec{}
}
func form(m map[string]string) url.Values {
	v := url.Values{}
	for k, s := range m {
		v.Set(k, s)
	}
	return v
}
func (a *Auth) xai(ctx context.Context, s core.Scope) (oauthSpec, error) {
	sp := spec("xai")
	m, e := a.send(ctx, s, "GET", "https://auth.x.ai/.well-known/openid-configuration", nil, nil)
	if e != nil {
		return sp, e
	}
	sp.authorize = str(m, "authorization_endpoint")
	sp.token = str(m, "token_endpoint")
	for _, ep := range []string{sp.authorize, sp.token} {
		u, e := url.Parse(ep)
		if e != nil || u.Scheme != "https" || (u.Host != "auth.x.ai" && u.Host != "accounts.x.ai") || u.User != nil || u.Fragment != "" {
			return sp, ErrResponse
		}
	}
	return sp, nil
}
func (a *Auth) Start(ctx context.Context, s core.Scope, p string, o StartOptions) (Challenge, Flow, error) {
	if !validScope(s) {
		return Challenge{}, Flow{}, ErrScope
	}
	if e := ctx.Err(); e != nil {
		return Challenge{}, Flow{}, e
	}
	methods := AuthMethods(p)
	if len(methods) == 0 {
		return Challenge{}, Flow{}, ErrUnsupported
	}
	if o.Method == "" {
		o.Method = methods[0]
	}
	if !contains(methods, o.Method) {
		return Challenge{}, Flow{}, ErrUnsupported
	}
	f := Flow{Scope: s, Provider: p, Method: o.Method, State: randomString(), Verifier: randomString(), RedirectURI: o.RedirectURI, ExpiresAt: a.now().Add(10 * time.Minute), Interval: 5 * time.Second, Metadata: map[string]string{}}
	if e := configureFlow(&f, o); e != nil {
		return Challenge{}, Flow{}, e
	}
	var ch Challenge
	var e error
	switch o.Method {
	case PKCE:
		ch, e = a.startPKCE(ctx, &f)
	case Device:
		ch, e = a.startDevice(ctx, &f, o)
	case BrowserPoll:
		ch, e = a.startBrowser(ctx, &f)
	case NativeCallback:
		ch, e = a.startNative(&f)
	case ExplicitToken:
		ch = Challenge{}
		if p == "devin" {
			ch.URL = f.Metadata["portalURL"] + "/windsurf/signin?" + url.Values{"response_type": {"token"}, "client_id": {f.Metadata["clientID"]}, "redirect_uri": {"show-auth-token"}, "state": {f.State}, "prompt": {"login"}}.Encode()
		}
	default:
		e = ErrUnsupported
	}
	if e != nil {
		return Challenge{}, Flow{}, e
	}
	f.NextPoll = a.now().Add(f.Interval)
	ch.State = f.State
	ch.Method = f.Method
	ch.ExpiresAt = f.ExpiresAt
	ch.Interval = f.Interval
	return ch, f, nil
}
func (a *Auth) startPKCE(ctx context.Context, f *Flow) (Challenge, error) {
	p := f.Provider
	sp := spec(p)
	if p == "google-antigravity" && (sp.client == "" || googleClientSecret() == "") {
		return Challenge{}, ErrUnsupported
	}
	if p == "anthropic" || p == "anthropic2" {
		if f.RedirectURI == "" {
			f.RedirectURI = "https://platform.claude.com/oauth/code/callback"
		}
	}
	if !validURL(f.RedirectURI) {
		return Challenge{}, ErrConnection
	}
	if p == "xai" {
		var e error
		sp, e = a.xai(ctx, f.Scope)
		if e != nil {
			return Challenge{}, e
		}
		f.Metadata["tokenEndpoint"] = sp.token
	}
	q := url.Values{"client_id": {sp.client}, "response_type": {"code"}, "redirect_uri": {f.RedirectURI}, "scope": {sp.scope}, "state": {f.State}, "code_challenge": {challenge(f.Verifier)}, "code_challenge_method": {"S256"}}
	switch p {
	case "google-antigravity":
		q.Set("access_type", "offline")
		q.Set("prompt", "consent select_account")
	case "openai":
		q.Set("codex_cli_simplified_flow", "true")
		q.Set("originator", "opencodex")
		q.Set("id_token_add_organizations", "true")
	case "anthropic", "anthropic2":
		q.Set("code", "true")
	case "xai":
		q.Set("nonce", randomString())
	case "orcarouter-oauth":
		sp.authorize = f.Metadata["authOrigin"] + "/auth"
		q = url.Values{"callback_url": {f.RedirectURI}, "code_challenge": {challenge(f.Verifier)}, "code_challenge_method": {"S256"}, "state": {f.State}, "app_name": {"TaiRouter"}, "scope": {"api"}}
	}
	return Challenge{URL: sp.authorize + "?" + q.Encode()}, nil
}

// Complete consumes no storage itself. The service must atomically claim the encrypted
// flow before this call and fence its result against cancellation and membership changes.
func (a *Auth) Complete(ctx context.Context, s core.Scope, f Flow, code, state string) (result Credential, err error) {
	defer func() {
		if err == nil {
			if e := a.check(ctx, s, f); e != nil {
				result = Credential{}
				err = e
			}
		}
	}()
	if e := a.check(ctx, s, f); e != nil {
		return Credential{}, e
	}
	if !equal(state, f.State) || !safeString(code) {
		return Credential{}, ErrFlow
	}
	if f.Method == NativeCallback {
		return a.completeNative(ctx, s, f, code)
	}
	if f.Method == ExplicitToken {
		if f.Provider == "devin" {
			return a.devinAt(ctx, s, code, f.Metadata["registerURL"])
		}
		return a.importToken(ctx, s, f.Provider, code)
	}
	if f.Method != PKCE {
		return Credential{}, ErrUnsupported
	}
	sp := spec(f.Provider)
	if f.Provider == "google-antigravity" && (sp.client == "" || googleClientSecret() == "") {
		return Credential{}, ErrUnsupported
	}
	m := map[string]string{"grant_type": "authorization_code", "client_id": sp.client, "code": code, "redirect_uri": f.RedirectURI, "code_verifier": f.Verifier}
	switch f.Provider {
	case "google-antigravity":
		m["client_secret"] = googleClientSecret()
	case "anthropic", "anthropic2":
		if c, st, ok := strings.Cut(code, "#"); ok {
			if !equal(st, f.State) {
				return Credential{}, ErrFlow
			}
			m["code"] = c
		}
		m["state"] = f.State
	case "xai":
		sp.token = f.Metadata["tokenEndpoint"]
		u, e := url.Parse(sp.token)
		if e != nil || u.Scheme != "https" || (u.Host != "auth.x.ai" && u.Host != "accounts.x.ai") || u.User != nil {
			return Credential{}, ErrFlow
		}
	case "orcarouter-oauth":
		d, e := a.send(ctx, s, "POST", f.Metadata["authOrigin"]+"/api/v1/auth/keys", map[string]string{"code": code, "code_verifier": f.Verifier, "code_challenge_method": "S256"}, nil)
		if e != nil {
			return Credential{}, e
		}
		key := str(d, "key")
		if !strings.HasPrefix(key, "sk-orca-") || !safeString(key) {
			return Credential{}, ErrResponse
		}
		id := scalarID(d, "user_id")
		if !safeString(id) || (str(d, "scope") != "" && str(d, "scope") != "api") {
			return Credential{}, ErrResponse
		}
		return Credential{Scope: s, Provider: f.Provider, AccessToken: key, AccountID: id, APIBaseURL: f.Metadata["apiOrigin"] + "/v1"}, nil
	}
	var body any = form(m)
	if sp.json {
		body = m
	}
	d, e := a.send(ctx, s, "POST", sp.token, body, nil)
	if e != nil {
		return Credential{}, e
	}
	if e = a.check(ctx, s, f); e != nil {
		return Credential{}, e
	}
	c, e := a.credential(s, f.Provider, d, "")
	if e == nil && f.Provider == "google-antigravity" {
		return a.googleProject(ctx, s, c)
	}
	return c, e
}
func (a *Auth) Refresh(ctx context.Context, s core.Scope, p string, c Credential) (result Credential, err error) {
	defer func() {
		if err == nil {
			if result.AccessToken != c.AccessToken {
				meta := map[string]string{}
				for k, v := range result.Metadata {
					if k != "profileBearerSHA256" && k != "profileAccountId" && k != "profileEmail" && k != "profilePlan" {
						meta[k] = v
					}
				}
				result.Metadata = meta
			}
			if ctx.Err() != nil {
				result = Credential{}
				err = ctx.Err()
			} else if c.AccountID != "" && result.AccountID != "" && c.AccountID != result.AccountID {
				result = Credential{}
				err = ErrReauthenticate
			}
		}
	}()
	if e := ctx.Err(); e != nil {
		return Credential{}, e
	}
	if !validScope(s) || s != c.Scope || p != c.Provider {
		return Credential{}, ErrScope
	}
	if p == "google-vertex" {
		if c.Google == nil {
			return Credential{}, ErrReauthenticate
		}
		return a.ExchangeVertex(ctx, s, *c.Google, "")
	}
	if p == "zed" {
		return a.PrepareZed(ctx, s, c)
	}
	if !safeString(c.RefreshToken) {
		return Credential{}, ErrReauthenticate
	}
	switch p {
	case "orcarouter-oauth", "dsh-account", "meta-muse", "devin", "zed":
		return Credential{}, ErrReauthenticate
	case "command-code":
		return a.importToken(ctx, s, p, c.RefreshToken)
	case "cursor":
		d, e := a.send(ctx, s, "POST", "https://api2.cursor.sh/auth/exchange_user_api_key", map[string]any{}, bearer(c.RefreshToken))
		if e != nil {
			return Credential{}, e
		}
		return a.cursorCredential(s, d, c.RefreshToken)
	case "github-copilot":
		grant := c.RefreshToken
		access := grant
		if strings.HasPrefix(grant, "ghr_") {
			sp := spec(p)
			d, e := a.send(ctx, s, "POST", sp.token, form(map[string]string{"client_id": sp.client, "grant_type": "refresh_token", "refresh_token": grant}), nil)
			if e != nil {
				return Credential{}, e
			}
			access = str(d, "access_token")
			if str(d, "refresh_token") != "" {
				grant = str(d, "refresh_token")
			}
		}
		return a.copilot(ctx, s, access, grant)
	case "kiro":
		return a.refreshKiro(ctx, s, c)
	case "workbuddy-ai":
		return a.refreshWorkBuddy(ctx, s, c)
	}
	sp := spec(p)
	if p == "google-antigravity" && (sp.client == "" || googleClientSecret() == "") {
		return Credential{}, ErrUnsupported
	}
	if sp.token == "" && p != "xai" {
		return Credential{}, ErrUnsupported
	}
	if p == "xai" {
		var e error
		sp, e = a.xai(ctx, s)
		if e != nil {
			return Credential{}, e
		}
	}
	m := map[string]string{"client_id": sp.client, "grant_type": "refresh_token", "refresh_token": c.RefreshToken}
	h := http.Header{}
	if p == "google-antigravity" {
		m["client_secret"] = googleClientSecret()
	}
	if p == "nous" {
		delete(m, "refresh_token")
		h.Set("x-nous-refresh-token", c.RefreshToken)
	}
	var body any = form(m)
	if sp.json {
		body = m
	}
	d, e := a.send(ctx, s, "POST", sp.token, body, h)
	if e != nil {
		return Credential{}, e
	}
	next, e := a.credential(s, p, d, c.RefreshToken)
	if next.AccountID == "" {
		next.AccountID = c.AccountID
	}
	if e == nil && p == "google-antigravity" {
		return a.googleProject(ctx, s, next)
	}
	return next, e
}
