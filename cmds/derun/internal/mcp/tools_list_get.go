package mcp

import (
	"errors"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/errmsg"
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
			return nil, parseFieldError("limit", err, errmsg.ReceivedDetails(raw))
		}
		if parsed > 0 {
			limit = parsed
		}
	}

	sessions, totalCount, err := store.ListSessions(stateFilter, limit)
	if err != nil {
		return nil, wrapRuntimeErrorWithDetails("list sessions", err, map[string]any{
			"state": stateFilter,
			"limit": limit,
		})
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
	rawSessionID, exists := args["session_id"]
	if !exists {
		return nil, requiredFieldError("session_id", "a non-empty string", nil)
	}
	sessionID, ok := rawSessionID.(string)
	if !ok || sessionID == "" {
		return nil, requiredFieldError("session_id", "a non-empty string", rawSessionID)
	}

	detail, err := store.GetSession(sessionID)
	if err != nil {
		if errors.Is(err, state.ErrSessionNotFound) {
			return nil, errmsg.Wrap(state.ErrSessionNotFound, map[string]any{"session_id": sessionID})
		}
		return nil, wrapRuntimeErrorWithDetails("get session", err, map[string]any{
			"session_id": sessionID,
		})
	}

	return map[string]any{
		"schema_version": SchemaVersion,
		"session":        detail,
		"output_bytes":   detail.OutputBytes,
		"chunk_count":    detail.ChunkCount,
		"last_chunk_at":  detail.LastChunkAt,
	}, nil
}
