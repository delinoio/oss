package mcp

import (
	"fmt"
	"strconv"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/state"
)

func handleReadOutput(store *state.Store, args map[string]any) (map[string]any, error) {
	sessionID, ok := args["session_id"].(string)
	if !ok || sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	cursor := uint64(0)
	if raw, ok := args["cursor"].(string); ok && raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse cursor: %w", err)
		}
		cursor = parsed
	}

	maxBytes := DefaultMaxBytes
	if raw, ok := args["max_bytes"]; ok {
		parsed, err := anyToInt(raw)
		if err != nil {
			return nil, fmt.Errorf("parse max_bytes: %w", err)
		}
		if parsed > 0 {
			maxBytes = parsed
		}
	}

	chunks, nextCursor, eof, err := store.ReadOutput(sessionID, cursor, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("read output: %w", err)
	}

	return map[string]any{
		"schema_version": SchemaVersion,
		"session_id":     sessionID,
		"chunks":         chunks,
		"next_cursor":    strconv.FormatUint(nextCursor, 10),
		"eof":            eof,
	}, nil
}

func handleWaitOutput(store *state.Store, args map[string]any) (map[string]any, error) {
	sessionID, ok := args["session_id"].(string)
	if !ok || sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	rawCursor, ok := args["cursor"].(string)
	if !ok || rawCursor == "" {
		return nil, fmt.Errorf("cursor is required")
	}
	cursor, err := strconv.ParseUint(rawCursor, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse cursor: %w", err)
	}

	maxBytes := DefaultMaxBytes
	if raw, ok := args["max_bytes"]; ok {
		parsed, err := anyToInt(raw)
		if err != nil {
			return nil, fmt.Errorf("parse max_bytes: %w", err)
		}
		if parsed > 0 {
			maxBytes = parsed
		}
	}

	timeout := DefaultWaitTimeout
	if raw, ok := args["timeout_ms"]; ok {
		parsed, err := anyToInt(raw)
		if err != nil {
			return nil, fmt.Errorf("parse timeout_ms: %w", err)
		}
		if parsed > 0 {
			timeout = time.Duration(parsed) * time.Millisecond
		}
	}
	if timeout > MaxWaitTimeout {
		timeout = MaxWaitTimeout
	}

	started := time.Now()
	for {
		chunks, nextCursor, eof, err := store.ReadOutput(sessionID, cursor, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("wait read output: %w", err)
		}
		if len(chunks) > 0 || eof {
			return map[string]any{
				"schema_version": SchemaVersion,
				"session_id":     sessionID,
				"chunks":         chunks,
				"next_cursor":    strconv.FormatUint(nextCursor, 10),
				"eof":            eof,
				"timed_out":      false,
				"waited_ms":      time.Since(started).Milliseconds(),
			}, nil
		}
		if time.Since(started) >= timeout {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	chunks, nextCursor, eof, err := store.ReadOutput(sessionID, cursor, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("read output after timeout: %w", err)
	}
	return map[string]any{
		"schema_version": SchemaVersion,
		"session_id":     sessionID,
		"chunks":         chunks,
		"next_cursor":    strconv.FormatUint(nextCursor, 10),
		"eof":            eof,
		"timed_out":      true,
		"waited_ms":      timeout.Milliseconds(),
	}, nil
}
