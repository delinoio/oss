// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const ManagedMCPV1 WorkerCapability = "managed-mcp-v1"

type MCPTransport string

const (
	MCPStdio          MCPTransport = "stdio"
	MCPStreamableHTTP MCPTransport = "streamable-http"
)

type MCPAuthentication string

const (
	MCPNoAuthentication     MCPAuthentication = "none"
	MCPManualAuthentication MCPAuthentication = "manual"
	MCPOAuthAuthentication  MCPAuthentication = "oauth"
)

type MCPOAuthProfile struct {
	ClientID         string `json:"client_id"`
	AuthorizationURL string `json:"authorization_url"`
	TokenURL         string `json:"token_url"`
	RedirectURI      string `json:"redirect_uri"`
	Scope            string `json:"scope,omitempty"`
}

// Definitions contain no secret environment/header values or OAuth tokens.
// Each revision remains Worker-owned even after its current catalog entry retires.
type ManagedMCPDefinition struct {
	ID                  ID                `json:"id"`
	Revision            uint64            `json:"revision"`
	MachineID           ID                `json:"machine_id"`
	WorkerDeviceID      ID                `json:"worker_device_id"`
	Name                string            `json:"name"`
	Transport           MCPTransport      `json:"transport"`
	Command             string            `json:"command,omitempty"`
	Arguments           []string          `json:"arguments,omitempty"`
	Cwd                 string            `json:"cwd,omitempty"`
	Endpoint            string            `json:"endpoint,omitempty"`
	Enabled             bool              `json:"enabled"`
	Authentication      MCPAuthentication `json:"authentication"`
	OAuth               *MCPOAuthProfile  `json:"oauth,omitempty"`
	CredentialExpiresAt time.Time         `json:"credential_expires_at,omitempty"`
	CredentialID        ID                `json:"credential_id,omitempty"`
	SupportedHarnesses  []Harness         `json:"supported_harnesses"`
}

func ValidateMCPURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
		return Fail(InvalidArgument, "The MCP endpoint is invalid.", "Use HTTPS or an explicit loopback HTTP endpoint without credentials or fragments.")
	}
	return Text(raw, "MCP URL", 4096, true)
}
func (d ManagedMCPDefinition) Validate() error {
	if d.ID.Validate() != nil || d.MachineID.Validate() != nil || d.WorkerDeviceID.Validate() != nil || d.Revision == 0 || Text(d.Name, "MCP name", 256, true) != nil {
		return Fail(InvalidArgument, "The MCP definition identity is invalid.", "Use the selected Runner Device and original definition revision.")
	}
	if d.CredentialID != "" && d.CredentialID.Validate() != nil {
		return Fail(InvalidArgument, "The MCP credential reference is invalid.", "Authenticate the original definition again.")
	}
	switch d.Transport {
	case MCPStdio:
		if Text(d.Command, "MCP executable", 4096, true) != nil || !filepath.IsAbs(d.Command) || !filepath.IsAbs(d.Cwd) || d.Endpoint != "" || len(d.Arguments) > 128 {
			return Fail(InvalidArgument, "The stdio MCP definition is invalid.", "Choose an absolute executable and working directory, with at most 128 arguments.")
		}
		for _, v := range d.Arguments {
			if e := Text(v, "MCP argument", 4096, false); e != nil {
				return e
			}
		}
	case MCPStreamableHTTP:
		if e := ValidateMCPURL(d.Endpoint); e != nil {
			return e
		}
		if d.Command != "" || d.Cwd != "" || len(d.Arguments) > 0 {
			return Fail(InvalidArgument, "HTTP MCP cannot include a local command.", "Choose one transport.")
		}
	default:
		return Fail(InvalidArgument, "Unknown MCP transport.", "Choose stdio or Streamable HTTP.")
	}
	if d.Authentication != MCPNoAuthentication && d.Authentication != MCPManualAuthentication && d.Authentication != MCPOAuthAuthentication {
		return Fail(InvalidArgument, "Unknown MCP authentication.", "Choose none, manual credentials or OAuth.")
	}
	if d.Authentication == MCPOAuthAuthentication {
		if d.Transport != MCPStreamableHTTP || d.OAuth == nil || Text(d.OAuth.ClientID, "OAuth client", 256, true) != nil || ValidateMCPURL(d.OAuth.AuthorizationURL) != nil || ValidateMCPURL(d.OAuth.TokenURL) != nil || ValidateMCPURL(d.OAuth.RedirectURI) != nil || Text(d.OAuth.Scope, "OAuth scope", 2048, false) != nil {
			return Fail(InvalidArgument, "The MCP OAuth profile is incomplete.", "Use the server's explicit public-client OAuth profile.")
		}
	} else if d.OAuth != nil {
		return Fail(InvalidArgument, "OAuth profile does not match authentication.", "Remove the unused OAuth profile.")
	}
	seen := map[Harness]bool{}
	for _, h := range d.SupportedHarnesses {
		if !h.Valid() || seen[h] {
			return Fail(InvalidArgument, "The MCP support observation is invalid.", "Inspect the original native adapter.")
		}
		seen[h] = true
	}
	return nil
}

