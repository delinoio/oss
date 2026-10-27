// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const NativeCallback Method = "native-callback"

// OAuth application credentials belong to the deployment, not the source tree.
// Keep Google login disabled until both values are supplied by the operator.
func googleClient() string       { return os.Getenv("AIPROUTER_GOOGLE_OAUTH_CLIENT_ID") }
func googleClientSecret() string { return os.Getenv("AIPROUTER_GOOGLE_OAUTH_CLIENT_SECRET") }

func (a *Auth) startNative(f *Flow) (Challenge, error) {
	switch f.Provider {
	case "zed":
		u, e := url.Parse(f.RedirectURI)
		if e != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || u.User != nil {
			return Challenge{}, ErrConnection
		}
		port, e := strconv.Atoi(u.Port())
		if e != nil || port < 1 || port > 65535 {
			return Challenge{}, ErrConnection
		}
		key, e := rsa.GenerateKey(rand.Reader, 2048)
		if e != nil {
			return Challenge{}, ErrResponse
		}
		f.Verifier = base64.RawURLEncoding.EncodeToString(x509.MarshalPKCS1PrivateKey(key))
		f.Metadata["systemId"] = core.ID()
		q := url.Values{"native_app_port": {strconv.Itoa(port)}, "native_app_public_key": {base64.RawURLEncoding.EncodeToString(x509.MarshalPKCS1PublicKey(&key.PublicKey))}, "system_id": {f.Metadata["systemId"]}}
		return Challenge{URL: "https://zed.dev/native_app_signin?" + q.Encode()}, nil
	case "command-code":
		if !validURL(f.RedirectURI) {
			return Challenge{}, ErrConnection
		}
		q := url.Values{"callback": {f.RedirectURI}, "state": {f.State}}
		return Challenge{URL: "https://commandcode.ai/studio/auth/cli?" + q.Encode()}, nil
	}
	return Challenge{}, ErrUnsupported
}
func (a *Auth) completeNative(ctx context.Context, s core.Scope, f Flow, code string) (Credential, error) {
	m := map[string]any{}
	if json.Unmarshal([]byte(code), &m) != nil {
		u, e := url.Parse(code)
		if e != nil {
			return Credential{}, ErrResponse
		}
		q := u.Query()
		if u.Host == "" {
			q, e = url.ParseQuery(strings.TrimPrefix(code, "?"))
			if e != nil {
				return Credential{}, ErrResponse
			}
		}
		for k, v := range q {
			if len(v) != 1 {
				return Credential{}, ErrResponse
			}
			m[k] = v[0]
		}
	}
	if f.Provider == "command-code" {
		if !equal(str(m, "state"), f.State) {
			return Credential{}, ErrFlow
		}
		return a.importToken(ctx, s, f.Provider, str(m, "apiKey"))
	}
	if f.Provider != "zed" {
		return Credential{}, ErrUnsupported
	}
	id := str(m, "user_id")
	if id == "" {
		id = str(m, "userId")
	}
	cipher := str(m, "access_token")
	if cipher == "" {
		cipher = str(m, "accessToken")
	}
	if cipher == "" {
		cipher = str(m, "token")
	}
	b, e := base64.RawURLEncoding.DecodeString(cipher)
	if e != nil {
		return Credential{}, ErrResponse
	}
	keyDER, e := base64.RawURLEncoding.DecodeString(f.Verifier)
	if e != nil {
		return Credential{}, ErrFlow
	}
	key, e := x509.ParsePKCS1PrivateKey(keyDER)
	if e != nil {
		return Credential{}, ErrFlow
	}
	plain, e := rsa.DecryptOAEP(sha256.New(), rand.Reader, key, b, nil)
	if e != nil {
		plain, e = rsa.DecryptPKCS1v15(rand.Reader, key, b)
	}
	if e != nil || !utf8.Valid(plain) || !safeString(string(plain)) || !safeString(id) {
		return Credential{}, ErrResponse
	}
	return a.PrepareZed(ctx, s, Credential{Scope: s, Provider: "zed", AccountID: id, Zed: &ZedAccount{NativeToken: string(plain), SystemID: f.Metadata["systemId"]}})
}

var devinToken = regexp.MustCompile(`^[A-Za-z0-9._$~+/=-]+$`)

