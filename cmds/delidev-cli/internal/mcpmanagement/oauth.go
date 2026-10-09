// SPDX-License-Identifier: Apache-2.0
package mcpmanagement

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Only non-secret claim metadata is durable here. PKCE/state/code/token bytes
// remain in the original Worker's protected vault or bounded transient memory.
type oauthPhase string

type oauthAttempt struct {
	ID               domain.ID       `json:"id"`
	Actor            domain.ID       `json:"actor"`
	Server           domain.ID       `json:"server"`
	Revision         uint64          `json:"revision,string"`
	Callback         string          `json:"callback"`
	State            AuthState       `json:"state"`
	Phase            oauthPhase      `json:"phase"`
	Authorization    string          `json:"authorization,omitempty"`
	TokenEndpoint    string          `json:"token_endpoint,omitempty"`
	Resource         string          `json:"resource,omitempty"`
	ClientID         string          `json:"client_id,omitempty"`
	Secret           credentials.Ref `json:"secret"`
	Expires          time.Time       `json:"expires"`
	StartDigest      string          `json:"start_digest"`
	CompletionID     domain.ID       `json:"completion_id,omitempty"`
	CompletionDigest string          `json:"completion_digest,omitempty"`
}
type oauthPrivate struct {
	State        string    `json:"state"`
	Verifier     string    `json:"verifier"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

func randomValue() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func oauthURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Host != "" && u.User == nil && u.Fragment == "" && (u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))
}
func callbackURL(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Path != "/oauth/mcp/callback" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return false
	}
	p, e := strconv.Atoi(u.Port())
	return e == nil && p > 0 && p <= 65535 && strconv.Itoa(p) == u.Port()
}
func (m *Manager) oauthHTTP(ctx context.Context, method, endpoint, contentType string, body io.Reader, out any) error {
	if !oauthURL(endpoint) {
		return domain.Fail(domain.Unsupported, "Unsupported MCP OAuth endpoint.", "Use a protected endpoint with an accepted authorization profile.")
	}
	req, e := http.NewRequestWithContext(ctx, method, endpoint, body)
	if e != nil {
		return invalid()
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(req)
	if e != nil {
		return unavailable()
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return domain.Fail(domain.Unavailable, "MCP OAuth did not complete.", "Inspect the original authentication attempt without replaying its exchange.")
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, 64<<10+1))
	defer clear(raw)
	if e != nil || len(raw) > 64<<10 || json.Unmarshal(raw, out) != nil {
		return unavailable()
	}
	return nil
}
func (m *Manager) Authenticate(ctx context.Context, actor domain.ID, req *pb.AuthenticateMcpServerRequest) (*pb.AuthenticateMcpServerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failed || req == nil || len(req.CallbackUrl) > 16<<10 || req.Mutation == nil || actor.Validate() != nil || domain.ID(req.Mutation.RequestId).Validate() != nil || domain.ID(req.Mutation.Id).Validate() != nil {
		return nil, invalid()
	}
	id := domain.ID(req.Mutation.Id)
	s, exists := m.state.Servers[id]
	if !exists || s.Retired || s.Deleting != nil {
		return nil, conflict()
	}
	g := s.Generations[s.Current]
	if req.Operation != pb.McpAuthenticationOperation_MCP_AUTHENTICATION_OPERATION_CANCEL && (g.Entry.Definition.Authentication != domain.MCPOAuth || g.Entry.Definition.Transport != domain.MCPHTTP) {
		return nil, domain.Fail(domain.Unsupported, "This MCP definition does not use OAuth.", "Use its explicitly selected authentication method.")
	}
	requestID := domain.ID(req.Mutation.RequestId)
	raw, _ := json.Marshal(req)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	defer clear(raw)
	response := &pb.AuthenticateMcpServerResponse{RequestId: string(requestID)}
	if m.state.OAuth == nil {
		m.state.OAuth = map[domain.ID]oauthAttempt{}
	}
	if req.Operation == pb.McpAuthenticationOperation_MCP_AUTHENTICATION_OPERATION_START {
		if old, ok := m.state.OAuth[requestID]; ok {
			if old.Actor != actor || old.Server != id || old.StartDigest != digest {
				return nil, conflict()
			}
			response.AttemptId = string(old.ID)
			response.State = authToWire(old.State)
			response.AuthorizationUrl = old.Authorization
			if old.State == Pending {
				raw, e := m.vault.Get(ctx, old.Secret)
				if e != nil {
					return nil, e
				}
				var p oauthPrivate
				if domain.Decode(raw, &p) != nil {
					return nil, unavailable()
				}
				u, e := url.Parse(old.Authorization)
				if e != nil {
					return nil, unavailable()
				}
				q := u.Query()
				q.Set("state", p.State)
				u.RawQuery = q.Encode()
				response.AuthorizationUrl = u.String()
			}
			response.Replayed = true
			return response, nil
		}
		for _, prior := range m.state.OAuth {
			if prior.Server == id && (prior.State == Pending || prior.State == Uncertain) {
				return nil, unavailable()
			}
		}
		if s.Current != req.Mutation.ExpectedRevision || !callbackURL(req.CallbackUrl) || req.AttemptId != "" || len(m.state.OAuth) >= 256 {
			return nil, invalid()
		}
		// Every external registration is preceded by an immutable claim. Restart
		// never repeats a registration or token exchange whose outcome is unknown.
		a := oauthAttempt{ID: requestID, Actor: actor, Server: id, Revision: s.Current, Callback: req.CallbackUrl, State: Uncertain, Phase: "registration-started", Secret: credentials.Ref{Owner: id, ID: requestID, Purpose: credentials.MCPServer}, Expires: time.Now().Add(10 * time.Minute), StartDigest: digest}
		m.state.OAuth[requestID] = a
		if m.logger != nil {
			m.logger.InfoContext(ctx, "mcp_oauth_claim", "operation_id", requestID, "server_id", id, "phase", a.Phase)
		}
		if e := m.persist(); e != nil {
			return nil, e
		}
		private := oauthPrivate{State: randomValue(), Verifier: randomValue()}
		secret, _ := json.Marshal(private)
		defer clear(secret)
		if _, e := m.vault.Put(ctx, a.Secret, secret); e != nil {
			return nil, e
		}
		endpoint, _ := url.Parse(g.Entry.Definition.Endpoint)
		discovery := endpoint.Scheme + "://" + endpoint.Host + "/.well-known/oauth-protected-resource" + strings.TrimSuffix(endpoint.Path, "/")
		var resource struct {
			Resource string   `json:"resource"`
			Servers  []string `json:"authorization_servers"`
		}
		if e := m.oauthHTTP(ctx, http.MethodGet, discovery, "", nil, &resource); e != nil {
			return nil, e
		}
		if resource.Resource != g.Entry.Definition.Endpoint || len(resource.Servers) != 1 || !oauthURL(resource.Servers[0]) {
			return nil, domain.Fail(domain.Unsupported, "MCP authorization discovery is ambiguous.", "Use an endpoint with one exact protected-resource issuer.")
		}
		issuer, _ := url.Parse(resource.Servers[0])
		metadata := issuer.Scheme + "://" + issuer.Host + "/.well-known/oauth-authorization-server" + strings.TrimSuffix(issuer.Path, "/")
		var authorization struct {
			Issuer        string   `json:"issuer"`
			Authorization string   `json:"authorization_endpoint"`
			Token         string   `json:"token_endpoint"`
			Registration  string   `json:"registration_endpoint"`
			Challenge     []string `json:"code_challenge_methods_supported"`
		}
		if e := m.oauthHTTP(ctx, http.MethodGet, metadata, "", nil, &authorization); e != nil {
			return nil, e
		}
		s256 := false
		for _, v := range authorization.Challenge {
			if v == "S256" {
				s256 = true
			}
		}
		if authorization.Issuer != resource.Servers[0] || !s256 || !oauthURL(authorization.Authorization) || !oauthURL(authorization.Token) || !oauthURL(authorization.Registration) {
			return nil, domain.Fail(domain.Unsupported, "MCP OAuth metadata is unsupported.", "Use PKCE S256 and an explicit public-client registration endpoint.")
		}
		registration, _ := json.Marshal(map[string]any{"client_name": "DeliDev MCP", "redirect_uris": []string{a.Callback}, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": "none"})
		var registered struct {
			ID     string `json:"client_id"`
			Method string `json:"token_endpoint_auth_method"`
			Secret string `json:"client_secret"`
		}
		if e := m.oauthHTTP(ctx, http.MethodPost, authorization.Registration, "application/json", strings.NewReader(string(registration)), &registered); e != nil {
			return nil, e
		}
		if registered.ID == "" || len(registered.ID) > 1024 || registered.Secret != "" || registered.Method != "" && registered.Method != "none" {
			return nil, domain.Fail(domain.Unsupported, "The MCP issuer did not register a public client.", "Use an issuer supporting the original PKCE public-client profile.")
		}
		u, _ := url.Parse(authorization.Authorization)
		q := u.Query()
		challenge := sha256.Sum256([]byte(private.Verifier))
		q.Set("client_id", registered.ID)
		q.Set("redirect_uri", a.Callback)
		q.Set("response_type", "code")
		q.Set("state", private.State)
		q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
		q.Set("code_challenge_method", "S256")
		q.Set("resource", resource.Resource)
		u.RawQuery = q.Encode()
		a.State = Pending
		a.Phase = "awaiting-callback"
		q.Del("state")
		u.RawQuery = q.Encode()
		a.Authorization = u.String()
		a.TokenEndpoint = authorization.Token
		a.ClientID = registered.ID
		a.Resource = resource.Resource
		m.state.OAuth[a.ID] = a
		if e := m.persist(); e != nil {
			return nil, e
		}
		response.AttemptId = string(a.ID)
		response.State = authToWire(a.State)
		q.Set("state", private.State)
		u.RawQuery = q.Encode()
		response.AuthorizationUrl = u.String()
		return response, nil
	}
	attemptID := domain.ID(req.AttemptId)
	a, ok := m.state.OAuth[attemptID]
	if !ok || a.Actor != actor || a.Server != id || a.Revision != req.Mutation.ExpectedRevision {
		return nil, conflict()
	}
	response.AttemptId = string(attemptID)
	if a.CompletionID == requestID && a.Phase != "cancel-started" {
		if a.CompletionDigest != digest {
			return nil, conflict()
		}
		response.Replayed = true
		response.State = authToWire(a.State)
		return response, nil
	}
	if req.Operation == pb.McpAuthenticationOperation_MCP_AUTHENTICATION_OPERATION_CANCEL {
		if a.State == Ready || a.Phase == "exchange-started" || a.State == Uncertain {
			return nil, unavailable()
		}
		if a.Phase == "cancel-started" && (a.CompletionID != requestID || a.CompletionDigest != digest) {
			return nil, conflict()
		}
		a.Phase = "cancel-started"
		a.CompletionID = requestID
		a.CompletionDigest = digest
		m.state.OAuth[a.ID] = a
		if e := m.persist(); e != nil {
			return nil, e
		}
		if e := m.vault.Delete(ctx, a.Secret); e != nil {
			return nil, e
		}
		a.State = Canceled
		a.Phase = "canceled"
		a.Authorization = ""
		a.CompletionID = requestID
		a.CompletionDigest = digest
		m.state.OAuth[a.ID] = a
		if e := m.persist(); e != nil {
			return nil, e
		}
		response.State = authToWire(a.State)
		return response, nil
	}
	if req.Operation != pb.McpAuthenticationOperation_MCP_AUTHENTICATION_OPERATION_COMPLETE || a.State != Pending || a.Phase != "awaiting-callback" || time.Now().After(a.Expires) || s.Current != a.Revision {
		return nil, unavailable()
	}
	privateRaw, e := m.vault.Get(ctx, a.Secret)
	defer clear(privateRaw)
	if e != nil {
		return nil, e
	}
	var private oauthPrivate
	if domain.Decode(privateRaw, &private) != nil {
		return nil, unavailable()
	}
	callback, e := url.Parse(req.CallbackUrl)
	original, _ := url.Parse(a.Callback)
	if e != nil || callback.Scheme != original.Scheme || callback.Host != original.Host || callback.EscapedPath() != original.EscapedPath() || callback.Fragment != "" || callback.User != nil || len(callback.Query()["state"]) != 1 || len(callback.Query()["code"]) != 1 || len(callback.Query()) != 2 || subtle.ConstantTimeCompare([]byte(callback.Query().Get("state")), []byte(private.State)) != 1 || callback.Query().Get("code") == "" {
		return nil, invalid()
	}
	a.State = Uncertain
	a.Phase = "exchange-started"
	if m.logger != nil {
		m.logger.InfoContext(ctx, "mcp_oauth_claim", "operation_id", requestID, "server_id", id, "phase", a.Phase)
	}
	a.CompletionID = requestID
	a.CompletionDigest = digest
	a.Authorization = ""
	m.state.OAuth[a.ID] = a
	if e = m.persist(); e != nil {
		return nil, e
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {callback.Query().Get("code")}, "code_verifier": {private.Verifier}, "client_id": {a.ClientID}, "redirect_uri": {a.Callback}, "resource": {a.Resource}}
	var token struct {
		Access  string      `json:"access_token"`
		Refresh string      `json:"refresh_token"`
		Type    string      `json:"token_type"`
		Expiry  json.Number `json:"expires_in"`
	}
	if e = m.oauthHTTP(ctx, http.MethodPost, a.TokenEndpoint, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), &token); e != nil {
		return nil, e
	}
	expiry, e := strconv.ParseInt(string(token.Expiry), 10, 64)
	if e != nil || expiry <= 0 || expiry > 365*24*60*60 || !strings.EqualFold(token.Type, "Bearer") || token.Access == "" || strings.ContainsAny(token.Access, "\r\n\x00") || len(token.Access) > 16<<10 || len(token.Refresh) > 16<<10 {
		return nil, unavailable()
	}
	credential := Secrets{Headers: map[string]string{"Authorization": "Bearer " + token.Access}, OAuth: &OAuthToken{Refresh: token.Refresh, ExpiresAt: time.Now().Add(time.Duration(expiry) * time.Second)}}
	ref := credentials.Ref{Owner: id, ID: requestID, Purpose: credentials.MCPServer}
	secret, _ := json.Marshal(credential)
	defer clear(secret)
	m.state.Prepared[requestID] = preparation{Actor: actor, Digest: digest, Reference: ref}
	if e = m.persist(); e != nil {
		return nil, e
	}
	if _, e = m.vault.Put(ctx, ref, secret); e != nil {
		return nil, e
	}
	g.Entry.Definition.Revision++
	g.Entry.Authentication = Ready
	g.Entry.ExpiresAt = credential.OAuth.ExpiresAt
	g.Secret = &ref
	s.Current = g.Entry.Definition.Revision
	s.Generations[s.Current] = g
	m.state.Servers[id] = s
	a.State = Ready
	a.Phase = "complete"
	m.state.OAuth[a.ID] = a
	if e = m.persist(); e != nil {
		return nil, e
	}
	if e = m.vault.Delete(ctx, a.Secret); e != nil {
		return nil, e
	}
	response.State = authToWire(a.State)
	return response, nil
}

// AuthenticationMetadata projects only safe original-attempt ownership. Another
// actor can observe the busy state but cannot adopt its callback or cancel it.
func (m *Manager) AuthenticationMetadata(actor, server domain.ID) (AuthState, domain.ID, uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var newest oauthAttempt
	for _, a := range m.state.OAuth {
		if a.Server == server && (a.State == Pending || a.State == Uncertain) && (newest.ID == "" || string(a.ID) > string(newest.ID)) {
			newest = a
		}
	}
	if newest.ID == "" {
		return "", "", 0
	}
	if newest.Actor != actor {
		return newest.State, "", 0
	}
	return newest.State, newest.ID, newest.Revision
}
