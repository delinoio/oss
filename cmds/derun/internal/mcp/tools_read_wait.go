package mcp

import (
	"errors"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/errmsg"
	"github.com/delinoio/oss/cmds/derun/internal/state"
)

func handleReadOutput(store *state.Store, args map[string]any) (map[string]any, error) {
	sessionID, err := parseRequiredSessionID(args)
	if err != nil {
		return nil, err
	}

	cursor, err := parseCursor(args, false)
	if err != nil {
		return nil, err
	}

	maxBytes, err := parsePositiveIntDefault(args, "max_bytes", DefaultMaxBytes)
	if err != nil {
		return nil, err
	}

	chunks, nextCursor, eof, err := store.ReadOutput(sessionID, cursor, maxBytes)
	if err != nil {
		return nil, wrapReadWaitError("read output", err, map[string]any{
			"session_id": sessionID,
			"cursor":     cursor,
			"max_bytes":  maxBytes,
		})
	}

	return buildOutputPayload(sessionID, chunks, nextCursor, eof, outputPayloadOptions{}), nil
}

func handleWaitOutput(store *state.Store, args map[string]any) (map[string]any, error) {
	sessionID, err := parseRequiredSessionID(args)
	if err != nil {
		return nil, err
	}

	cursor, err := parseCursor(args, true)
	if err != nil {
		return nil, err
	}

	maxBytes, err := parsePositiveIntDefault(args, "max_bytes", DefaultMaxBytes)
	if err != nil {
		return nil, err
	}

	timeout, err := parseWaitTimeout(args)
	if err != nil {
		return nil, err
	}
	timeoutMS := timeout.Milliseconds()

	started := time.Now()
	for {
		chunks, nextCursor, eof, err := store.ReadOutput(sessionID, cursor, maxBytes)
		if err != nil {
			return nil, wrapReadWaitError("read output while waiting", err, map[string]any{
				"session_id": sessionID,
				"cursor":     cursor,
				"max_bytes":  maxBytes,
				"timeout_ms": timeoutMS,
			})
		}
		if len(chunks) > 0 {
			return buildOutputPayload(sessionID, chunks, nextCursor, eof, outputPayloadOptions{
				includeWait: true,
				timedOut:    false,
				waitedMS:    time.Since(started).Milliseconds(),
			}), nil
		}
		if eof {
			detail, err := store.GetSession(sessionID)
			if err != nil {
				return nil, wrapReadWaitError("get session detail", err, map[string]any{
					"session_id": sessionID,
				})
			}
			if !isSessionActive(detail.State) {
				return buildOutputPayload(sessionID, chunks, nextCursor, eof, outputPayloadOptions{
					includeWait: true,
					timedOut:    false,
					waitedMS:    time.Since(started).Milliseconds(),
				}), nil
			}
		}
		if time.Since(started) >= timeout {
			break
		}
		time.Sleep(waitPollInterval)
	}

	chunks, nextCursor, eof, err := store.ReadOutput(sessionID, cursor, maxBytes)
	if err != nil {
		return nil, wrapReadWaitError("read output after timeout", err, map[string]any{
			"session_id": sessionID,
			"cursor":     cursor,
			"max_bytes":  maxBytes,
			"timeout_ms": timeoutMS,
		})
	}
	return buildOutputPayload(sessionID, chunks, nextCursor, eof, outputPayloadOptions{
		includeWait: true,
		timedOut:    true,
		waitedMS:    time.Since(started).Milliseconds(),
	}), nil
}

func isSessionActive(sessionState contracts.DerunSessionState) bool {
	return sessionState == contracts.DerunSessionStateStarting || sessionState == contracts.DerunSessionStateRunning
}

func wrapReadWaitError(prefix string, err error, details map[string]any) error {
	if errors.Is(err, state.ErrSessionNotFound) {
		return errmsg.Wrap(state.ErrSessionNotFound, details)
	}
	return wrapRuntimeErrorWithDetails(prefix, err, details)
}