// Presence preserves old-client omission separately from explicit unselection.
type ManagedMCPSelections struct {
	Selections []ManagedMCPSelection `json:"selections"`
}
type ManagedMCPSelection struct {
	MachineID      ID `json:"machine_id"`
	WorkerDeviceID ID `json:"worker_device_id"`
	DefinitionID   ID `json:"definition_id"`
	// Imported definitions must be rebound on the destination Worker.
	Unresolved bool `json:"unresolved,omitempty"`
}

func (s *ManagedMCPSelections) Validate() error {
	if s == nil {
		return nil
	}
	if len(s.Selections) > 64 {
		return Fail(ResourceExhausted, "Too many MCP selections.", "Select at most 64 definitions.")
	}
	seen := map[string]bool{}
	for _, v := range s.Selections {
		key := string(v.WorkerDeviceID) + "/" + string(v.DefinitionID)
		if v.MachineID.Validate() != nil || v.WorkerDeviceID.Validate() != nil || v.DefinitionID.Validate() != nil || seen[key] {
			return Fail(InvalidArgument, "The MCP selection is invalid.", "Select each original Worker definition once.")
		}
		seen[key] = true
	}
	return nil
}
func (d ManagedMCPDefinition) Eligible(h Harness) bool {
	return d.Enabled && slices.Contains(d.SupportedHarnesses, h) && (d.Authentication == MCPNoAuthentication || d.CredentialID != "" && (d.CredentialExpiresAt.IsZero() || time.Now().Before(d.CredentialExpiresAt)))
}

type MCPSecretInput struct {
	Environment map[string]string `json:"environment,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
}

func (s MCPSecretInput) Validate(t MCPTransport) error {
	if len(s.Environment)+len(s.Headers) == 0 || len(s.Environment)+len(s.Headers) > 64 || t == MCPStdio && len(s.Headers) > 0 || t == MCPStreamableHTTP && len(s.Environment) > 0 {
		return Fail(InvalidArgument, "MCP credentials do not match the transport.", "Use environment values for stdio or headers for HTTP.")
	}
	for k, v := range s.Environment {
		if len(k) > 128 || k == "" || strings.ContainsAny(k, "=\x00") || Text(v, "MCP secret", 8192, true) != nil {
			return Fail(InvalidArgument, "Invalid MCP environment credential.", "Provide bounded names and values.")
		}
	}
	for k, v := range s.Headers {
		if k == "" || len(k) > 128 || strings.ContainsAny(k, "\r\n: \x00") || Text(v, "MCP secret", 8192, true) != nil || strings.ContainsAny(v, "\r\n") {
			return Fail(InvalidArgument, "Invalid MCP header credential.", "Provide bounded header names and values.")
		}
	}
	return nil
}

type ManagedMCPAction string

const (
	MCPList          ManagedMCPAction = "list"
	MCPSave          ManagedMCPAction = "save"
	MCPDelete        ManagedMCPAction = "delete"
	MCPAuthenticate  ManagedMCPAction = "authenticate"
	MCPOAuthBegin    ManagedMCPAction = "oauth-begin"
	MCPOAuthComplete ManagedMCPAction = "oauth-complete"
	MCPOAuthCancel   ManagedMCPAction = "oauth-cancel"
	MCPOperationRead ManagedMCPAction = "operation-read"
)

type ManagedMCPRequest struct {
	ServerID         ID                    `json:"server_id"`
	MachineID        ID                    `json:"machine_id"`
	WorkerDeviceID   ID                    `json:"worker_device_id"`
	WorkerInstanceID ID                    `json:"worker_instance_id"`
	ActorID          ID                    `json:"actor_id"`
	RequestID        ID                    `json:"request_id"`
	Action           ManagedMCPAction      `json:"action"`
	Definition       *ManagedMCPDefinition `json:"definition,omitempty"`
	DefinitionID     ID                    `json:"definition_id,omitempty"`
	ExpectedRevision uint64                `json:"expected_revision,omitempty"`
	Confirmed        bool                  `json:"confirmed,omitempty"`
	Referenced       bool                  `json:"referenced,omitempty"`
	Secrets          *MCPSecretInput       `json:"secrets,omitempty"`
	AttemptID        ID                    `json:"attempt_id,omitempty"`
	CallbackURL      string                `json:"callback_url,omitempty"`
}
type MCPOperationState string

const (
	MCPOperationStarted   MCPOperationState = "started"
	MCPOperationCompleted MCPOperationState = "completed"
	MCPOperationAwaiting  MCPOperationState = "awaiting-authorization"
	MCPOperationCanceled  MCPOperationState = "canceled"
	MCPOperationRecovery  MCPOperationState = "recovery-required"
	MCPOperationRejected  MCPOperationState = "rejected"
)

type ManagedMCPOperation struct {
	ID               ID                `json:"id"`
	DefinitionID     ID                `json:"definition_id"`
	State            MCPOperationState `json:"state"`
	AuthorizationURL string            `json:"authorization_url,omitempty"`
	ExpiresAt        string            `json:"expires_at,omitempty"`
}
type ManagedMCPResult struct {
	Definitions []ManagedMCPDefinition `json:"definitions"`
	Operation   *ManagedMCPOperation   `json:"operation,omitempty"`
	Replayed    bool                   `json:"replayed,omitempty"`
}
