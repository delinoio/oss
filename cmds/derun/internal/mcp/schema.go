package mcp

import (
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
)

const (
	SchemaVersion      = "v1alpha1"
	DefaultMaxBytes    = 64 * 1024
	DefaultWaitTimeout = 30 * time.Second
	MaxWaitTimeout     = 60 * time.Second
)

type ToolDefinition struct {
	Name        contracts.DerunMCPTool `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]any         `json:"inputSchema"`
}

func toolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        contracts.DerunMCPToolListSessions,
			Description: "List recent sessions with optional state filter.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"state": map[string]any{"type": "string"},
					"limit": map[string]any{"type": "integer", "minimum": 1},
				},
			},
		},
		{
			Name:        contracts.DerunMCPToolGetSession,
			Description: "Get detailed metadata and output stats for one session.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"session_id"},
				"properties": map[string]any{
					"session_id": map[string]any{"type": "string"},
				},
			},
		},
		{
			Name:        contracts.DerunMCPToolReadOutput,
			Description: "Read output chunks from a cursor.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"session_id"},
				"properties": map[string]any{
					"session_id": map[string]any{"type": "string"},
					"cursor":     map[string]any{"type": "string"},
					"max_bytes":  map[string]any{"type": "integer", "minimum": 1},
				},
			},
		},
		{
			Name:        contracts.DerunMCPToolWaitOutput,
			Description: "Wait for output from a cursor.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"session_id", "cursor"},
				"properties": map[string]any{
					"session_id": map[string]any{"type": "string"},
					"cursor":     map[string]any{"type": "string"},
					"max_bytes":  map[string]any{"type": "integer", "minimum": 1},
					"timeout_ms": map[string]any{"type": "integer", "minimum": 1},
				},
			},
		},
	}
}
