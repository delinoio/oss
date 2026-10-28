// SPDX-License-Identifier: Apache-2.0
// Conversation wire behavior adapted from the pinned MIT source; see LICENSE.upstream.
package cursor

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

func (s *Session) replayBlob(data []byte) ([]byte, error) {
	if len(data) > MaxBlobBytes {
		return nil, protocolError("cursor_blob_limit")
	}
	digest := sha256.Sum256(data)
	key := hex.EncodeToString(digest[:])
	if _, ok := s.blobs[key]; !ok {
		if s.blobBytes+len(data) > MaxStateBytes {
			return nil, protocolError("cursor_state_limit")
		}
		s.blobs[key] = append([]byte(nil), data...)
		s.blobBytes += len(data)
	}
	return digest[:], nil
}
func replayCallID(id string) string {
	if strings.HasPrefix(id, "aipcursor_") {
		if raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(id, "aipcursor_")); err == nil {
			return string(raw)
		}
	}
	return id
}
func (s *Session) replayTool(call historyCall, result historyMessage, tools []tool) ([]byte, error) {
	name := call.Name
	for _, tool := range tools {
		if tool.Name == name {
			name = tool.WireName
			break
		}
	}
	var args map[string]any
	if json.Unmarshal([]byte(call.Arguments), &args) != nil || args == nil {
		return nil, protocolError("cursor_invalid_tool_arguments")
	}
	encoded := join(ps(1, name), ps(3, replayCallID(call.ID)), ps(4, toolProvider), ps(5, name))
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value, err := encodeValue(args[key], 0)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, pb(2, join(ps(1, key), pb(2, value)))...)
	}
	success := pb(1, pb(1, ps(1, result.Text)))
	if result.IsError {
		success = append(success, pv(2, 1)...)
	}
	// ConversationStep.tool_call -> ToolCall.mcp_tool_call -> args/result.
	return s.replayBlob(pb(2, pb(15, join(pb(1, encoded), pb(2, pb(1, success))))))
}

// Native Composer consumes typed turns. External workers reject native MCP and
// thinking history, so this path is restricted to native model identities.
// Missing results are explicit error observations, never fabricated success.
func (s *Session) nativeReplay(history []historyMessage, active int, tools []tool) ([]byte, error) {
	var turns, current []byte
	pending := map[string]historyCall{}
	var order []string
	appendStep := func(blob []byte, err error) error {
		if err != nil {
			return err
		}
		current = append(current, pb(2, blob)...)
		return nil
	}
	flush := func() error {
		if current == nil {
			return nil
		}
		for _, id := range order {
			if call, ok := pending[id]; ok {
				if err := appendStep(s.replayTool(call, historyMessage{Text: "[missing tool_result for this tool_use in history]", IsError: true}, tools)); err != nil {
					return err
				}
			}
		}
		id, err := s.replayBlob(pb(1, current))
		if err != nil {
			return err
		}
		turns = append(turns, pb(8, id)...)
		current = nil
		pending = map[string]historyCall{}
		order = nil
		return nil
	}
	for index, h := range history {
		if index == active {
			break
		}
		switch h.Role {
		case "system":
			continue
		case "user", "developer":
			if err := flush(); err != nil {
				return nil, err
			}
			id, err := newID()
			if err != nil {
				return nil, err
			}
			blob, err := s.replayBlob(join(ps(1, h.Text), ps(2, id), pb(3, nil), pv(4, 1)))
			if err != nil {
				return nil, err
			}
			current = pb(1, blob)
		case "assistant":
			if current == nil {
				continue
			}
			if h.Thinking != "" {
				if err := appendStep(s.replayBlob(pb(3, ps(1, h.Thinking)))); err != nil {
					return nil, err
				}
			}
			if h.Text != "" {
				if err := appendStep(s.replayBlob(pb(1, ps(1, h.Text)))); err != nil {
					return nil, err
				}
			}
			for _, call := range h.Calls {
				if _, ok := pending[call.ID]; !ok {
					order = append(order, call.ID)
				}
				pending[call.ID] = call
			}
		case "tool":
			if current == nil {
				continue
			}
			if call, ok := pending[h.CallID]; ok {
				if err := appendStep(s.replayTool(call, h, tools)); err != nil {
					return nil, err
				}
				delete(pending, h.CallID)
			} else {
				return nil, protocolError("cursor_unpaired_tool_result")
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return turns, nil
}
func externalToolContinuation(model string) bool {
	model = strings.TrimPrefix(strings.ToLower(model), "cursor/")
	return !nativeModel(model) || model == "composer-2.5" || model == "composer-2.5-fast"
}
