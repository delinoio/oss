// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func trustedBrowser(raw string, hosts ...string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.User == nil && (u.Port() == "" || u.Port() == "443") && contains(hosts, u.Hostname())
}
func (a *Auth) startDevice(ctx context.Context, f *Flow, o StartOptions) (Challenge, error) {
	if f.Provider == "kiro" {
		return a.startKiro(ctx, f, o.LoginProvider)
	}
	sp := spec(f.Provider)
	endpoint := ""
	host := ""
	h := http.Header{}
	var body any = form(map[string]string{"client_id": sp.client})
	switch f.Provider {
	case "openai":
		endpoint = "https://auth.openai.com/api/accounts/deviceauth/usercode"
		body = map[string]string{"client_id": sp.client}
	case "kimi", "kimi-responses":
		endpoint = "https://auth.kimi.com/api/oauth/device_authorization"
		host = "auth.kimi.com"
	case "nous":
		endpoint = "https://portal.nousresearch.com/api/oauth/device/code"
		host = "portal.nousresearch.com"
		body = form(map[string]string{"client_id": sp.client, "scope": sp.scope})
	case "github-copilot":
		endpoint = "https://github.com/login/device/code"
		host = "github.com"
		body = form(map[string]string{"client_id": sp.client, "scope": sp.scope})
	case "meta-muse":
		endpoint = "https://auth.meta.com/oidc/device/authorization/"
		host = "auth.meta.com"
		h.Set("x-api-version", "1.0.0")
	default:
		return Challenge{}, ErrUnsupported
	}
	m, e := a.send(ctx, f.Scope, "POST", endpoint, body, h)
	if e != nil {
		return Challenge{}, e
	}
	f.DeviceCode = str(m, "device_code")
	f.UserCode = str(m, "user_code")
	verify := str(m, "verification_uri_complete")
	if verify == "" {
		verify = str(m, "verification_uri")
	}
	if f.Provider == "openai" {
		f.DeviceCode = str(m, "device_auth_id")
		if f.UserCode == "" {
			f.UserCode = str(m, "usercode")
		}
		verify = "https://auth.openai.com/codex/device"
		host = "auth.openai.com"
	}
	if f.Provider == "github-copilot" {
		verify = "https://github.com/login/device?user_code=" + url.QueryEscape(f.UserCode)
	}
	if f.Provider == "meta-muse" && verify == "" {
		verify = "https://auth.meta.com/oauth/device/"
	}
	if !safeString(f.DeviceCode) || !safeString(f.UserCode) || !trustedBrowser(verify, host) {
		return Challenge{}, ErrResponse
	}
	f.Interval = seconds(m, "interval", 5*time.Second)
	if f.Interval > 15*time.Minute {
		f.Interval = 15 * time.Minute
	}
	if f.Interval < time.Second {
		f.Interval = time.Second
	}
	ttl := seconds(m, "expires_in", 15*time.Minute)
	if ttl > 15*time.Minute {
		ttl = 15 * time.Minute
	}
	f.ExpiresAt = a.now().Add(ttl)
	return Challenge{URL: verify, UserCode: f.UserCode}, nil
}

