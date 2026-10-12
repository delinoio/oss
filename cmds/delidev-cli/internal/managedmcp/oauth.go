// SPDX-License-Identifier: Apache-2.0
package managedmcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func fmtRevision(v uint64) string { return strconv.FormatUint(v, 10) }
func randomToken() (string, error) {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), e
}

type oauthPrivate struct {
	Verifier string `json:"verifier"`
}

func (m Manager) oauth(ctx context.Context, c *catalog, q domain.ManagedMCPRequest, d domain.ManagedMCPDefinition, result domain.ManagedMCPResult, save func() error) (domain.ManagedMCPResult, error) {
	if d.Authentication != domain.MCPOAuthAuthentication || d.OAuth == nil {
		return result, domain.Fail(domain.Unsupported, "This MCP definition has no OAuth profile.", "Configure the original server's public-client profile.")
	}
	if q.Action == domain.MCPOAuthBegin {
		verifier, e := randomToken()
		if e != nil {
			return result, e
		}
		state, e := randomToken()
		if e != nil {
			return result, e
		}
		challenge := sha256.Sum256([]byte(verifier))
		hash := sha256.Sum256([]byte(state))
		u, _ := url.Parse(d.OAuth.AuthorizationURL)
		params := u.Query()
		params.Set("resource", d.Endpoint)
		params.Set("response_type", "code")
		params.Set("client_id", d.OAuth.ClientID)
		params.Set("redirect_uri", d.OAuth.RedirectURI)
		params.Set("scope", d.OAuth.Scope)
		params.Set("state", state)
		params.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
		params.Set("code_challenge_method", "S256")
		u.RawQuery = params.Encode()
		secret, _ := json.Marshal(oauthPrivate{Verifier: verifier})
		defer clear(secret)
		if e = m.putSecret(ctx, d.ID, q.RequestID, secret); e != nil {
			return result, e
		}
		a := oauthAttempt{ActorID: q.ActorID, DefinitionID: d.ID, Revision: d.Revision, State: domain.MCPOperationAwaiting, ExpiresAt: m.now().Add(10 * time.Minute), AuthorizationURL: u.String(), StateDigest: hex.EncodeToString(hash[:])}
		c.Attempts[q.RequestID] = a
		result.Operation.State = a.State
		result.Operation.AuthorizationURL = a.AuthorizationURL
		result.Operation.ExpiresAt = a.ExpiresAt.Format(time.RFC3339Nano)
		return result, nil
	}
	a, ok := c.Attempts[q.AttemptID]
	if !ok || a.ActorID != q.ActorID || a.DefinitionID != d.ID || a.Revision != d.Revision {
		return result, domain.Fail(domain.PermissionDenied, "MCP authentication ownership changed.", "Use the original account-independent definition and requesting client.")
	}
	if m.Secrets == nil {
		return result, unavailable()
	}
	ref := credentials.Ref{Owner: d.ID, ID: q.AttemptID, Purpose: credentials.ManagedMCP}
	if q.Action == domain.MCPOAuthCancel {
		if !q.Confirmed || a.State != domain.MCPOperationAwaiting {
			return result, unavailable()
		}
		// Fence capture/exchange before credential cleanup. A cleanup failure retains
		// recovery instead of claiming that the original attempt was canceled.
		a.State = domain.MCPOperationRecovery
		c.Attempts[q.AttemptID] = a
		if e := save(); e != nil {
			return result, e
		}
		if e := m.Secrets.Delete(ctx, ref); e != nil {
			return result, e
		}
		a.State = domain.MCPOperationCanceled
		c.Attempts[q.AttemptID] = a
		result.Operation.State = domain.MCPOperationCanceled
		return result, nil
	}
	if a.State != domain.MCPOperationAwaiting || !m.now().Before(a.ExpiresAt) {
		return result, unavailable()
	}
	callback, _ := url.Parse(q.CallbackURL)
	code := callback.Query().Get("code")
	private, e := m.Secrets.Get(ctx, ref)
	if e != nil {
		return result, e
	}
	defer clear(private)
	var p oauthPrivate
	if domain.Decode(private, &p) != nil || len(p.Verifier) != 43 {
		return result, unavailable()
	}
	// Persist the no-resend boundary before the token request. A missing response
	// cannot authorize a second authorization-code exchange, even after restart.
	a.State = domain.MCPOperationStarted
	c.Attempts[q.AttemptID] = a
	if e = save(); e != nil {
		return result, e
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {d.OAuth.ClientID}, "redirect_uri": {d.OAuth.RedirectURI}, "code": {code}, "code_verifier": {p.Verifier}, "resource": {d.Endpoint}}
	attempt, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	request, e := http.NewRequestWithContext(attempt, http.MethodPost, d.OAuth.TokenURL, strings.NewReader(form.Encode()))
	if e != nil {
		return result, unavailable()
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if m.HTTP != nil {
		copy := *m.HTTP
		copy.CheckRedirect = client.CheckRedirect
		copy.Timeout = 20 * time.Second
		client = &copy
	}
	response, e := client.Do(request)
	if e != nil {
		a.State = domain.MCPOperationRecovery
		c.Attempts[q.AttemptID] = a
		_ = save()
		return result, unavailable()
	}
	defer response.Body.Close()
	b, e := io.ReadAll(io.LimitReader(response.Body, 65537))
	defer clear(b)
	var token struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token,omitempty"`
		ExpiresIn    *int64 `json:"expires_in,omitempty"`
		Scope        string `json:"scope,omitempty"`
	}
	if e != nil || len(b) > 65536 || response.StatusCode != http.StatusOK || json.Unmarshal(b, &token) != nil || token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") || domain.Text(token.AccessToken, "OAuth token", 8192, true) != nil || token.ExpiresIn != nil && (*token.ExpiresIn <= 0 || *token.ExpiresIn > 31536000) {
		a.State = domain.MCPOperationRecovery
		c.Attempts[q.AttemptID] = a
		_ = save()
		return result, unavailable()
	}
	// The sealed generation retains tokens and expiry privately. It never becomes
	// a list response, portable document or logged diagnostic.
	stored, _ := json.Marshal(struct {
		Token      any       `json:"token"`
		AcceptedAt time.Time `json:"accepted_at"`
	}{token, m.now()})
	defer clear(stored)
	if e = m.putSecret(ctx, d.ID, q.RequestID, stored); e != nil {
		return result, e
	}
	if e = m.Secrets.Delete(ctx, ref); e != nil {
		return result, e
	}
	d.CredentialID = q.RequestID
	if token.ExpiresIn != nil {
		d.CredentialExpiresAt = m.now().Add(time.Duration(*token.ExpiresIn) * time.Second)
	}
	d.Revision++
	c.Entries[d.ID] = entry{Definition: d}
	c.Generations[generationKey(d)] = d
	a.State = domain.MCPOperationCompleted
	c.Attempts[q.AttemptID] = a
	result.Definitions = []domain.ManagedMCPDefinition{d}
	result.Operation.State = domain.MCPOperationCompleted
	return result, nil
}

func validateCallback(raw string, d domain.ManagedMCPDefinition, a oauthAttempt) error {
	callback, e := url.Parse(raw)
	redirect, e2 := url.Parse(d.OAuth.RedirectURI)
	if e != nil || e2 != nil || callback.Scheme != redirect.Scheme || callback.Host != redirect.Host || callback.Path != redirect.Path || callback.User != nil || callback.Fragment != "" {
		return domain.Fail(domain.PermissionDenied, "The MCP callback belongs to another redirect.", "Use the original authorization callback.")
	}
	values := callback.Query()
	state := values.Get("state")
	code := values.Get("code")
	hash := sha256.Sum256([]byte(state))
	expected, e := hex.DecodeString(a.StateDigest)
	if e != nil || subtle.ConstantTimeCompare(hash[:], expected) != 1 || domain.Text(code, "OAuth code", 8192, true) != nil || len(values["state"]) != 1 || len(values["code"]) != 1 || values.Get("error") != "" {
		return domain.Fail(domain.PermissionDenied, "The MCP callback is invalid.", "Keep the original authentication attempt and callback state.")
	}
	return nil
}
