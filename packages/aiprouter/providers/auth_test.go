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
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

var testScope = core.Scope{OrganizationID: "org", UserID: "user", Generation: 1}

type responseStep struct {
	method, endpoint string
	status           int
	body             string
	check            func(*testing.T, *http.Request, []byte)
}
type mockExecutor struct {
	t     *testing.T
	steps []responseStep
	calls int
}

func (m *mockExecutor) Do(ctx context.Context, s core.Scope, r *http.Request) (*http.Response, error) {
	m.t.Helper()
	if s != testScope {
		m.t.Fatal("wrong scope at physical send")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if _, ok := ctx.Deadline(); !ok {
		m.t.Fatal("missing network deadline")
	}
	if m.calls >= len(m.steps) {
		m.t.Fatal("unexpected physical send")
	}
	step := m.steps[m.calls]
	m.calls++
	if r.Method != step.method || r.URL.String() != step.endpoint {
		m.t.Fatalf("send %d: %s %s; want %s %s", m.calls, r.Method, r.URL, step.method, step.endpoint)
	}
	b, _ := io.ReadAll(r.Body)
	if step.check != nil {
		step.check(m.t, r, b)
	}
	status := step.status
	if status == 0 {
		status = 200
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(step.body))}, nil
}
func newMock(t *testing.T, steps ...responseStep) (*Auth, *mockExecutor) {
	t.Helper()
	m := &mockExecutor{t: t, steps: steps}
	a := NewAuth(m)
	a.now = func() time.Time { return time.Unix(1700000000, 0) }
	t.Cleanup(func() {
		if m.calls != len(m.steps) {
			t.Errorf("used %d of %d network steps", m.calls, len(m.steps))
		}
	})
	return a, m
}
func formValue(k, want string) func(*testing.T, *http.Request, []byte) {
	return func(t *testing.T, r *http.Request, b []byte) {
		t.Helper()
		v, e := url.ParseQuery(string(b))
		if e != nil || v.Get(k) != want {
			t.Fatalf("wrong form field %s", k)
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Fatal("wrong form content type")
		}
	}
}
func jsonValue(k, want string) func(*testing.T, *http.Request, []byte) {
	return func(t *testing.T, r *http.Request, b []byte) {
		t.Helper()
		var v map[string]any
		if json.Unmarshal(b, &v) != nil || v[k] != want {
			t.Fatalf("wrong JSON field %s", k)
		}
	}
}
func jwt(payload string) string {
	return "e30." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".fixture"
}
func due(a *Auth, f *Flow) { f.NextPoll = a.now() }
func TestPKCEFamilies(t *testing.T) {
	t.Setenv("AIPROUTER_GOOGLE_OAUTH_CLIENT_ID", "fixture-google-client")
	t.Setenv("AIPROUTER_GOOGLE_OAUTH_CLIENT_SECRET", "fixture-google-secret")
	for _, p := range []string{"openai", "anthropic", "anthropic2", "xai", "orcarouter-oauth", "google-antigravity"} {
		t.Run(p, func(t *testing.T) {
			steps := []responseStep{}
			sp := spec(p)
			if p == "xai" {
				steps = append(steps, responseStep{"GET", "https://auth.x.ai/.well-known/openid-configuration", 200, `{"authorization_endpoint":"https://accounts.x.ai/oauth/authorize","token_endpoint":"https://auth.x.ai/oauth/token"}`, nil})
				sp.token = "https://auth.x.ai/oauth/token"
			}
			endpoint := sp.token
			body := `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`
			check := formValue("code", "code")
			if p == "anthropic" || p == "anthropic2" {
				check = jsonValue("code", "code")
			}
			if p == "orcarouter-oauth" {
				endpoint = "https://www.orcarouter.ai/api/v1/auth/keys"
				body = `{"key":"sk-orca-fixture","user_id":"upstream-user","scope":"api"}`
				check = jsonValue("code", "code")
			}
			if p == "google-antigravity" {
				check = formValue("client_secret", "fixture-google-secret")
			}
			steps = append(steps, responseStep{"POST", endpoint, 200, body, check})
			if p == "google-antigravity" {
				steps = append(steps, responseStep{"POST", "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist", 200, `{"cloudaicompanionProject":{"id":"project"}}`, nil})
			}
			a, _ := newMock(t, steps...)
			ch, f, e := a.Start(context.Background(), testScope, p, StartOptions{Method: PKCE, RedirectURI: "http://localhost:1455/auth/callback"})
			if e != nil {
				t.Fatal(e)
			}
			u, _ := url.Parse(ch.URL)
			if u.Query().Get("code_challenge") != challenge(f.Verifier) || u.Query().Get("state") != f.State {
				t.Fatal("PKCE/state not bound")
			}
			if strings.Contains(ch.URL, f.Verifier) {
				t.Fatal("verifier disclosed")
			}
			code := "code"
			if p == "anthropic2" {
				code += "#" + f.State
			}
			c, e := a.Complete(context.Background(), testScope, f, code, f.State)
			if e != nil {
				t.Fatal(e)
			}
			if c.Provider != p || c.Scope != testScope || c.AccessToken == "" {
				t.Fatal("credential owner lost")
			}
			if p == "google-antigravity" && c.ProjectID != "project" {
				t.Fatal("project missing")
			}
		})
	}
}
func TestDeviceFamilies(t *testing.T) {
	cases := []struct{ p, start, verify string }{
		{"kimi", "https://auth.kimi.com/api/oauth/device_authorization", "https://auth.kimi.com/device"},
		{"kimi-responses", "https://auth.kimi.com/api/oauth/device_authorization", "https://auth.kimi.com/device"},
		{"nous", "https://portal.nousresearch.com/api/oauth/device/code", "https://portal.nousresearch.com/device"},
		{"github-copilot", "https://github.com/login/device/code", "https://github.com/login/device"},
		{"meta-muse", "https://auth.meta.com/oidc/device/authorization/", "https://auth.meta.com/oauth/device/"},
	}
	for _, tc := range cases {
		t.Run(tc.p, func(t *testing.T) {
			sp := spec(tc.p)
			steps := []responseStep{
				{"POST", tc.start, 200, `{"device_code":"secret-device","user_code":"ABCD","verification_uri":"` + tc.verify + `","interval":5,"expires_in":600}`, formValue("client_id", sp.client)},
				{"POST", sp.token, 400, `{"error":"authorization_pending"}`, nil},
				{"POST", sp.token, 400, `{"error":"slow_down"}`, nil},
				{"POST", sp.token, 200, `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`, formValue("device_code", "secret-device")},
			}
			if tc.p == "github-copilot" {
				steps = append(steps, responseStep{"GET", "https://api.github.com/copilot_internal/v2/token", 200, `{"token":"copilot","expires_at":1700003600,"endpoints":{"api":"https://api.business.githubcopilot.com"}}`, func(t *testing.T, r *http.Request, b []byte) {
					if r.Header.Get("Authorization") != "token access" {
						t.Fatal("wrong GitHub grant")
					}
				}}, responseStep{"GET", "https://api.github.com/user", 200, `{"id":123}`, nil})
			}
			if tc.p == "meta-muse" {
				steps = append(steps, responseStep{"POST", "https://api.meta.ai/muse-code/key", 200, `{"api_key":"muse-key","user_id":"muse-user","require_payment":true}`, func(t *testing.T, r *http.Request, b []byte) {
					if r.Header.Get("Authorization") != "Bearer access" || r.Header.Get("x-api-version") != "1.0.0" {
						t.Fatal("wrong Muse mint auth")
					}
				}})
			}
			a, m := newMock(t, steps...)
			ch, f, e := a.Start(context.Background(), testScope, tc.p, StartOptions{})
			if e != nil {
				t.Fatal(e)
			}
			if ch.UserCode != "ABCD" || strings.Contains(ch.URL, "secret-device") {
				t.Fatal("unsafe challenge")
			}
			if _, e = a.Poll(context.Background(), testScope, &f); !errors.Is(e, ErrPending) || m.calls != 1 {
				t.Fatal("poll interval not enforced")
			}
			for i := 0; i < 2; i++ {
				due(a, &f)
				if _, e = a.Poll(context.Background(), testScope, &f); !errors.Is(e, ErrPending) {
					t.Fatal(e)
				}
			}
			if f.Interval != 10*time.Second {
				t.Fatal("slow_down not persisted")
			}
			due(a, &f)
			c, e := a.Poll(context.Background(), testScope, &f)
			if e != nil {
				t.Fatal(e)
			}
			if c.AccessToken == "" || c.Scope != testScope {
				t.Fatal("invalid credential")
			}
			if tc.p == "meta-muse" && (c.AccessToken != "muse-key" || c.Metadata["paymentRequired"] != "true") {
				t.Fatal("Muse account token incorrectly used as API credential")
			}
		})
	}
}
func TestOpenAIDeviceThreeStep(t *testing.T) {
	a, _ := newMock(t,
		responseStep{"POST", "https://auth.openai.com/api/accounts/deviceauth/usercode", 200, `{"device_auth_id":"device","usercode":"USER","interval":"5"}`, nil},
		responseStep{"POST", "https://auth.openai.com/api/accounts/deviceauth/token", 403, `not JSON`, nil},
		responseStep{"POST", "https://auth.openai.com/api/accounts/deviceauth/token", 200, `{"authorization_code":"issued","code_verifier":"server-verifier"}`, jsonValue("device_auth_id", "device")},
		responseStep{"POST", "https://auth.openai.com/oauth/token", 200, `{"access_token":"access","refresh_token":"refresh"}`, formValue("code_verifier", "server-verifier")})
	_, f, e := a.Start(context.Background(), testScope, "openai", StartOptions{})
	if e != nil {
		t.Fatal(e)
	}
	due(a, &f)
	if _, e = a.Poll(context.Background(), testScope, &f); !errors.Is(e, ErrPending) {
		t.Fatal(e)
	}
	due(a, &f)
	if _, e = a.Poll(context.Background(), testScope, &f); e != nil {
		t.Fatal(e)
	}
}
func TestKiroDeviceMethods(t *testing.T) {
	for _, method := range []string{"builder-id", "google", "github"} {
		t.Run(method, func(t *testing.T) {
			steps := []responseStep{}
			start, poll := kiroSocial+"/oauth/device/authorization", kiroSocial+"/oauth/device/poll"
			body := `{"status":"authorized","accessToken":"access","refreshToken":"refresh","profileArn":"arn:aws:codewhisperer:us-east-1:123:profile/test"}`
			if method == "builder-id" {
				steps = append(steps, responseStep{"POST", kiroOIDC + "/client/register", 200, `{"clientId":"client","clientSecret":"secret"}`, nil})
				start = kiroOIDC + "/device_authorization"
				poll = kiroOIDC + "/token"
				body = `{"accessToken":"access","refreshToken":"refresh","expiresIn":3600}`
			}
			steps = append(steps, responseStep{"POST", start, 200, `{"deviceCode":"device","userCode":"ABCD","verificationUri":"https://view.awsapps.com/start"}`, nil}, responseStep{"POST", poll, 200, body, nil})
			a, _ := newMock(t, steps...)
			_, f, e := a.Start(context.Background(), testScope, "kiro", StartOptions{LoginProvider: method})
			if e != nil {
				t.Fatal(e)
			}
			due(a, &f)
			c, e := a.Poll(context.Background(), testScope, &f)
			if e != nil {
				t.Fatal(e)
			}
			if c.Metadata["ssoRegion"] != "us-east-1" {
				t.Fatal("region lost")
			}
			if method == "builder-id" && c.Metadata["clientSecret"] != "secret" {
				t.Fatal("registered client lost")
			}
		})
	}
}
func TestRefreshProtocols(t *testing.T) {
	t.Setenv("AIPROUTER_GOOGLE_OAUTH_CLIENT_ID", "fixture-google-client")
	t.Setenv("AIPROUTER_GOOGLE_OAUTH_CLIENT_SECRET", "fixture-google-secret")
	for _, p := range []string{"openai", "anthropic", "anthropic2", "kimi", "kimi-responses", "nous", "xai", "google-antigravity", "kiro", "cursor", "github-copilot"} {
		t.Run(p, func(t *testing.T) {
			c := Credential{Scope: testScope, Provider: p, AccessToken: "old", RefreshToken: "refresh"}
			steps := []responseStep{}
			sp := spec(p)
			endpoint := sp.token
			body := `{"access_token":"new","expires_in":3600}`
			check := formValue("refresh_token", "refresh")
			switch p {
			case "xai":
				steps = append(steps, responseStep{"GET", "https://auth.x.ai/.well-known/openid-configuration", 200, `{"authorization_endpoint":"https://auth.x.ai/authorize","token_endpoint":"https://auth.x.ai/token"}`, nil})
				endpoint = "https://auth.x.ai/token"
			case "anthropic", "anthropic2":
				check = jsonValue("refresh_token", "refresh")
			case "nous":
				check = func(t *testing.T, r *http.Request, b []byte) {
					if r.Header.Get("x-nous-refresh-token") != "refresh" || strings.Contains(string(b), "refresh_token=refresh") {
						t.Fatal("Nous refresh must use header")
					}
				}
			case "kiro":
				endpoint = kiroSocial + "/refreshToken"
				body = `{"accessToken":"new","refreshToken":"rotated"}`
				check = jsonValue("refreshToken", "refresh")
			case "cursor":
				endpoint = "https://api2.cursor.sh/auth/exchange_user_api_key"
				body = `{"accessToken":"` + jwt(`{"sub":"user","exp":1700003600}`) + `"}`
				check = func(t *testing.T, r *http.Request, b []byte) {
					if r.Header.Get("Authorization") != "Bearer refresh" {
						t.Fatal("wrong Cursor refresh auth")
					}
				}
			case "github-copilot":
				steps = append(steps, responseStep{"GET", "https://api.github.com/copilot_internal/v2/token", 200, `{"token":"new","refresh_in":1500}`, nil}, responseStep{"GET", "https://api.github.com/user", 200, `{"id":123}`, nil})
			}
			if p != "github-copilot" {
				steps = append(steps, responseStep{"POST", endpoint, 200, body, check})
			}
			if p == "google-antigravity" {
				steps = append(steps, responseStep{"POST", "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist", 200, `{"projectId":"project"}`, nil})
			}
			a, _ := newMock(t, steps...)
			next, e := a.Refresh(context.Background(), testScope, p, c)
			if e != nil {
				t.Fatal(e)
			}
			if next.RefreshToken == "" || next.Scope != testScope {
				t.Fatal("refresh grant/owner lost")
			}
		})
	}
}
func TestNativeAndExplicitLogins(t *testing.T) {
	t.Run("zed", func(t *testing.T) {
		a, _ := newMock(t, responseStep{"GET", "https://cloud.zed.dev/client/users/me", 200, `{"default_organization_id":"zed-org"}`, func(t *testing.T, r *http.Request, b []byte) {
			if r.Header.Get("Authorization") != "zed-user zed-token" {
				t.Fatal("native auth header missing")
			}
		}}, responseStep{"POST", "https://cloud.zed.dev/client/llm_tokens", 200, `{"token":"llm-token"}`, jsonValue("organization_id", "zed-org")})
		ch, f, e := a.Start(context.Background(), testScope, "zed", StartOptions{RedirectURI: "http://127.0.0.1:54321/"})
		if e != nil {
			t.Fatal(e)
		}
		u, _ := url.Parse(ch.URL)
		der, _ := base64.RawURLEncoding.DecodeString(u.Query().Get("native_app_public_key"))
		key, e := x509.ParsePKCS1PublicKey(der)
		if e != nil {
			t.Fatal(e)
		}
		cipher, e := rsa.EncryptOAEP(sha256.New(), rand.Reader, key, []byte("zed-token"), nil)
		if e != nil {
			t.Fatal(e)
		}
		code := `{"user_id":"zed-user","access_token":"` + base64.RawURLEncoding.EncodeToString(cipher) + `"}`
		c, e := a.Complete(context.Background(), testScope, f, code, f.State)
		if e != nil || c.AccessToken != "llm-token" || c.AccountID != "zed-user" {
			t.Fatalf("native callback: %v", e)
		}
	})
	t.Run("devin", func(t *testing.T) {
		a, _ := newMock(t, responseStep{"POST", "https://register.windsurf.com/exa.seat_management_pb.SeatManagementService/RegisterUser", 200, `{"api_key":"devin-key","api_server_url":"https://eu.windsurf.com/_route/api_server"}`, jsonValue("firebase_id_token", strings.Repeat("a", 20))})
		ch, f, e := a.Start(context.Background(), testScope, "devin", StartOptions{})
		if e != nil || ch.URL == "" {
			t.Fatal(e)
		}
		c, e := a.Complete(context.Background(), testScope, f, strings.Repeat("a", 20), f.State)
		if e != nil || c.APIBaseURL != "https://eu.windsurf.com/_route/api_server" {
			t.Fatal(e)
		}
	})
	for _, p := range []string{"command-code", "dsh-account"} {
		t.Run(p, func(t *testing.T) {
			endpoint := "https://api.commandcode.ai/alpha/whoami"
			body := `{"user":{"id":"user"}}`
			if p == "dsh-account" {
				endpoint = "https://platform.deepseek.com/auth-api/v0/users/current"
				body = `{"code":0,"data":{"biz_code":0,"biz_data":{"id":"user"}}}`
			}
			a, _ := newMock(t, responseStep{"GET", endpoint, 200, body, nil})
			_, f, e := a.Start(context.Background(), testScope, p, StartOptions{Method: ExplicitToken})
			if e != nil {
				t.Fatal(e)
			}
			c, e := a.Complete(context.Background(), testScope, f, "explicit-token", f.State)
			if e != nil || c.AccountID != "user" {
				t.Fatal(e)
			}
		})
	}
}
func TestCursorBrowser(t *testing.T) {
	token := jwt(`{"sub":"cursor-user","exp":1700003600}`)
	a, m := newMock(t)
	ch, f, e := a.Start(context.Background(), testScope, "cursor", StartOptions{})
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(ch.URL)
	if u.Query().Get("challenge") != challenge(f.Verifier) {
		t.Fatal("challenge mismatch")
	}
	q := url.Values{"uuid": {f.Metadata["uuid"]}, "verifier": {f.Verifier}}
	m.steps = []responseStep{{"GET", "https://api2.cursor.sh/auth/poll?" + q.Encode(), 404, "", nil}, {"GET", "https://api2.cursor.sh/auth/poll?" + q.Encode(), 200, `{"accessToken":"` + token + `","refreshToken":"refresh"}`, nil}}
	due(a, &f)
	if _, e = a.Poll(context.Background(), testScope, &f); !errors.Is(e, ErrPending) {
		t.Fatal(e)
	}
	due(a, &f)
	c, e := a.Poll(context.Background(), testScope, &f)
	if e != nil || c.AccountID != "cursor-user" {
		t.Fatal(e)
	}
}
func TestWorkBuddyPersonalLoginAndRefresh(t *testing.T) {
	a, _ := newMock(t,
		responseStep{"POST", "https://www.workbuddy.ai/v2/plugin/auth/state?platform=workbuddy-ai", 200, `{"code":0,"data":{"state":"state","authUrl":"https://www.workbuddy.ai/login"}}`, nil},
		responseStep{"GET", "https://www.workbuddy.ai/v2/plugin/auth/token?state=state", 200, `{"code":0,"data":{"accessToken":"access","refreshToken":"refresh","expiresIn":3600,"domain":"www.workbuddy.ai"}}`, nil},
		responseStep{"GET", "https://www.workbuddy.ai/v2/plugin/login/account?state=state", 200, `{"code":0,"data":{"uid":"user","type":"personal"}}`, nil},
		responseStep{"POST", "https://www.workbuddy.ai/v2/plugin/auth/token/refresh", 200, `{"code":0,"data":{"accessToken":"new","refreshToken":"rotated","expiresIn":3600}}`, func(t *testing.T, r *http.Request, b []byte) {
			if r.Header.Get("X-Refresh-Token") != "refresh" || r.Header.Get("X-User-Id") != "user" {
				t.Fatal("WorkBuddy identity missing")
			}
		}})
	_, f, e := a.Start(context.Background(), testScope, "workbuddy-ai", StartOptions{})
	if e != nil {
		t.Fatal(e)
	}
	due(a, &f)
	c, e := a.Poll(context.Background(), testScope, &f)
	if e != nil {
		t.Fatal(e)
	}
	next, e := a.Refresh(context.Background(), testScope, "workbuddy-ai", c)
	if e != nil || next.RefreshToken != "rotated" || next.AccountID != c.AccountID {
		t.Fatal(e)
	}
}
func TestAuthGuardsAndSanitizedErrors(t *testing.T) {
	t.Run("scope-state-expiry-cancellation", func(t *testing.T) {
		a, _ := newMock(t)
		_, f, e := a.Start(context.Background(), testScope, "anthropic", StartOptions{})
		if e != nil {
			t.Fatal(e)
		}
		other := testScope
		other.Generation++
		if _, e = a.Complete(context.Background(), other, f, "code", f.State); !errors.Is(e, ErrScope) {
			t.Fatal(e)
		}
		if _, e = a.Complete(context.Background(), testScope, f, "code", "wrong"); !errors.Is(e, ErrFlow) {
			t.Fatal(e)
		}
		if _, e = a.Complete(context.Background(), testScope, f, "code#wrong", f.State); !errors.Is(e, ErrFlow) {
			t.Fatal(e)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, e = a.Complete(ctx, testScope, f, "code", f.State); !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
		f.ExpiresAt = a.now()
		if _, e = a.Complete(context.Background(), testScope, f, "code", f.State); !errors.Is(e, ErrFlow) {
			t.Fatal(e)
		}
	})
	for _, tc := range []struct {
		name, body string
		status     int
	}{{"terminal", `{"error":"invalid_grant","error_description":"SECRET"}`, 400}, {"unknown-error", `{"error":"SECRET"}`, 400}, {"missing-token", `{"refresh_token":"SECRET"}`, 200}, {"redirect", `SECRET`, 307}, {"oversized", strings.Repeat("SECRET", 50000), 200}, {"malformed", `SECRET`, 200}} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newMock(t, responseStep{"POST", "https://auth.openai.com/oauth/token", tc.status, tc.body, nil})
			_, e := a.Refresh(context.Background(), testScope, "openai", Credential{Scope: testScope, Provider: "openai", RefreshToken: "refresh"})
			if e == nil || strings.Contains(e.Error(), "SECRET") {
				t.Fatalf("unsafe or missing error: %v", e)
			}
		})
	}
	t.Run("phishing", func(t *testing.T) {
		a, _ := newMock(t, responseStep{"POST", "https://auth.kimi.com/api/oauth/device_authorization", 200, `{"device_code":"device","user_code":"ABCD","verification_uri":"https://evil.example/"}`, nil})
		if _, _, e := a.Start(context.Background(), testScope, "kimi", StartOptions{}); !errors.Is(e, ErrResponse) {
			t.Fatal(e)
		}
	})
	t.Run("foreign-refresh", func(t *testing.T) {
		a, _ := newMock(t)
		c := Credential{Scope: testScope, Provider: "openai", RefreshToken: "refresh"}
		c.Scope.UserID = "another"
		if _, e := a.Refresh(context.Background(), testScope, "openai", c); !errors.Is(e, ErrScope) {
			t.Fatal(e)
		}
	})
}

func TestGoogleOAuthRequiresDeploymentCredentials(t *testing.T) {
	for _, values := range [][2]string{{"", ""}, {"fixture-client", ""}, {"", "fixture-secret"}} {
		t.Run(values[0]+"/"+values[1], func(t *testing.T) {
			t.Setenv("AIPROUTER_GOOGLE_OAUTH_CLIENT_ID", values[0])
			t.Setenv("AIPROUTER_GOOGLE_OAUTH_CLIENT_SECRET", values[1])
			a, _ := newMock(t)
			_, _, err := a.Start(context.Background(), testScope, "google-antigravity", StartOptions{Method: PKCE, RedirectURI: "http://localhost:1455/auth/callback"})
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("missing credentials must refuse login: %v", err)
			}
			_, err = a.Refresh(context.Background(), testScope, "google-antigravity", Credential{Scope: testScope, Provider: "google-antigravity", RefreshToken: "fixture-refresh"})
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("missing credentials must refuse refresh: %v", err)
			}
		})
	}
}
