// Package github implements the GitHub.com read-only integration adapter.
package github

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
)

const apiOrigin = "https://api.github.com"
const apiVersion = "2026-03-10"

type Client struct{ http *http.Client }

// New has no configurable authority or ambient credential/proxy lookup. Each
// request is bounded and redirect refusal prevents token forwarding elsewhere.
func New(routing ...outbound.Resolver) *Client {
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: -1}).DialContext,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
		MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true, DisableCompression: true,
	}
	var rt http.RoundTripper = transport
	if len(routing) > 0 {
		rt = &outbound.Transport{Base: transport, Resolve: routing[0]}
	}
	return &Client{http: &http.Client{Transport: rt, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type IdentityObservation struct {
	State    domain.IntegrationValidationState
	Identity *domain.GitHubIdentity
	Problem  *domain.Error
}

func unavailable() IdentityObservation {
	return IdentityObservation{State: domain.IntegrationUnavailable, Problem: domain.Fail(domain.Unavailable, "GitHub identity could not be verified.", "Check the server's network connection and retry validation; repository access remains independently unverified.")}
}

// Identity retrieves only authenticated public identity. It asks for no email or
// private-user scope, copies only stable ID/node ID/login, and grants no access
// claim for any repository, pull request, issue, ruleset, check or reviewer.
func (c *Client) Identity(ctx context.Context, token []byte) (IdentityObservation, error) {
	if err := credentials.ValidatePAT(token); err != nil {
		return IdentityObservation{}, err
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(bounded, http.MethodGet, apiOrigin+"/user", nil)
	if err != nil {
		return unavailable(), nil
	}
	req.Header.Set("Authorization", "Bearer "+string(token))
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", "DeliDev/0.1.0")
	defer req.Header.Del("Authorization")
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return IdentityObservation{}, domain.SafeError(ctx.Err())
		}
		return unavailable(), nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return identityFailure(response), nil
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10+1))
	if err != nil || len(raw) > 64<<10 {
		return unavailable(), nil
	}
	// GitHub adds response fields over time. Validate complete JSON for duplicate
	// keys/UTF-8, then extract only this adapter's explicitly retained identity.
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &fields) != nil {
		return unavailable(), nil
	}
	var id uint64
	var identity domain.GitHubIdentity
	var kind string
	if json.Unmarshal(fields["id"], &id) != nil || json.Unmarshal(fields["node_id"], &identity.NodeID) != nil || json.Unmarshal(fields["login"], &identity.Login) != nil || json.Unmarshal(fields["type"], &kind) != nil || kind != "User" {
		return unavailable(), nil
	}
	identity.ID = strconv.FormatUint(id, 10)
	if identity.Validate() != nil {
		return unavailable(), nil
	}
	if ctx.Err() != nil {
		return IdentityObservation{}, domain.SafeError(ctx.Err())
	}
	return IdentityObservation{State: domain.IntegrationIdentityVerified, Identity: &identity}, nil
}

func identityFailure(response *http.Response) IdentityObservation {
	if response.StatusCode == http.StatusUnauthorized {
		return IdentityObservation{State: domain.IntegrationInvalidToken, Problem: domain.Fail(domain.Unauthenticated, "GitHub rejected the personal access token.", "Replace an invalid, expired or revoked token; no other credential is selected.")}
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0" {
		return IdentityObservation{State: domain.IntegrationRateLimited, Problem: domain.Fail(domain.ResourceExhausted, "GitHub rate-limited the validation request.", "Wait for the applicable GitHub rate limit to reset, then validate again.")}
	}
	if response.StatusCode == http.StatusForbidden {
		// Preserve the known SSO classification without retaining/following the
		// response's private organization URL or treating other 403s as SSO.
		if strings.TrimSpace(strings.SplitN(response.Header.Get("X-GitHub-SSO"), ";", 2)[0]) == "required" {
			return IdentityObservation{State: domain.IntegrationSSORequired, Problem: domain.Fail(domain.ConfirmationRequired, "GitHub requires organization SSO authorization.", "Authorize this token in GitHub's official settings, then validate it again.")}
		}
		return IdentityObservation{State: domain.IntegrationAccessRestricted, Problem: domain.Fail(domain.PermissionDenied, "GitHub restricted access for this token.", "Check token permissions and organization approval/SSO requirements in GitHub; the exact cause is not independently known.")}
	}
	return unavailable()
}
