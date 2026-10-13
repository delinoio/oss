// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
)

// CodexManagedMCPV1 is advertised by a Worker whose Codex adapter loads the
// selected managed MCP generations, verifies the exact native inventory before
// input and publishes typed call and elicitation observations. It never grants
// authority by itself: each execution still verifies its installed profile.
const CodexManagedMCPV1 WorkerCapability = "codex-managed-mcp-v1"

// ManagedMCPSupport derives the harnesses whose native adapter was published by
// the original authenticated Worker. Definitions and clients cannot supply it.
func ManagedMCPSupport(capabilities []WorkerCapability) []Harness {
	if slices.Contains(capabilities, CodexManagedMCPV1) {
		return []Harness{Codex}
	}
	return nil
}

// NativeServerName is the stable private native identity of a definition. It is
// independent of the immutable revision so tool names survive future generations,
// and it embeds a digest of the definition identity so two display names can
// never collide in one native configuration.
func (d ManagedMCPDefinition) NativeServerName() string {
	slug := make([]byte, 0, 24)
	for _, r := range strings.ToLower(d.Name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			slug = append(slug, byte(r))
		case len(slug) > 0 && slug[len(slug)-1] != '_':
			slug = append(slug, '_')
		}
		if len(slug) >= 24 {
			break
		}
	}
	prefix := strings.Trim(string(slug), "_")
	if prefix == "" {
		prefix = "server"
	}
	digest := sha256.Sum256([]byte(d.ID))
	return prefix + "_" + hex.EncodeToString(digest[:5])
}

// MCPStartupOutcome is a closed native startup classification for one selected
// server. Diagnostics and native error text are deliberately not part of it.
type MCPStartupOutcome string

const (
	MCPStartupReady                  MCPStartupOutcome = "ready"
	MCPStartupFailed                 MCPStartupOutcome = "failed"
	MCPStartupAuthenticationRequired MCPStartupOutcome = "authentication-required"
	MCPStartupCancelled              MCPStartupOutcome = "cancelled"
	MCPStartupPending                MCPStartupOutcome = "pending"
)

// MCPServerIdentity binds a native observation to the exact accepted generation.
type MCPServerIdentity struct {
	DefinitionID ID     `json:"definition_id"`
	Revision     uint64 `json:"revision"`
	Server       string `json:"server"`
}

func (i MCPServerIdentity) Validate() error {
	if i.DefinitionID.Validate() != nil || i.Revision == 0 || Text(i.Server, "native MCP server", 128, true) != nil {
		return invalidTool()
	}
	return nil
}

// Matches reports whether the identity names one of the execution's retained
// generations. A foreign or later revision is never accepted.
func (c ExecutionConfiguration) MatchesManagedMCP(i MCPServerIdentity) bool {
	return slices.ContainsFunc(c.ManagedMCP, func(d ManagedMCPDefinition) bool {
		return d.ID == i.DefinitionID && d.Revision == i.Revision && d.NativeServerName() == i.Server
	})
}

const MCPTool ToolKind = "mcp"

// MCPToolObservation is a passive native observation. Arguments are excluded
// because they may contain protected values; the result keeps only bounded text.
// An observation never authorizes retrying the call.
type MCPToolObservation struct {
	MCPServerIdentity
	Tool         string  `json:"tool"`
	ReadOnlyHint *bool   `json:"read_only_hint,omitempty"`
	DurationMS   *int64  `json:"duration_ms,omitempty"`
	Error        *string `json:"error,omitempty"`
	ResultText   *string `json:"result_text,omitempty"`
	// ContentBlocks counts every native result block, including omitted ones.
	ContentBlocks uint32 `json:"content_blocks,omitempty"`
}

const maxMCPResultText = 64 << 10

func (o MCPToolObservation) Validate(status ToolStatus) error {
	if o.MCPServerIdentity.Validate() != nil || Text(o.Tool, "native MCP tool", 256, true) != nil || (o.DurationMS != nil && *o.DurationMS < 0) {
		return invalidTool()
	}
	for _, field := range []struct {
		value *string
		limit int
	}{{o.Error, 16 << 10}, {o.ResultText, maxMCPResultText}} {
		if field.value != nil && Text(*field.value, "native MCP observation", field.limit, false) != nil {
			return invalidTool()
		}
	}
	switch status {
	case ToolRunning:
		if o.Error != nil || o.ResultText != nil || o.DurationMS != nil {
			return invalidTool()
		}
	case ToolCompleted:
		if o.Error != nil {
			return invalidTool()
		}
	case ToolFailed:
	default:
		return invalidTool()
	}
	return nil
}