func validDevinToken(token string) bool {
	return len(token) >= 20 && len(token) <= 4096 && devinToken.MatchString(token)
}

var devinPath = regexp.MustCompile(`^(/[A-Za-z0-9._-]+)*$`)

func devinURL(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || !validURL(raw) || u.Scheme != "https" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	h := u.Hostname()
	return (h == "server.codeium.com" || h == "server-staging.codeium.com" || h == "server-beta.codeium.com" || h == "windsurf.com" || strings.HasSuffix(h, ".windsurf.com") || h == "windsurf.fedstart.com") && devinPath.MatchString(strings.TrimRight(u.Path, "/"))
}
func (a *Auth) devin(ctx context.Context, s core.Scope, raw string) (Credential, error) {
	return a.devinAt(ctx, s, raw, "https://register.windsurf.com")
}
func (a *Auth) devinAt(ctx context.Context, s core.Scope, raw, register string) (Credential, error) {
	if !devinURL(register) {
		return Credential{}, ErrConnection
	}
	token := strings.TrimSpace(raw)
	if strings.HasPrefix(token, "https://") {
		u, e := url.Parse(token)
		if e != nil {
			return Credential{}, ErrResponse
		}
		fragment, _ := url.ParseQuery(u.Fragment)
		token = ""
		for _, q := range []url.Values{fragment, u.Query()} {
			for _, key := range []string{"firebase_id_token", "access_token", "id_token", "token"} {
				if v := q.Get(key); validDevinToken(v) {
					token = v
					break
				}
			}
			if token != "" {
				break
			}
		}
	}
	if !validDevinToken(token) {
		return Credential{}, ErrResponse
	}
	m, e := a.send(ctx, s, "POST", strings.TrimRight(register, "/")+"/exa.seat_management_pb.SeatManagementService/RegisterUser", map[string]string{"firebase_id_token": token}, http.Header{"Connect-Protocol-Version": {"1"}})
	if e != nil {
		return Credential{}, e
	}
	key := str(m, "api_key")
	base := str(m, "api_server_url")
	if base == "" {
		base = "https://server.codeium.com"
	}
	if !safeString(key) || !devinURL(base) {
		return Credential{}, ErrResponse
	}
	claims := jwtClaims(strings.TrimPrefix(key, "devin-session-token$"))
	id := str(claims, "auth_uid")
	if id == "" {
		id = str(claims, "sub")
	}
	return Credential{Scope: s, Provider: "devin", AccessToken: key, AccountID: id, APIBaseURL: base}, nil
}
func googleSpec() oauthSpec {
	return oauthSpec{googleClient(), "https://accounts.google.com/o/oauth2/v2/auth", "https://oauth2.googleapis.com/token", "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile https://www.googleapis.com/auth/cclog https://www.googleapis.com/auth/experimentsandconfigs", false}
}
func projectID(m map[string]any) string {
	for _, k := range []string{"cloudaicompanionProject", "projectId", "project"} {
		if v := str(m, k); v != "" {
			return v
		}
		if v := str(obj(m, k), "id"); v != "" {
			return v
		}
	}
	return ""
}
func (a *Auth) googleProject(ctx context.Context, s core.Scope, c Credential) (Credential, error) {
	h := bearer(c.AccessToken)
	h.Set("User-Agent", "antigravity/ide/2.5.5 (aidev_client)")
	m, e := a.send(ctx, s, "POST", "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist", map[string]any{"metadata": map[string]string{"ideType": "ANTIGRAVITY"}}, h)
	if e != nil {
		return Credential{}, e
	}
	id := projectID(m)
	for attempt := 0; id == "" && attempt < 5; attempt++ {
		m, e = a.send(ctx, s, "POST", "https://daily-cloudcode-pa.googleapis.com/v1internal:onboardUser", map[string]any{"tier_id": "free-tier", "metadata": map[string]string{"ide_type": "ANTIGRAVITY", "ide_name": "antigravity", "ide_version": "2.5.5"}}, h)
		if e != nil {
			return Credential{}, e
		}
		if m["done"] == true {
			id = projectID(obj(m, "response"))
			break
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Credential{}, ctx.Err()
		case <-timer.C:
		}
	}
	if !safeString(id) {
		return Credential{}, ErrResponse
	}
	c.ProjectID = id
	return c, nil
}
