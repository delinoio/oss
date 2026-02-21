package mcp

import (
	"fmt"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/state"
)

func handleListSessions(store *state.Store, args map[string]any) (map[string]any, error) {
	var stateFilter contracts.DerunSessionState
	if raw, ok := args["state"].(string); ok && raw != "" {
		stateFilter = contracts.DerunSessionState(raw)
	}

	limit := 50
	if raw, ok := args["limit"]; ok {
		parsed, err := anyToInt(raw)
		if err != nil {
			return nil, fmt.Errorf("parse limit: %w", err)
		}
		if parsed > 0 {
			limit = parsed
		}
	}

	sessions, totalCount, err := store.ListSessions(stateFilter, limit)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	return map[string]any{
		"schema_version": SchemaVersion,
		"generated_at":   time.Now().UTC(),
		"total_count":    totalCount,
		"truncated":      totalCount > len(sessions),
		"sessions":       sessions,
	}, nil
}

func handleGetSession(store *state.Store, args map[string]any) (map[string]any, error) {
	rawSessionID, ok := args["session_id"].(string)
	if !ok || rawSessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	detail, err := store.GetSession(rawSessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	return map[string]any{
		"schema_version": SchemaVersion,
		"session":        detail,
		"output_bytes":   detail.OutputBytes,
		"chunk_count":    detail.ChunkCount,
		"last_chunk_at":  detail.LastChunkAt,
	}, nil
}
