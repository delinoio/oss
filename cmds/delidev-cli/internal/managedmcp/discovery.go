// SPDX-License-Identifier: Apache-2.0
package managedmcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Discovery occurs only during the explicit OAuth action. It verifies the
// advertised resource and authorization authority before opening authorization;
// saving or inspecting a definition never contacts its configured endpoint.
func (m Manager) verifyOAuthMetadata(ctx context.Context, d domain.ManagedMCPDefinition) error {
	endpoint, e := url.Parse(d.Endpoint)
	if e != nil || d.OAuth == nil {
		return unavailable()
	}
	metadata := *endpoint
	metadata.Path = "/.well-known/oauth-protected-resource" + strings.TrimSuffix(endpoint.Path, "/")
	metadata.RawQuery = ""
	var resource struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if e = m.readOAuthMetadata(ctx, metadata.String(), &resource); e != nil || resource.Resource != d.Endpoint || len(resource.AuthorizationServers) == 0 || len(resource.AuthorizationServers) > 8 {
		return domain.Fail(domain.Unsupported, "MCP OAuth resource metadata is unavailable.", "Use the server's advertised resource and supported public-client profile.")
	}
	for _, issuer := range resource.AuthorizationServers {
		if domain.ValidateMCPURL(issuer) != nil {
			continue
		}
		u, e := url.Parse(issuer)
		if e != nil {
			continue
		}
		u.Path = "/.well-known/oauth-authorization-server" + strings.TrimSuffix(u.Path, "/")
		u.RawQuery = ""
		var server struct {
			Issuer                        string   `json:"issuer"`
			AuthorizationEndpoint         string   `json:"authorization_endpoint"`
			TokenEndpoint                 string   `json:"token_endpoint"`
			CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
		}
		if m.readOAuthMetadata(ctx, u.String(), &server) == nil && server.Issuer == issuer && server.AuthorizationEndpoint == d.OAuth.AuthorizationURL && server.TokenEndpoint == d.OAuth.TokenURL && slices.Contains(server.CodeChallengeMethodsSupported, "S256") {
			return nil
		}
	}
	return domain.Fail(domain.Unsupported, "The MCP authorization server does not match the selected profile.", "Use advertised authorization and token endpoints with S256 PKCE support.")
}
func (m Manager) readOAuthMetadata(ctx context.Context, raw string, target any) error {
	attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, e := http.NewRequestWithContext(attempt, http.MethodGet, raw, nil)
	if e != nil {
		return unavailable()
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if m.HTTP != nil {
		c := *m.HTTP
		c.Timeout = 5 * time.Second
		c.CheckRedirect = client.CheckRedirect
		client = &c
	}
	response, e := client.Do(request)
	if e != nil {
		return unavailable()
	}
	defer response.Body.Close()
	b, e := io.ReadAll(io.LimitReader(response.Body, 65537))
	defer clear(b)
	if e != nil || response.StatusCode != http.StatusOK || len(b) > 65536 || json.Unmarshal(b, target) != nil {
		return unavailable()
	}
	return nil
}
