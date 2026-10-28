// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"github.com/delinoio/oss/packages/tairouter/core"
	"strings"
	"time"
)

const kiroOIDC = "https://oidc.us-east-1.amazonaws.com"
const kiroSocial = "https://prod.us-east-1.auth.desktop.kiro.dev"

func (a *Auth) startKiro(ctx context.Context, f *Flow, method string) (Challenge, error) {
	if method == "" {
		method = "builder-id"
	}
	f.Metadata["loginProvider"] = method
	oidc, social, err := kiroOrigins(f.Metadata)
	if err != nil {
		return Challenge{}, err
	}
	startURL := f.Metadata["startUrl"]
	if startURL == "" {
		startURL = "https://view.awsapps.com/start"
	}
	if method == "identity-center" && f.Metadata["startUrl"] == "" {
		return Challenge{}, ErrConnection
	}
	var m map[string]any
	var e error
	switch method {
	case "builder-id", "identity-center":
		r, e := a.send(ctx, f.Scope, "POST", oidc+"/client/register", map[string]any{"clientName": "kiro-cli", "clientType": "public", "scopes": []string{"codewhisperer:completions", "codewhisperer:analysis", "codewhisperer:conversations"}}, nil)
		if e != nil {
			return Challenge{}, e
		}
		id, secret := str(r, "clientId"), str(r, "clientSecret")
		if !safeString(id) || !safeString(secret) {
			return Challenge{}, ErrResponse
		}
		f.Metadata["clientId"] = id
		f.Metadata["clientSecret"] = secret
		m, e = a.send(ctx, f.Scope, "POST", oidc+"/device_authorization", map[string]string{"clientId": id, "clientSecret": secret, "startUrl": startURL}, nil)
	case "google", "github":
		provider := "Google"
		if method == "github" {
			provider = "Github"
		}
		m, e = a.send(ctx, f.Scope, "POST", social+"/oauth/device/authorization", map[string]string{"clientId": "kiro-cli", "loginProvider": provider}, nil)
	default:
		return Challenge{}, ErrUnsupported
	}
	if e != nil {
		return Challenge{}, e
	}
	f.DeviceCode = str(m, "deviceCode")
	f.UserCode = str(m, "userCode")
	raw := str(m, "verificationUriComplete")
	if raw == "" {
		raw = str(m, "verificationUri")
	}
	// AWS's device page is fixed to the AWS login family, not an arbitrary upstream URL.
	if !safeString(f.DeviceCode) || !safeString(f.UserCode) || !trustedBrowser(raw, "view.awsapps.com", "device.sso."+f.Metadata["ssoRegion"]+".amazonaws.com", "signin.aws.amazon.com", "view.awsapps.com", strings.TrimPrefix(social, "https://"), "kiro.dev") {
		return Challenge{}, ErrResponse
	}
	ttl := 10 * time.Minute
	f.Interval = 5 * time.Second
	if method == "builder-id" || method == "identity-center" {
		ttl = seconds(m, "expiresIn", ttl)
		f.Interval = seconds(m, "interval", f.Interval)
	} else {
		ttl = 5 * time.Minute
		if n := number(m, "expiresInMilliseconds"); n > 0 && n < 900000 {
			ttl = time.Duration(n) * time.Millisecond
		}
		if n := number(m, "intervalInMilliseconds"); n >= 1000 && n < 900000 {
			f.Interval = time.Duration(n) * time.Millisecond
		}
	}
	if ttl > 15*time.Minute {
		ttl = 15 * time.Minute
	}
	f.ExpiresAt = a.now().Add(ttl)
	return Challenge{URL: raw, UserCode: f.UserCode}, nil
}
func (a *Auth) pollKiro(ctx context.Context, s core.Scope, f *Flow) (Credential, error) {
	builder := f.Metadata["loginProvider"] == "builder-id" || f.Metadata["loginProvider"] == "identity-center"
	oidc, social, err := kiroOrigins(f.Metadata)
	if err != nil {
		return Credential{}, err
	}
	endpoint := social + "/oauth/device/poll"
	body := map[string]string{"clientId": "kiro-cli", "deviceCode": f.DeviceCode}
	if builder {
		endpoint = oidc + "/token"
		body = map[string]string{"clientId": f.Metadata["clientId"], "clientSecret": f.Metadata["clientSecret"], "deviceCode": f.DeviceCode, "grantType": "urn:ietf:params:oauth:grant-type:device_code"}
	}
	m, status, e := a.request(ctx, s, "POST", endpoint, body, nil)
	if e != nil {
		return Credential{}, e
	}
	code := str(m, "error")
	if code == "" {
		code = str(m, "__type")
	}
	switch code {
	case "authorization_pending", "AuthorizationPendingException":
		return Credential{}, ErrPending
	case "slow_down", "SlowDownException":
		f.Interval += 5 * time.Second
		f.NextPoll = a.now().Add(f.Interval)
		return Credential{}, ErrPending
	}
	if str(m, "status") == "authorization_pending" {
		return Credential{}, ErrPending
	}
	if status != 200 || code != "" {
		return Credential{}, authFailure(status, m)
	}
	if !builder && str(m, "status") != "" && str(m, "status") != "authorized" && str(m, "status") != "approved" {
		return Credential{}, ErrResponse
	}
	meta := map[string]string{"apiRegion": f.Metadata["apiRegion"], "ssoRegion": f.Metadata["ssoRegion"]}
	if builder {
		meta["clientId"] = f.Metadata["clientId"]
		meta["clientSecret"] = f.Metadata["clientSecret"]
	} else {
		arn := str(m, "profileArn")
		if !strings.HasPrefix(arn, "arn:aws:codewhisperer:") || !safeString(arn) {
			return Credential{}, ErrResponse
		}
		meta["profileArn"] = arn
	}
	return a.kiroCredential(s, m, meta, "")
}
func (a *Auth) kiroCredential(s core.Scope, m map[string]any, meta map[string]string, previous string) (Credential, error) {
	access, refresh := str(m, "accessToken"), str(m, "refreshToken")
	if refresh == "" {
		refresh = previous
	}
	if !safeString(access) || !safeString(refresh) {
		return Credential{}, ErrResponse
	}
	return Credential{Scope: s, Provider: "kiro", AccessToken: access, RefreshToken: refresh, ExpiresAt: a.now().Add(seconds(m, "expiresIn", time.Hour)), AccountID: meta["profileArn"], Metadata: meta}, nil
}
func (a *Auth) refreshKiro(ctx context.Context, s core.Scope, c Credential) (Credential, error) {
	oidc, social, err := kiroOrigins(c.Metadata)
	if err != nil {
		return Credential{}, err
	}
	endpoint := social + "/refreshToken"
	body := map[string]string{"refreshToken": c.RefreshToken}
	if id := c.Metadata["clientId"]; id != "" {
		secret := c.Metadata["clientSecret"]
		if !safeString(id) || !safeString(secret) {
			return Credential{}, ErrReauthenticate
		}
		endpoint = oidc + "/token"
		body = map[string]string{"clientId": id, "clientSecret": secret, "grantType": "refresh_token", "refreshToken": c.RefreshToken}
	}
	m, e := a.send(ctx, s, "POST", endpoint, body, nil)
	if e != nil {
		return Credential{}, e
	}
	next, e := a.kiroCredential(s, m, c.Metadata, c.RefreshToken)
	if next.AccountID == "" {
		next.AccountID = c.AccountID
	}
	return next, e
}
