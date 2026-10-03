// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"encoding/json"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type foregroundToolCall struct {
	name      string
	arguments strings.Builder
}

// Tool arguments can be executable before the final provider frame arrives.
// Retain their original frames until the complete call is validated; observing
// native task reuse afterwards would be too late to prevent its side effect.
type foregroundToolGuard struct {
	calls    map[uint32]*foregroundToolCall
	frames   [][]byte
	bytes    int
	finished bool
}

func (g *foregroundToolGuard) clear() {
	for _, frame := range g.frames {
		clear(frame)
	}
	g.frames, g.calls = nil, nil
}

func (g *foregroundToolGuard) deliver(frame []byte, object map[string]json.RawMessage, write func([]byte) error) error {
	var choices []struct {
		Index uint32 `json:"index"`
		Delta struct {
			Tools []struct {
				Index    uint32  `json:"index"`
				Type     *string `json:"type"`
				Function struct {
					Name      *string `json:"name"`
					Arguments *string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		Finish *string `json:"finish_reason"`
	}
	if raw, ok := object["choices"]; ok && json.Unmarshal(raw, &choices) != nil {
		return errInvalidDocument
	}
	terminal := false
	for _, choice := range choices {
		if len(choice.Delta.Tools) > 0 && (choice.Index != 0 || g.finished) {
			return errInvalidDocument
		}
		for _, delta := range choice.Delta.Tools {
			if delta.Index >= 128 || delta.Type != nil && *delta.Type != "function" {
				return errInvalidDocument
			}
			if g.calls == nil {
				g.calls = map[uint32]*foregroundToolCall{}
			}
			call := g.calls[delta.Index]
			if call == nil {
				call = &foregroundToolCall{}
				g.calls[delta.Index] = call
			}
			if delta.Function.Name != nil {
				call.name += *delta.Function.Name
			}
			if len(call.name) > 256 {
				return errInvalidDocument
			}
			if delta.Function.Arguments != nil {
				if len(*delta.Function.Arguments) > domain.MaxPromptBytes-call.arguments.Len() {
					return errInvalidDocument
				}
				call.arguments.WriteString(*delta.Function.Arguments)
			}
		}
		if choice.Finish != nil && choice.Index == 0 {
			terminal = true
		}
	}
	if len(g.calls) == 0 {
		return write(frame)
	}
	if len(g.frames) >= 2048 || len(frame) > 2<<20-g.bytes {
		return errInvalidDocument
	}
	g.frames = append(g.frames, append([]byte(nil), frame...))
	g.bytes += len(frame)
	if !terminal {
		return nil
	}
	for _, call := range g.calls {
		if call.name == "" {
			return errInvalidDocument
		}
		if call.name == string(domain.OpenCodeTask) {
			if _, err := domain.DecodeOpenCodeForegroundTask([]byte(call.arguments.String())); err != nil {
				return errInvalidDocument
			}
		}
	}
	for _, pending := range g.frames {
		if err := write(pending); err != nil {
			return err
		}
	}
	g.clear()
	g.bytes, g.finished = 0, true
	return nil
}

func (g *foregroundToolGuard) settled() bool { return len(g.calls) == 0 && len(g.frames) == 0 }

func validateForegroundToolResponse(object map[string]json.RawMessage) error {
	var choices []struct {
		Message struct {
			Calls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	}
	if json.Unmarshal(object["choices"], &choices) != nil {
		return errInvalidDocument
	}
	for _, choice := range choices {
		if len(choice.Message.Calls) > 128 {
			return errInvalidDocument
		}
		for _, call := range choice.Message.Calls {
			if call.Function.Name == string(domain.OpenCodeTask) {
				if _, err := domain.DecodeOpenCodeForegroundTask([]byte(call.Function.Arguments)); err != nil {
					return errInvalidDocument
				}
			}
		}
	}
	return nil
}
