// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"net/url"
	"slices"
	"strings"
	"time"
)

const MCPManagementV1 WorkerCapability = "mcp-management-v1"
const MaxMCPDefinitions = 256
const MaxMCPDocumentBytes = 64 << 10

type MCPTransport string

const (
	MCPStdio          MCPTransport = "stdio"
	MCPStreamableHTTP MCPTransport = "streamable-http"
)

type MCPAuthenticationState string

const (
	MCPNoAuthentication       MCPAuthenticationState = "none"
	MCPAuthenticationRequired MCPAuthenticationState = "required"
	MCPAuthenticated          MCPAuthenticationState = "authenticated"
)

type MCPMutationState string

const (
	MCPPending          MCPMutationState = "pending"
	MCPSucceeded        MCPMutationState = "succeeded"
	MCPCanceled         MCPMutationState = "canceled"
	MCPRecoveryRequired MCPMutationState = "recovery-required"
	MCPFailed           MCPMutationState = "failed"
)

type MCPAction string

const (
	MCPList                 MCPAction = "list"
	MCPSave                 MCPAction = "save"
	MCPDelete               MCPAction = "delete"
	MCPInspectMutation      MCPAction = "inspect-mutation"
	MCPManualAuthentication MCPAction = "manual-authentication"
	MCPOAuthStart           MCPAction = "oauth-start"
	MCPOAuthCallback        MCPAction = "oauth-callback"
	MCPOAuthCancel          MCPAction = "oauth-cancel"
)

