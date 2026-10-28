// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/packages/tairouter/core"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

func scalarID(m map[string]any, k string) string {
	if s := str(m, k); s != "" {
		return s
	}
	if n, ok := m[k].(float64); ok && n > 0 && n < 9007199254740992 && n == float64(int64(n)) {
		return strconv.FormatInt(int64(n), 10)
	}
	return ""
}
func (a *Auth) startBrowser(ctx context.Context, f *Flow) (Challenge, error) {
	switch f.Provider {
	case "cursor":
		f.Metadata["uuid"] = core.ID()
		q := url.Values{"challenge": {challenge(f.Verifier)}, "uuid": {f.Metadata["uuid"]}, "mode": {"login"}, "redirectTarget": {"cli"}}
		f.Interval = time.Second
		return Challenge{URL: "https://cursor.com/loginDeepControl?" + q.Encode()}, nil
	case "workbuddy-ai":
		h := http.Header{}
		for _, k := range []string{"X-No-Authorization", "X-No-User-Id", "X-No-Enterprise-Id", "X-No-Department-Info"} {
			h.Set(k, "true")
		}
		m, e := a.workBuddy(ctx, f.Scope, "POST", "/v2/plugin/auth/state?platform=workbuddy-ai", map[string]any{}, h)
		if e != nil {
			return Challenge{}, e
		}
		f.DeviceCode = str(m, "state")
		raw := str(m, "authUrl")
		if !safeString(f.DeviceCode) || !trustedBrowser(raw, "www.workbuddy.ai", "www.codebuddy.ai") {
			return Challenge{}, ErrResponse
		}
		u, _ := url.Parse(raw)
		q := u.Query()
		q.Set("version", "2.0.0")
		q.Set("loginSessionId", core.ID())
		u.RawQuery = q.Encode()
		f.Interval = time.Second
		f.ExpiresAt = a.now().Add(5 * time.Minute)
		return Challenge{URL: u.String()}, nil
	}
	return Challenge{}, ErrUnsupported
}
func (a *Auth) pollBrowser(ctx context.Context, s core.Scope, f *Flow) (Credential, error) {
	switch f.Provider {
	case "cursor":
		q := url.Values{"uuid": {f.Metadata["uuid"]}, "verifier": {f.Verifier}}
		m, status, e := a.request(ctx, s, "GET", "https://api2.cursor.sh/auth/poll?"+q.Encode(), nil, nil)
		if e != nil {
			return Credential{}, e
		}
		if status == 404 {
			f.Interval = time.Duration(float64(f.Interval) * 1.2)
			if f.Interval > 10*time.Second {
				f.Interval = 10 * time.Second
			}
			f.NextPoll = a.now().Add(f.Interval)
			return Credential{}, ErrPending
		}
		if status != 200 {
			return Credential{}, authFailure(status, m)
		}
		return a.cursorCredential(s, m, "")
	case "workbuddy-ai":
		return a.pollWorkBuddy(ctx, s, f)
	}
	return Credential{}, ErrUnsupported
}
func (a *Auth) cursorCredential(s core.Scope, m map[string]any, previous string) (Credential, error) {
	access, refresh := str(m, "accessToken"), str(m, "refreshToken")
	if refresh == "" {
		refresh = previous
	}
	if !safeString(access) || !safeString(refresh) {
		return Credential{}, ErrResponse
	}
	claims := jwtClaims(access)
	id := scalarID(claims, "sub")
	if id == "" {
		id = scalarID(jwtClaims(refresh), "sub")
	}
	if id == "" {
		return Credential{}, ErrResponse
	}
	expiry := a.now().Add(time.Hour)
	if n := number(claims, "exp"); n > 0 && n < 1e12 {
		expiry = time.Unix(int64(n), 0)
	}
	return Credential{Scope: s, Provider: "cursor", AccessToken: access, RefreshToken: refresh, AccountID: id, ExpiresAt: expiry}, nil
}
func (a *Auth) importToken(ctx context.Context, s core.Scope, p, token string) (Credential, error) {
	if p == "google-vertex" {
		var g GoogleCredential
		if len(token) > 256*1024 || json.Unmarshal([]byte(token), &g) != nil {
			return Credential{}, ErrConnection
		}
		return a.ExchangeVertex(ctx, s, g, "")
	}
	if !safeString(token) {
		return Credential{}, ErrResponse
	}
	var id string
	switch p {
	case "devin":
		return a.devin(ctx, s, token)
	case "command-code":
		m, e := a.send(ctx, s, "GET", "https://api.commandcode.ai/alpha/whoami", nil, bearer(token))
		if e != nil {
			return Credential{}, e
		}
		id = str(obj(m, "user"), "id")
	case "dsh-account":
		m, e := a.send(ctx, s, "GET", "https://platform.deepseek.com/auth-api/v0/users/current", nil, http.Header{"x-dsh-auth-token": {token}})
		if e != nil {
			return Credential{}, e
		}
		d := obj(m, "data")
		if m["code"] != float64(0) || d["biz_code"] != float64(0) {
			return Credential{}, ErrResponse
		}
		id = str(obj(d, "biz_data"), "id")
	default:
		return Credential{}, ErrUnsupported
	}
	if !safeString(id) {
		return Credential{}, ErrResponse
	}
	return Credential{Scope: s, Provider: p, AccessToken: token, RefreshToken: token, AccountID: id}, nil
}
func (a *Auth) workBuddy(ctx context.Context, s core.Scope, method, path string, body any, h http.Header) (map[string]any, error) {
	if h == nil {
		h = make(http.Header)
	}
	h.Set("User-Agent", "WorkBuddy/5.5.6")
	m, status, e := a.request(ctx, s, method, "https://www.workbuddy.ai"+path, body, h)
	if e != nil {
		return nil, e
	}
	code := number(m, "code")
	if code == 11217 || code == 12151 || status == 408 || status == 429 {
		return nil, ErrPending
	}
	if status < 200 || status >= 300 {
		return nil, authFailure(status, m)
	}
	if _, ok := m["code"]; !ok || code != 0 {
		return nil, ErrResponse
	}
	d := obj(m, "data")
	if len(d) == 0 {
		return nil, ErrPending
	}
	return d, nil
}
func (a *Auth) pollWorkBuddy(ctx context.Context, s core.Scope, f *Flow) (Credential, error) {
	q := "?state=" + url.QueryEscape(f.DeviceCode)
	var token map[string]any
	if f.Metadata["token"] != "" {
		if json.Unmarshal([]byte(f.Metadata["token"]), &token) != nil {
			return Credential{}, ErrFlow
		}
	} else {
		var e error
		token, e = a.workBuddy(ctx, s, "GET", "/v2/plugin/auth/token"+q, nil, nil)
		if e != nil {
			return Credential{}, e
		}
		b, _ := json.Marshal(token)
		f.Metadata["token"] = string(b)
	}
	access := str(token, "accessToken")
	if !safeString(access) {
		return Credential{}, ErrResponse
	}
	domain := str(token, "domain")
	if domain == "" {
		domain = "www.workbuddy.ai"
	}
	if !safeString(domain) {
		return Credential{}, ErrResponse
	}
	h := bearer(access)
	h.Set("X-Domain", domain)
	h.Set("X-No-User-Id", "true")
	h.Set("X-No-Enterprise-Id", "true")
	who, e := a.workBuddy(ctx, s, "GET", "/v2/plugin/login/account"+q, nil, h)
	if e != nil {
		return Credential{}, e
	}
	uid := str(who, "uid")
	enterprise := str(who, "enterpriseId")
	if !safeString(uid) {
		return Credential{}, ErrResponse
	}
	if enterprise == "" && str(who, "type") != "personal" {
		m, e := a.workBuddy(ctx, s, "GET", "/v2/plugin/accounts", nil, h)
		if e != nil {
			return Credential{}, e
		}
		rows, _ := m["accounts"].([]any)
		var mine, same, last []string
		for _, v := range rows {
			r, _ := v.(map[string]any)
			eid := str(r, "enterpriseId")
			if str(r, "uid") != uid || !safeString(eid) {
				continue
			}
			mine = append(mine, eid)
			if str(r, "type") == str(who, "type") {
				same = append(same, eid)
			}
			if r["lastLogin"] == true {
				last = append(last, eid)
			}
		}
		switch {
		case len(same) == 1:
			enterprise = same[0]
		case len(last) == 1:
			enterprise = last[0]
		case len(mine) == 1:
			enterprise = mine[0]
		default:
			return Credential{}, ErrResponse
		}
	}
	return a.workBuddyCredential(s, token, map[string]string{"uid": uid, "domain": domain, "enterpriseId": enterprise}, "")
}
func (a *Auth) workBuddyCredential(s core.Scope, m map[string]any, meta map[string]string, previous string) (Credential, error) {
	access, refresh := str(m, "accessToken"), str(m, "refreshToken")
	if refresh == "" {
		refresh = previous
	}
	if !safeString(access) || !safeString(refresh) {
		return Credential{}, ErrResponse
	}
	expiry := a.now().Add(seconds(m, "expiresIn", 0))
	if n := number(m, "expiresAt"); n > 0 && n < 1e15 {
		expiry = time.UnixMilli(int64(n))
	}
	if !expiry.After(a.now()) {
		return Credential{}, ErrResponse
	}
	if d := str(m, "domain"); d != "" {
		if !safeString(d) {
			return Credential{}, ErrResponse
		}
		meta["domain"] = d
	}
	if n := number(m, "refreshExpiresAt"); n > 0 && n < 1e15 {
		meta["refreshExpiresAt"] = strconv.FormatInt(int64(n), 10)
	} else if n := seconds(m, "refreshExpiresIn", 0); n > 0 {
		meta["refreshExpiresAt"] = strconv.FormatInt(a.now().Add(n).UnixMilli(), 10)
	}
	id, _ := json.Marshal([]string{meta["uid"], meta["enterpriseId"]})
	return Credential{Scope: s, Provider: "workbuddy-ai", AccessToken: access, RefreshToken: refresh, ExpiresAt: expiry, AccountID: string(id), Metadata: meta}, nil
}
func (a *Auth) refreshWorkBuddy(ctx context.Context, s core.Scope, c Credential) (Credential, error) {
	meta := map[string]string{}
	for k, v := range c.Metadata {
		meta[k] = v
	}
	if !safeString(meta["uid"]) || !safeString(meta["domain"]) {
		return Credential{}, ErrReauthenticate
	}
	if n, _ := strconv.ParseInt(meta["refreshExpiresAt"], 10, 64); n > 0 && a.now().UnixMilli() >= n {
		return Credential{}, ErrReauthenticate
	}
	h := http.Header{"X-Refresh-Token": {c.RefreshToken}, "X-Auth-Refresh-Source": {"plugin"}, "X-User-Id": {meta["uid"]}, "X-Domain": {meta["domain"]}, "X-Product": {"SaaS"}, "X-IDE-Type": {"WorkBuddy"}, "X-IDE-Name": {"WorkBuddy"}, "X-IDE-Version": {"5.5.6"}, "X-Requested-With": {"XMLHttpRequest"}}
	if id := meta["enterpriseId"]; id != "" {
		if !safeString(id) {
			return Credential{}, ErrResponse
		}
		h.Set("X-Enterprise-Id", id)
		h.Set("X-Tenant-Id", id)
	}
	m, e := a.workBuddy(ctx, s, "POST", "/v2/plugin/auth/token/refresh", map[string]any{}, h)
	if e != nil {
		return Credential{}, e
	}
	return a.workBuddyCredential(s, m, meta, c.RefreshToken)
}
