package mcp

import (
	"fmt"
	"strconv"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/session"
)

const waitPollInterval = 100 * time.Millisecond

type outputPayloadOptions struct {
	includeWait bool
	timedOut    bool
	waitedMS    int64
}

func parseRequiredSessionID(args map[string]any) (string, error) {
	sessionID, ok := args["session_id"].(string)
	if !ok || sessionID == "" {
		return "", requiredFieldError("session_id", "a non-empty string")
	}
	return sessionID, nil
}

func parseCursor(args map[string]any, required bool) (uint64, error) {
	rawCursor, exists := args["cursor"]
	if !exists {
		if required {
			return 0, requiredFieldError("cursor", "a non-empty decimal string")
		}
		return 0, nil
	}

	cursorString, ok := rawCursor.(string)
	if !ok || cursorString == "" {
		if required {
			return 0, requiredFieldError("cursor", "a non-empty decimal string")
		}
		return 0, nil
	}

	cursor, err := strconv.ParseUint(cursorString, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse cursor: %w", err)
	}
	return cursor, nil
}

func parsePositiveIntDefault(args map[string]any, key string, defaultValue int) (int, error) {
	value := defaultValue
	raw, ok := args[key]
	if !ok {
		return value, nil
	}

	parsed, err := anyToInt(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	if parsed > 0 {
		value = parsed
	}
	return value, nil
}

func parseWaitTimeout(args map[string]any) (time.Duration, error) {
	timeoutMS, err := parsePositiveIntDefault(args, "timeout_ms", int(DefaultWaitTimeout/time.Millisecond))
	if err != nil {
		return 0, err
	}
	timeout := time.Duration(timeoutMS) * time.Millisecond
	if timeout > MaxWaitTimeout {
		timeout = MaxWaitTimeout
	}
	return timeout, nil
}

func buildOutputPayload(sessionID string, chunks []session.OutputChunk, nextCursor uint64, eof bool, options outputPayloadOptions) map[string]any {
	payload := map[string]any{
		"schema_version": SchemaVersion,
		"session_id":     sessionID,
		"chunks":         chunks,
		"next_cursor":    strconv.FormatUint(nextCursor, 10),
		"eof":            eof,
	}
	if options.includeWait {
		payload["timed_out"] = options.timedOut
		payload["waited_ms"] = options.waitedMS
	}
	return payload
}
