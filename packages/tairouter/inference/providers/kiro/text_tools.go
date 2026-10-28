// SPDX-License-Identifier: Apache-2.0
// Standalone freeform recovery follows opencodex's shared serialized-tool-call
// reader. Kiro's native decoder has no independent textual-call grammar.
package kiro

import (
	"encoding/json"
	"strings"
)

type textCall struct {
	alias, input string
	start, end   int
}

// scanTextTools recognizes bare wrappers outside Markdown literals. It never
// turns arbitrary JSON, a named parameter wrapper, or an undeclared function
// into a call. The complete buffer and candidate count have independent caps.
func scanTextTools(text string, definitions map[string]ToolDefinition) ([]textCall, error) {
	custom := false
	for _, definition := range definitions {
		if definition.Kind == CustomTool {
			custom = true
			break
		}
	}
	if !custom {
		return nil, nil
	}
	if len(text) > 4<<20 {
		return nil, failure(502, "kiro_text_tool_buffer_limit")
	}
	var calls []textCall
	fence := ""
	inline := 0
	lineStart := true
	literal := false
	for i := 0; i < len(text); {
		if lineStart {
			line := text[i:]
			end := strings.IndexByte(line, '\n')
			if end >= 0 {
				line = line[:end]
			}
			trim := strings.TrimLeft(line, " ")
			indent := len(line) - len(trim)
			if indent <= 3 && len(trim) >= 3 && (strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~")) && inline == 0 {
				char := trim[0]
				n := 0
				for n < len(trim) && trim[n] == char {
					n++
				}
				if fence == "" {
					fence = trim[:n]
				} else if fence[0] == char && n >= len(fence) && strings.TrimSpace(trim[n:]) == "" {
					fence = ""
				}
				i += len(line)
				if i < len(text) {
					i++
				}
				lineStart = true
				continue
			}
			literal = len(line) > 0 && (line[0] == ' ' || line[0] == '\t' || line[0] == '>')
			lineStart = false
		}
		if fence == "" && text[i] == '`' {
			n := 1
			for i+n < len(text) && text[i+n] == '`' {
				n++
			}
			if inline == 0 {
				inline = n
			} else if inline == n {
				inline = 0
			}
			i += n
			continue
		}
		if fence == "" && inline == 0 && !literal && strings.HasPrefix(text[i:], "<tool_call>") {
			start := i
			headerStart := i + len("<tool_call>")
			rest := strings.TrimLeft(text[headerStart:], " \r\n\t")
			headerStart = len(text) - len(rest)
			if !strings.HasPrefix(rest, "<function=") {
				i++
				continue
			}
			endName := strings.IndexByte(rest, '>')
			if endName < 0 {
				return nil, failure(502, "kiro_text_tool_incomplete")
			}
			name := strings.TrimSpace(rest[len("<function="):endName])
			bodyStart := headerStart + endName + 1
			closeAt := textBlockClose(text, bodyStart)
			if closeAt < 0 {
				return nil, failure(502, "kiro_text_tool_incomplete")
			}
			end := closeAt + len("</tool_call>")
			body := strings.TrimRight(text[bodyStart:closeAt], " \r\n\t")
			body = strings.TrimSuffix(body, "</function>")
			body = strings.TrimRight(body, " \r\n\t")
			body = strings.TrimSuffix(body, "</parameter>")
			// An enclosing ambiguous block cannot authorize an apparent inner call.
			if strings.Contains(body, "<tool_call") || strings.Contains(body, "<function=") {
				return nil, nil
			}
			definition, ok := definitions[name]
			if ok && definition.Kind == CustomTool && strings.TrimSpace(body) != "" && !strings.Contains(body, "<parameter=") && !strings.Contains(body, "</parameter>") {
				if len(calls) >= 128 {
					return nil, failure(502, "kiro_text_tool_count_limit")
				}
				calls = append(calls, textCall{name, body, start, end})
			}
			i = end
			continue
		}
		if text[i] == '\n' {
			lineStart = true
		}
		i++
	}
	return calls, nil
}

func textBlockClose(text string, bodyStart int) int {
	limit := len(text)
	if next := strings.Index(text[bodyStart:], "\n<tool_call>"); next >= 0 {
		limit = bodyStart + next + 1
	}
	fallback := -1
	for at := bodyStart; at < len(text); {
		n := strings.Index(text[at:], "</tool_call>")
		if n < 0 {
			break
		}
		closeAt := at + n
		if closeAt >= limit && fallback >= 0 {
			break
		}
		if strings.HasSuffix(strings.TrimRight(text[bodyStart:closeAt], " \r\n\t"), "</function>") {
			return closeAt
		}
		if fallback < 0 && closeAt < limit {
			fallback = closeAt
		}
		at = closeAt + len("</tool_call>")
	}
	return fallback
}

func (s *streamState) recoverTextTools() error {
	if s.deferredText.Len() == 0 {
		return nil
	}
	text := s.deferredText.String()
	calls, err := scanTextTools(text, s.definitions)
	if err != nil {
		return err
	}
	if len(calls) == 0 {
		return nil
	}
	// No native call exists and no deferred prose has been released. Only now
	// can a standalone textual call be committed to the outbound stream.
	s.deferredText.Reset()
	offset := 0
	for _, call := range calls {
		s.deferredText.WriteString(text[offset:call.start])
		id, err := identifier()
		if err != nil {
			return err
		}
		args, _ := json.Marshal(object{"input": call.input})
		if err := s.tool(object{"toolUseId": "toolu_" + strings.ReplaceAll(id, "-", ""), "name": call.alias, "input": string(args), "stop": true}); err != nil {
			return err
		}
		offset = call.end
	}
	s.deferredText.WriteString(text[offset:])
	return nil
}

// Suppress only exact native-call echoes. Other examples remain prose, and
// native calls never cause a second call from the fallback parser.
func (s *streamState) reconcileNativeText(text string) string {
	calls, err := scanTextTools(text, s.definitions)
	if err != nil || len(calls) == 0 {
		return text
	}
	var out strings.Builder
	offset := 0
	for _, call := range calls {
		match := false
		for _, tool := range s.tools {
			if tool.name != call.alias || !tool.stopped {
				continue
			}
			var args object
			if json.Unmarshal([]byte(tool.input.String()), &args) == nil && str(args["input"]) == call.input {
				match = true
				break
			}
		}
		if match {
			out.WriteString(text[offset:call.start])
			offset = call.end
		}
	}
	out.WriteString(text[offset:])
	return out.String()
}