// A reference grants no runtime authority. Every new generation resolves the
// current Worker-owned definition and requires separately verified adapter support.
type MCPSelection struct {
	WorkerDeviceID ID     `json:"worker_device_id"`
	DefinitionID   ID     `json:"definition_id"`
	Unresolved     bool   `json:"unresolved,omitempty"`
	Name           string `json:"name,omitempty"`
}
type MCPSelectionList struct {
	Selections []MCPSelection `json:"selections"`
}
type MCPHarnessSupport struct {
	Harness  Harness `json:"harness"`
	Verified bool    `json:"verified"`
	Profile  string  `json:"profile,omitempty"`
}
type MCPOAuthConfiguration struct {
	AuthorizationURL string   `json:"authorization_url"`
	TokenURL         string   `json:"token_url"`
	ClientID         string   `json:"client_id"`
	RedirectURI      string   `json:"redirect_uri"`
	Scopes           []string `json:"scopes,omitempty"`
}
type MCPDefinition struct {
	ID                      ID                     `json:"id"`
	WorkerDeviceID          ID                     `json:"worker_device_id"`
	Revision                uint64                 `json:"revision,string"`
	Name                    string                 `json:"name"`
	Enabled                 bool                   `json:"enabled"`
	Transport               MCPTransport           `json:"transport"`
	Executable              string                 `json:"executable,omitempty"`
	Arguments               []string               `json:"arguments,omitempty"`
	WorkingDirectory        string                 `json:"working_directory,omitempty"`
	Endpoint                string                 `json:"endpoint,omitempty"`
	Authentication          MCPAuthenticationState `json:"authentication"`
	AuthenticationExpiresAt *time.Time             `json:"authentication_expires_at,omitempty"`
	OAuth                   *MCPOAuthConfiguration `json:"oauth,omitempty"`
	CredentialID            ID                     `json:"credential_id,omitempty"`
	Support                 []MCPHarnessSupport    `json:"support"`
	AgentReferences         []MCPAgentReference    `json:"agent_references,omitempty"`
}
type MCPAgentReference struct {
	ID   ID     `json:"id"`
	Name string `json:"name"`
}
type MCPSecrets struct {
	Environment  map[string]string `json:"environment,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	AccessToken  string            `json:"access_token,omitempty"`
	RefreshToken string            `json:"refresh_token,omitempty"`
	ExpiresAt    *time.Time        `json:"expires_at,omitempty"`
}
type MCPMutation struct {
	Version           uint32         `json:"version"`
	Action            MCPAction      `json:"action"`
	DefinitionID      ID             `json:"definition_id"`
	ExpectedRevision  uint64         `json:"expected_revision,string"`
	Definition        *MCPDefinition `json:"definition,omitempty"`
	Secrets           *MCPSecrets    `json:"secrets,omitempty"`
	Confirmed         bool           `json:"confirmed,omitempty"`
	OriginalRequestID ID             `json:"original_request_id,omitempty"`
	CallbackURI       string         `json:"callback_uri,omitempty"`
}

// Scope is assigned by the server and verified against the live authenticated
// Worker stream. It cannot be supplied by a management client.
type MCPRequest struct {
	ServerID         ID          `json:"server_id"`
	ActorID          ID          `json:"actor_id"`
	MachineID        ID          `json:"machine_id"`
	WorkerDeviceID   ID          `json:"worker_device_id"`
	WorkerInstanceID ID          `json:"worker_instance_id"`
	RequestID        ID          `json:"request_id"`
	Mutation         MCPMutation `json:"mutation"`
}
type MCPResult struct {
	DefinitionID     ID               `json:"definition_id,omitempty"`
	WorkerDeviceID   ID               `json:"worker_device_id"`
	RequestID        ID               `json:"request_id"`
	State            MCPMutationState `json:"state"`
	Definitions      []MCPDefinition  `json:"definitions"`
	AuthorizationURL string           `json:"authorization_url,omitempty"`
	ExpiresAt        *time.Time       `json:"expires_at,omitempty"`
	Problem          *Error           `json:"problem,omitempty"`
}

func MCPUnavailable() error {
	return Fail(Unavailable, "MCP management is unavailable on this Runner Device.", "Connect the owning supported Worker and inspect the original request.")
}
func ValidateMCPSelections(list *MCPSelectionList) error {
	if list == nil {
		return nil
	}
	if len(list.Selections) > MaxMCPDefinitions {
		return MCPUnavailable()
	}
	seen := map[ID]bool{}
	for _, v := range list.Selections {
		if Text(v.Name, "MCP selection name", 128, false) != nil || v.DefinitionID.Validate() != nil || (!v.Unresolved && v.WorkerDeviceID.Validate() != nil) || seen[v.DefinitionID] {
			return MCPUnavailable()
		}
		seen[v.DefinitionID] = true
	}
	return nil
}
func MCPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(raw) > 4096 {
		return Fail(InvalidArgument, "MCP requires a credential-free HTTPS endpoint.", "Enter an explicit endpoint without user credentials or a fragment.")
	}
	for key := range u.Query() {
		if strings.Contains(strings.ToLower(key), "token") || strings.Contains(strings.ToLower(key), "secret") || strings.Contains(strings.ToLower(key), "password") || slices.Contains([]string{"key", "api_key", "apikey", "authorization"}, strings.ToLower(key)) {
			return MCPUnavailable()
		}
	}
	return nil
}
func (v MCPDefinition) Validate() error {
	if v.ID.Validate() != nil || v.WorkerDeviceID.Validate() != nil || v.Revision == 0 || v.Revision >= 1<<63 || Text(v.Name, "MCP name", 128, true) != nil || len(v.Support) > 4 || len(v.AgentReferences) > MaxMCPDefinitions {
		return MCPUnavailable()
	}
	if v.Transport == MCPStdio {
		if Text(v.Executable, "MCP executable", 4096, true) != nil || Text(v.WorkingDirectory, "MCP directory", 4096, false) != nil || len(v.Arguments) > 128 || v.Endpoint != "" || v.OAuth != nil {
			return MCPUnavailable()
		}
		for _, arg := range v.Arguments {
			if Text(arg, "MCP argument", 4096, false) != nil {
				return MCPUnavailable()
			}
		}
	} else if v.Transport == MCPStreamableHTTP {
		if MCPURL(v.Endpoint) != nil || v.Executable != "" || len(v.Arguments) != 0 || v.WorkingDirectory != "" {
			return MCPUnavailable()
		}
	} else {
		return MCPUnavailable()
	}
	if !slices.Contains([]MCPAuthenticationState{MCPNoAuthentication, MCPAuthenticationRequired, MCPAuthenticated}, v.Authentication) || (v.CredentialID != "" && v.CredentialID.Validate() != nil) || (v.Authentication == MCPAuthenticated && v.CredentialID == "") {
		return MCPUnavailable()
	}
	seen := map[Harness]bool{}
	for _, support := range v.Support {
		if !support.Harness.Valid() || seen[support.Harness] || Text(support.Profile, "MCP profile", 128, false) != nil || support.Verified && support.Profile == "" {
			return MCPUnavailable()
		}
		seen[support.Harness] = true
	}
	for _, ref := range v.AgentReferences {
		if ref.ID.Validate() != nil || Text(ref.Name, "MCP Agent name", 256, true) != nil {
			return MCPUnavailable()
		}
	}
	if v.OAuth != nil {
		o := v.OAuth
		u, err := url.Parse(o.RedirectURI)
		if MCPURL(o.AuthorizationURL) != nil || MCPURL(o.TokenURL) != nil || Text(o.ClientID, "OAuth client ID", 256, true) != nil || err != nil || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost"))) || u.User != nil || u.Fragment != "" || len(o.Scopes) > 32 {
			return MCPUnavailable()
		}
		for _, s := range o.Scopes {
			if Text(s, "OAuth scope", 128, true) != nil {
				return MCPUnavailable()
			}
		}
	}
	return nil
}
func (v MCPMutation) Validate() error {
	if v.Version != 1 || !slices.Contains([]MCPAction{MCPList, MCPSave, MCPDelete, MCPInspectMutation, MCPManualAuthentication, MCPOAuthStart, MCPOAuthCallback, MCPOAuthCancel}, v.Action) {
		return MCPUnavailable()
	}
	if v.Action != MCPList && v.Action != MCPInspectMutation && v.DefinitionID.Validate() != nil {
		return MCPUnavailable()
	}
	if v.ExpectedRevision >= 1<<63 || v.Action == MCPDelete && !v.Confirmed || v.Action == MCPSave && v.Definition == nil || (v.Action == MCPOAuthCallback || v.Action == MCPOAuthCancel) && v.OriginalRequestID.Validate() != nil {
		return MCPUnavailable()
	}
	if v.Definition != nil && v.Action != MCPSave || v.Secrets != nil && v.Action != MCPManualAuthentication || v.CallbackURI != "" && v.Action != MCPOAuthCallback || v.OriginalRequestID != "" && v.Action != MCPOAuthCallback && v.Action != MCPOAuthCancel || len(v.CallbackURI) > 4096 {
		return MCPUnavailable()
	}
	if v.Secrets != nil {
		if len(v.Secrets.Environment) == 0 && len(v.Secrets.Headers) == 0 && v.Secrets.AccessToken == "" || strings.ContainsAny(v.Secrets.AccessToken+v.Secrets.RefreshToken, "\r\n") {
			return MCPUnavailable()
		}
		if len(v.Secrets.Environment) > 64 || len(v.Secrets.Headers) > 64 || Text(v.Secrets.AccessToken, "MCP token", 16384, false) != nil || Text(v.Secrets.RefreshToken, "MCP refresh token", 16384, false) != nil {
			return MCPUnavailable()
		}
		for _, values := range []map[string]string{v.Secrets.Environment, v.Secrets.Headers} {
			for k, s := range values {
				if Text(k, "MCP secret name", 128, true) != nil || strings.ContainsAny(k, "\r\n=:") || Text(s, "MCP secret value", 16384, false) != nil || strings.ContainsAny(s, "\r\n") {
					return MCPUnavailable()
				}
			}
		}
	}
	return nil
}
func (v MCPResult) Validate() error {
	if v.WorkerDeviceID.Validate() != nil || v.RequestID.Validate() != nil || !slices.Contains([]MCPMutationState{MCPPending, MCPSucceeded, MCPCanceled, MCPRecoveryRequired, MCPFailed}, v.State) || len(v.Definitions) > MaxMCPDefinitions {
		return MCPUnavailable()
	}
	if v.DefinitionID != "" && v.DefinitionID.Validate() != nil || v.AuthorizationURL != "" && (MCPURL(v.AuthorizationURL) != nil || v.ExpiresAt == nil || v.DefinitionID == "") {
		return MCPUnavailable()
	}
	seen := map[ID]bool{}
	for _, d := range v.Definitions {
		if d.Validate() != nil || d.WorkerDeviceID != v.WorkerDeviceID || seen[d.ID] {
			return MCPUnavailable()
		}
		seen[d.ID] = true
	}
	return nil
}