// Poll performs one attempt. The caller atomically claims the stored flow, then
// saves NextPoll/Interval even when ErrPending is returned. No background goroutines.
func (a *Auth) Poll(ctx context.Context, s core.Scope, f *Flow) (result Credential, err error) {
	defer func() {
		if err == nil && f != nil {
			if e := a.check(ctx, s, *f); e != nil {
				result = Credential{}
				err = e
			}
		}
	}()
	if f == nil {
		return Credential{}, ErrFlow
	}
	if e := a.check(ctx, s, *f); e != nil {
		return Credential{}, e
	}
	if a.now().Before(f.NextPoll) {
		return Credential{}, ErrPending
	}
	f.NextPoll = a.now().Add(f.Interval)
	if f.Method == BrowserPoll {
		return a.pollBrowser(ctx, s, f)
	}
	if f.Method != Device {
		return Credential{}, ErrUnsupported
	}
	if f.Provider == "kiro" {
		return a.pollKiro(ctx, s, f)
	}
	sp := spec(f.Provider)
	endpoint := sp.token
	h := http.Header{}
	var body any = form(map[string]string{"client_id": sp.client, "device_code": f.DeviceCode, "grant_type": "urn:ietf:params:oauth:grant-type:device_code"})
	if f.Provider == "openai" {
		endpoint = "https://auth.openai.com/api/accounts/deviceauth/token"
		body = map[string]string{"device_auth_id": f.DeviceCode, "user_code": f.UserCode}
	}
	if f.Provider == "meta-muse" {
		h.Set("x-api-version", "1.0.0")
	}
	m, status, e := a.request(ctx, s, "POST", endpoint, body, h)
	if e != nil {
		return Credential{}, e
	}
	if e = a.check(ctx, s, *f); e != nil {
		return Credential{}, e
	}
	if f.Provider == "openai" && (status == 403 || status == 404) {
		return Credential{}, ErrPending
	}
	switch str(m, "error") {
	case "authorization_pending":
		return Credential{}, ErrPending
	case "slow_down":
		f.Interval += 5 * time.Second
		f.NextPoll = a.now().Add(f.Interval)
		return Credential{}, ErrPending
	}
	if status < 200 || status >= 300 || str(m, "error") != "" {
		return Credential{}, authFailure(status, m)
	}
	if f.Provider == "openai" {
		code, v := str(m, "authorization_code"), str(m, "code_verifier")
		if !safeString(code) || !safeString(v) {
			return Credential{}, ErrResponse
		}
		m, e = a.send(ctx, s, "POST", sp.token, form(map[string]string{"grant_type": "authorization_code", "client_id": sp.client, "code": code, "code_verifier": v, "redirect_uri": "https://auth.openai.com/deviceauth/callback"}), nil)
		if e != nil {
			return Credential{}, e
		}
	}
	if f.Provider == "github-copilot" {
		access := str(m, "access_token")
		grant := str(m, "refresh_token")
		if grant == "" {
			grant = access
		}
		return a.copilot(ctx, s, access, grant)
	}
	if f.Provider == "meta-muse" {
		return a.muse(ctx, s, str(m, "access_token"))
	}
	return a.credential(s, f.Provider, m, "")
}
func copilotURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.User == nil && u.Port() == "" && (u.Hostname() == "api.githubcopilot.com" || strings.HasSuffix(u.Hostname(), ".githubcopilot.com")) && (u.Path == "" || u.Path == "/") && u.RawQuery == "" && u.Fragment == ""
}
func (a *Auth) copilot(ctx context.Context, s core.Scope, access, grant string) (Credential, error) {
	if !safeString(access) || !safeString(grant) {
		return Credential{}, ErrResponse
	}
	h := http.Header{"Authorization": {"token " + access}, "Editor-Version": {"opencodex/0.1.0"}, "Editor-Plugin-Version": {"opencodex/0.1.0"}, "Copilot-Integration-Id": {"vscode-chat"}}
	m, e := a.send(ctx, s, "GET", "https://api.github.com/copilot_internal/v2/token", nil, h)
	if e != nil {
		return Credential{}, e
	}
	token := str(m, "token")
	if !safeString(token) {
		return Credential{}, ErrResponse
	}
	base := str(obj(m, "endpoints"), "api")
	if base == "" {
		base = "https://api.githubcopilot.com"
	}
	if !copilotURL(base) {
		return Credential{}, ErrResponse
	}
	h = bearer(access)
	h.Set("X-GitHub-Api-Version", "2022-11-28")
	who, e := a.send(ctx, s, "GET", "https://api.github.com/user", nil, h)
	if e != nil {
		return Credential{}, e
	}
	id := scalarID(who, "id")
	if id == "" {
		id = str(who, "login")
	}
	if !safeString(id) {
		return Credential{}, ErrResponse
	}
	expiry := a.now().Add(seconds(m, "refresh_in", 25*time.Minute))
	if n := number(m, "expires_at"); n > 0 && n < 1e12 {
		expiry = time.Unix(int64(n), 0)
	}
	expiry = expiry.Add(-2 * time.Minute)
	return Credential{Scope: s, Provider: "github-copilot", AccessToken: token, RefreshToken: grant, ExpiresAt: expiry, AccountID: id, APIBaseURL: base}, nil
}
func (a *Auth) muse(ctx context.Context, s core.Scope, access string) (Credential, error) {
	if !safeString(access) {
		return Credential{}, ErrResponse
	}
	h := bearer(access)
	h.Set("x-api-version", "1.0.0")
	m, e := a.send(ctx, s, "POST", "https://api.meta.ai/muse-code/key", map[string]bool{"onboard": true}, h)
	if e != nil {
		return Credential{}, e
	}
	key := str(m, "api_key")
	id := str(m, "user_email")
	if id == "" {
		id = str(m, "user_id")
	}
	if !safeString(key) || !safeString(id) {
		return Credential{}, ErrResponse
	}
	meta := map[string]string{"oauthAccessToken": access, "userId": str(m, "user_id")}
	if m["require_payment"] == true || str(m, "action_url") != "" || str(m, "require_payment_action_url") != "" {
		meta["paymentRequired"] = "true"
	}
	return Credential{Scope: s, Provider: "meta-muse", AccessToken: key, AccountID: id, Metadata: meta}, nil
}
