// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Executor owns current authorization and destination policy for EVERY physical send,
// including DNS resolution, redirects and credential exchanges. It must not follow
// redirects or retry token requests. Service implementations initiate I/O inside the
// admission gate and return the response without reading the stream in that gate.
type Executor interface {
	Do(context.Context, core.Scope, *http.Request) (*http.Response, error)
}
type Method string

const (
	PKCE          Method = "pkce"
	Device        Method = "device"
	BrowserPoll   Method = "browser-poll"
	ExplicitToken Method = "explicit-token"
)

var (
	ErrScope          = errors.New("provider personal scope mismatch")
	ErrFlow           = errors.New("provider login expired or invalid")
	ErrPending        = errors.New("provider authorization pending")
	ErrResponse       = errors.New("invalid provider authentication response")
	ErrNetwork        = errors.New("provider authentication transport failed")
	ErrReauthenticate = errors.New("provider requires a new explicit login")
)

type AuthError struct {
	Status int
	Code   string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("provider authentication failed (HTTP %d, %s)", e.Status, e.Code)
}
func (e *AuthError) Terminal() bool {
	return e.Code == "invalid_grant" || e.Code == "access_denied" || e.Code == "expired_token" || e.Status == 401 || e.Status == 403
}

// Credential and Flow must only be persisted through encrypted, personal storage.
type Credential struct {
	Scope        core.Scope        `json:"scope"`
	Provider     string            `json:"provider"`
	AccessToken  string            `json:"accessToken"`
	RefreshToken string            `json:"refreshToken,omitempty"`
	ExpiresAt    time.Time         `json:"expiresAt,omitempty"`
	AccountID    string            `json:"accountId,omitempty"`
	APIBaseURL   string            `json:"apiBaseUrl,omitempty"`
	ProjectID    string            `json:"projectId,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Google       *GoogleCredential `json:"google,omitempty"`
	Zed          *ZedAccount       `json:"zed,omitempty"`
}
type Flow struct {
	Scope       core.Scope        `json:"scope"`
	Provider    string            `json:"provider"`
	Method      Method            `json:"method"`
	State       string            `json:"state"`
	Verifier    string            `json:"verifier"`
	RedirectURI string            `json:"redirectUri"`
	DeviceCode  string            `json:"deviceCode,omitempty"`
	UserCode    string            `json:"userCode,omitempty"`
	ExpiresAt   time.Time         `json:"expiresAt"`
	NextPoll    time.Time         `json:"nextPoll"`
	Interval    time.Duration     `json:"interval"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}
type Challenge struct {
	URL, UserCode, State string
	Method               Method
	ExpiresAt            time.Time
	Interval             time.Duration
}
type StartOptions struct {
	Method                           Method
	RedirectURI                      string
	LoginProvider                    string
	SSORegion, APIRegion, StartURL   string
	BaseURL, AuthBaseURL             string
	PortalURL, RegisterURL, ClientID string
}
type Auth struct {
	executor Executor
	now      func() time.Time
}

func NewAuth(e Executor) *Auth { return &Auth{executor: e, now: time.Now} }
func AuthMethods(provider string) []Method {
	switch provider {
	case "openai":
		return []Method{Device, PKCE}
	case "anthropic", "anthropic2", "xai", "orcarouter-oauth", "google-antigravity":
		return []Method{PKCE}
	case "kimi", "kimi-responses", "nous", "github-copilot", "kiro", "meta-muse":
		return []Method{Device}
	case "cursor", "workbuddy-ai":
		return []Method{BrowserPoll}
	case "command-code":
		return []Method{NativeCallback, ExplicitToken}
	case "zed":
		return []Method{NativeCallback}
	case "google-vertex", "devin", "dsh-account":
		return []Method{ExplicitToken}
	}
	return nil
}
func validScope(s core.Scope) bool {
	return s.OrganizationID != "" && s.UserID != "" && s.Generation > 0
}
func randomString() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func challenge(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
func safeString(s string) bool {
	return s != "" && len(s) <= 32768 && !strings.ContainsAny(s, "\r\n\x00")
}
func str(m map[string]any, k string) string         { s, _ := m[k].(string); return s }
func obj(m map[string]any, k string) map[string]any { v, _ := m[k].(map[string]any); return v }
func number(m map[string]any, k string) float64 {
	switch v := m[k].(type) {
	case float64:
		return v
	case string:
		n, _ := strconv.ParseFloat(v, 64)
		return n
	}
	return 0
}
func seconds(m map[string]any, k string, fallback time.Duration) time.Duration {
	n := number(m, k)
	if n <= 0 || n > 86400*366 {
		return fallback
	}
	return time.Duration(n * float64(time.Second))
}
func authFailure(status int, m map[string]any) error {
	code := "upstream_error"
	switch str(m, "error") {
	case "invalid_grant", "access_denied", "expired_token", "authorization_pending", "slow_down", "invalid_client", "incorrect_device_code", "unsupported_grant_type":
		code = str(m, "error")
	}
	return &AuthError{status, code}
}

// request never includes untrusted response bodies or transport errors in diagnostics.
func (a *Auth) request(ctx context.Context, s core.Scope, method, endpoint string, body any, headers http.Header) (map[string]any, int, error) {
	b, status, _, e := a.requestBytes(ctx, s, method, endpoint, body, headers, 256*1024)
	if e != nil {
		return nil, status, e
	}
	m := map[string]any{}
	if json.Unmarshal(b, &m) != nil && status >= 200 && status < 300 {
		return nil, status, ErrResponse
	}
	return m, status, nil
}
func (a *Auth) send(ctx context.Context, s core.Scope, method, endpoint string, body any, h http.Header) (map[string]any, error) {
	m, status, e := a.request(ctx, s, method, endpoint, body, h)
	if e != nil {
		return nil, e
	}
	if status < 200 || status >= 300 || str(m, "error") != "" {
		return nil, authFailure(status, m)
	}
	return m, nil
}
func bearer(t string) http.Header { return http.Header{"Authorization": []string{"Bearer " + t}} }
func (a *Auth) check(ctx context.Context, s core.Scope, f Flow) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if !validScope(s) || s != f.Scope {
		return ErrScope
	}
	if f.State == "" || !a.now().Before(f.ExpiresAt) {
		return ErrFlow
	}
	return nil
}
func (a *Auth) credential(s core.Scope, p string, m map[string]any, previous string) (Credential, error) {
	access := str(m, "access_token")
	if !safeString(access) {
		return Credential{}, ErrResponse
	}
	refresh := str(m, "refresh_token")
	if refresh == "" {
		refresh = previous
	}
	if refresh != "" && !safeString(refresh) {
		return Credential{}, ErrResponse
	}
	c := Credential{Scope: s, Provider: p, AccessToken: access, RefreshToken: refresh, ExpiresAt: a.now().Add(seconds(m, "expires_in", time.Hour))}
	// JWT claims are provider metadata only, never service authentication or authorization.
	claims := jwtClaims(str(m, "id_token"))
	if claims == nil {
		claims = jwtClaims(access)
	}
	c.AccountID = str(claims, "sub")
	if p == "openai" {
		c.AccountID = str(claims, "chatgpt_account_id")
		if c.AccountID == "" {
			c.AccountID = str(obj(claims, "https://api.openai.com/auth"), "chatgpt_account_id")
		}
	}
	if p == "anthropic" || p == "anthropic2" {
		c.AccountID = str(obj(m, "account"), "uuid")
	}
	return c, nil
}
func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}
func equal(a, b string) bool { return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
