// SPDX-License-Identifier: Apache-2.0
// Text tool-call quarantine adapted from opencodex; see LICENSE.upstream.
package cursor

import (
	"encoding/json"
	"fmt"
	"strings"
)

const textMarker = "[TOOL_CALL]"
const maxPendingText = 64 << 10

// Tool markers are protocol material, never assistant prose. Fallback calls
// remain deferred until terminal confirmation and lose to real tool frames.
func (s *Session) textEvents(chunk string) ([]Event, error) {
	guarded, err := s.guardText(chunk, false)
	if err != nil {
		return nil, err
	}
	if guarded == "" {
		return nil, nil
	}
	if s.observeReplay {
		guarded = s.envelope.feed(guarded)
		s.midstreamEcho = s.envelope.matched || s.envelope.unverified
	}
	return s.parseTextEvents(guarded)
}
func (s *Session) parseTextEvents(chunk string) ([]Event, error) {
	text := s.pendingText + chunk
	s.pendingText = ""
	var visible strings.Builder
	for len(text) > 0 {
		start := strings.Index(strings.ToUpper(text), textMarker)
		if start < 0 {
			keep := 0
			for n := 1; n < len(textMarker) && n <= len(text); n++ {
				if strings.EqualFold(text[len(text)-n:], textMarker[:n]) {
					keep = n
				}
			}
			visible.WriteString(text[:len(text)-keep])
			s.pendingText = text[len(text)-keep:]
			break
		}
		visible.WriteString(text[:start])
		text = text[start:]
		argTag := strings.Index(strings.ToUpper(text), "[ARGS]")
		if argTag < 0 {
			s.pendingText = text
			break
		}
		name := strings.TrimSpace(text[len(textMarker):argTag])
		offset := argTag + len("[ARGS]")
		for offset < len(text) && (text[offset] == ' ' || text[offset] == '\t' || text[offset] == '\r' || text[offset] == '\n') {
			offset++
		}
		if offset == len(text) {
			s.pendingText = text
			break
		}
		if text[offset] != '{' {
			return nil, protocolError("cursor_invalid_text_tool_call")
		}
		end := jsonObjectEnd(text, offset)
		if end < 0 {
			s.pendingText = text
			break
		}
		args := text[offset:end]
		if len(args) > maxPendingText {
			return nil, protocolError("cursor_text_tool_call_limit")
		}
		name = strings.TrimPrefix(name, "mcp_"+toolProvider+"_")
		if _, ok := s.tools[name]; ok && !s.sawRealTool && json.Valid([]byte(args)) {
			if len(s.fallback) >= MaxToolCalls {
				return nil, protocolError("cursor_tool_call_limit")
			}
			s.fallback = append(s.fallback, Event{Kind: ToolCall, ID: fmt.Sprintf("textcall_%d", len(s.fallback)+1), Name: name, Arguments: args})
		}
		text = text[end:]
	}
	if len(s.pendingText) > maxPendingText {
		return nil, protocolError("cursor_text_tool_call_limit")
	}
	if visible.Len() == 0 {
		return nil, nil
	}
	s.visible = true
	return []Event{{Kind: Text, Text: visible.String()}}, nil
}

func jsonObjectEnd(s string, start int) int {
	depth := 0
	quoted, escape := false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if quoted {
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}
