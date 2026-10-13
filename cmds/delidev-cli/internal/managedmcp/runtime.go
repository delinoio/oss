// SPDX-License-Identifier: Apache-2.0
package managedmcp

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// RuntimeServer is one execution-owned native MCP server. It exists only in
// memory: protected values are read from the Worker vault for this execution and
// are never returned by catalog reads, persisted or logged.
type RuntimeServer struct {
	Definition domain.ManagedMCPDefinition
	// Name is the stable private native server identity.
	Name string
	// Environment holds stdio launch values. Headers holds Streamable HTTP
	// values. BearerToken is an OAuth access token for Streamable HTTP.
	Environment map[string]string
	Headers     map[string]string
	BearerToken string
}

// Identity binds native observations to the accepted generation.
func (r RuntimeServer) Identity() domain.MCPServerIdentity {
	return domain.MCPServerIdentity{DefinitionID: r.Definition.ID, Revision: r.Definition.Revision, Server: r.Name}
}

// ProtectedValues lists every secret byte sequence that must be redacted from
// diagnostics, even if a native child echoes it.
func (r RuntimeServer) ProtectedValues() []string {
	values := []string{}
	for _, v := range r.Environment {
		values = append(values, v)
	}
	for _, v := range r.Headers {
		values = append(values, v)
	}
	if r.BearerToken != "" {
		values = append(values, r.BearerToken)
	}
	return values
}

// Clear overwrites the in-memory secret maps. Go strings are immutable, so the
// maps are dropped and the owner must stop retaining the resolved value.
func (r *RuntimeServer) Clear() {
	clear(r.Environment)
	clear(r.Headers)
	r.Environment, r.Headers, r.BearerToken = nil, nil, ""
}

func runtimeUnavailable() error {
	return domain.Fail(domain.Unsupported, "A selected MCP generation is unavailable on this Worker.", "Keep the original selection and re-authenticate or reselect the definition for a future execution; the saved definition was not changed.")
}

func withoutSupport(d domain.ManagedMCPDefinition) domain.ManagedMCPDefinition {
	d.SupportedHarnesses = nil
	return d
}

// Resolve verifies every execution-frozen generation against this Worker's own
// immutable catalog and reads its protected credential. It never launches a
// server, logs in, refreshes a token or follows a newer catalog revision.
func (m Manager) Resolve(ctx context.Context, definitions []domain.ManagedMCPDefinition, harness domain.Harness) ([]RuntimeServer, error) {
	if len(definitions) == 0 {
		return nil, nil
	}
	if len(definitions) > 64 {
		return nil, domain.Fail(domain.ResourceExhausted, "Too many selected MCP generations.", "Select at most 64 definitions.")
	}
	root := filepath.Join(m.Root, "managed-mcp", string(m.ServerID), string(m.WorkerDeviceID))
	if err := security.PrivateDir(root); err != nil {
		return nil, err
	}
	var lock *security.Lock
	var err error
	// Management writes hold the catalog lock only briefly. Waiting here is
	// bounded so startup fails typed instead of racing a catalog mutation.
	for attempt := 0; attempt < 40; attempt++ {
		lock, err = security.TryLock(filepath.Join(root, "catalog.lock"))
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, domain.SafeError(ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	if err != nil {
		return nil, domain.Fail(domain.Conflict, "MCP management is busy.", "Wait for the original Worker operation, then retry the execution.")
	}
	defer lock.Close()
	raw, err := security.ReadPrivate(filepath.Join(root, "catalog.json"), 16<<20)
	if err != nil {
		return nil, runtimeUnavailable()
	}
	var c catalog
	if domain.DecodeWithLimit(raw, &c, 16<<20) != nil || c.Version != 1 || c.ServerID != m.ServerID || c.MachineID != m.MachineID || c.WorkerDeviceID != m.WorkerDeviceID || c.Generations == nil {
		return nil, runtimeUnavailable()
	}
	now := m.now()
	seen := map[domain.ID]bool{}
	names := map[string]bool{}
	result := make([]RuntimeServer, 0, len(definitions))
	fail := func(err error) ([]RuntimeServer, error) {
		for i := range result {
			result[i].Clear()
		}
		return nil, err
	}
	for _, d := range definitions {
		name := d.NativeServerName()
		if d.Validate() != nil || !d.Eligible(harness) || d.MachineID != m.MachineID || d.WorkerDeviceID != m.WorkerDeviceID || seen[d.ID] || names[name] {
			return fail(runtimeUnavailable())
		}
		seen[d.ID], names[name] = true, true
		original, exists := c.Generations[generationKey(d)]
		if !exists {
			return fail(runtimeUnavailable())
		}
		expected, e1 := json.Marshal(withoutSupport(original))
		actual, e2 := json.Marshal(withoutSupport(d))
		if e1 != nil || e2 != nil || string(expected) != string(actual) {
			return fail(runtimeUnavailable())
		}
		server := RuntimeServer{Definition: d, Name: name}
		if d.Authentication != domain.MCPNoAuthentication {
			if d.CredentialID == "" || !d.CredentialExpiresAt.IsZero() && !now.Before(d.CredentialExpiresAt) {
				return fail(runtimeUnavailable())
			}
			if m.Secrets == nil {
				return fail(domain.Fail(domain.Unavailable, "Worker credential storage is unavailable.", "Unlock the selected Worker's native credential store."))
			}
			secret, err := m.Secrets.Get(ctx, credentials.Ref{Owner: d.ID, ID: d.CredentialID, Purpose: credentials.ManagedMCP})
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return fail(domain.SafeError(err))
				}
				return fail(runtimeUnavailable())
			}
			if err := server.decodeSecret(secret); err != nil {
				clear(secret)
				return fail(err)
			}
			clear(secret)
		}
		result = append(result, server)
	}
	if m.Logger != nil {
		m.Logger.InfoContext(ctx, "managed_mcp_runtime_resolved", "machine_id", m.MachineID, "worker_device_id", m.WorkerDeviceID, "count", len(result), "harness", harness)
	}
	return result, nil
}

func (r *RuntimeServer) decodeSecret(secret []byte) error {
	switch r.Definition.Authentication {
	case domain.MCPManualAuthentication:
		var value domain.MCPSecretInput
		if domain.DecodeWithLimit(secret, &value, credentials.MaxSecretBytes) != nil || value.Validate(r.Definition.Transport) != nil {
			return runtimeUnavailable()
		}
		r.Environment, r.Headers = value.Environment, value.Headers
	case domain.MCPOAuthAuthentication:
		var value struct {
			Token struct {
				AccessToken string `json:"access_token"`
				TokenType   string `json:"token_type"`
			} `json:"token"`
		}
		if len(secret) > credentials.MaxSecretBytes || json.Unmarshal(secret, &value) != nil || value.Token.AccessToken == "" || domain.Text(value.Token.AccessToken, "OAuth token", 8192, true) != nil || r.Definition.Transport != domain.MCPStreamableHTTP {
			return runtimeUnavailable()
		}
		r.BearerToken = value.Token.AccessToken
	default:
		return runtimeUnavailable()
	}
	return nil
}
