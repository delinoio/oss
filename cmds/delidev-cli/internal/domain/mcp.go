// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"net/http"
	"net/url"
	"path"
	"strings"
)

type MCPTransport string

const (
	MCPStdio MCPTransport = "stdio"
	MCPHTTP  MCPTransport = "streamable-http"
)

type MCPAuthentication string

const (
	MCPAnonymous MCPAuthentication = "none"
	MCPManual    MCPAuthentication = "manual"
	MCPOAuth     MCPAuthentication = "oauth"
)

// MCPDefinition contains only configuration metadata. Secret values are write-only
// and remain in the original Worker's protected vault, never in this document.
type MCPDefinition struct {
	ID               ID                `json:"id"`
	Name             string            `json:"name"`
	Transport        MCPTransport      `json:"transport"`
	Executable       string            `json:"executable,omitempty"`
	Arguments        []string          `json:"arguments,omitempty"`
	Directory        string            `json:"directory,omitempty"`
	Endpoint         string            `json:"endpoint,omitempty"`
	EnvironmentNames []string          `json:"environment_names,omitempty"`
	HeaderNames      []string          `json:"header_names,omitempty"`
	Authentication   MCPAuthentication `json:"authentication"`
	Enabled          bool              `json:"enabled"`
	Revision         uint64            `json:"revision,string"`
}

type MCPSelection struct {
	MachineID ID     `json:"machine_id"`
	DeviceID  ID     `json:"device_id"`
	ServerID  ID     `json:"server_id"`
	Revision  uint64 `json:"revision,string"`
}
type MCPSelectionList struct {
	Selections []MCPSelection `json:"selections"`
}

func (s MCPSelectionList) Validate() error {
	if len(s.Selections) > 16 {
		return mcpInvalid()
	}
	seen := map[[3]ID]bool{}
	for _, v := range s.Selections {
		key := [3]ID{v.MachineID, v.DeviceID, v.ServerID}
		if v.MachineID.Validate() != nil || v.DeviceID.Validate() != nil || v.ServerID.Validate() != nil || v.Revision == 0 || seen[key] {
			return mcpInvalid()
		}
		seen[key] = true
	}
	return nil
}
func mcpInvalid() error {
	return Fail(InvalidArgument, "Invalid MCP configuration.", "Choose a supported transport and bounded non-secret configuration.")
}
func (v MCPDefinition) Validate() error {
	if v.ID.Validate() != nil || Text(v.Name, "MCP name", 128, true) != nil || len(v.Arguments) > 64 || len(v.EnvironmentNames) > 64 || len(v.HeaderNames) > 64 {
		return mcpInvalid()
	}
	for _, a := range v.Arguments {
		if Text(a, "MCP argument", 4096, false) != nil {
			return mcpInvalid()
		}
	}
	switch v.Transport {
	case MCPStdio:
		// Paths are interpreted and canonicalized on the selected Worker. A Windows
		// path must remain valid when the coordinating server runs on another OS.
		if Text(v.Executable, "MCP executable", 4096, true) != nil || (!path.IsAbs(v.Executable) && !windowsAbsolute(v.Executable)) || Text(v.Directory, "MCP directory", 4096, true) != nil || (!path.IsAbs(v.Directory) && !windowsAbsolute(v.Directory)) || v.Endpoint != "" || len(v.HeaderNames) != 0 || v.Authentication == MCPOAuth {
			return mcpInvalid()
		}
	case MCPHTTP:
		u, e := url.Parse(v.Endpoint)
		if e != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) || v.Executable != "" || len(v.Arguments) != 0 || v.Directory != "" || len(v.EnvironmentNames) != 0 || len(v.Endpoint) > 4096 {
			return mcpInvalid()
		}
	default:
		return mcpInvalid()
	}
	if v.Authentication != MCPAnonymous && v.Authentication != MCPManual && v.Authentication != MCPOAuth {
		return mcpInvalid()
	}
	seen := map[string]bool{}
	for _, n := range v.EnvironmentNames {
		if n == "" || len(n) > 128 || strings.ContainsAny(n, "=\x00\r\n") || seen[n] {
			return mcpInvalid()
		}
		seen[n] = true
	}
	seen = map[string]bool{}
	for _, n := range v.HeaderNames {
		key := http.CanonicalHeaderKey(n)
		if key == "" || len(n) > 128 || strings.ContainsAny(n, "\x00\r\n :") || seen[key] || key == "Host" || key == "Content-Length" || key == "Connection" {
			return mcpInvalid()
		}
		seen[key] = true
	}
	if v.Authentication == MCPAnonymous && (len(v.EnvironmentNames) > 0 || len(v.HeaderNames) > 0) {
		return mcpInvalid()
	}
	return nil
}
