// SPDX-License-Identifier: Apache-2.0
// Adapted from opencodex server/chat-native-reasoning.ts; see LICENSE-MIT.
package reasoningreplay

import (
	"context"
	"encoding/json"
	"math"
	"sync"
)

type streamChoice struct {
	text  string
	calls map[int]string
	order []int
}

// StreamCapture is a bounded request-local collector, not a replay store. Observe
// sees decoded native Chat SSE objects. Finish must be called on every exit path.
// An oversized collector is discarded, as in the pinned native Chat observer.
type StreamCapture struct {
	mu           sync.Mutex
	replay       *Replay
	choices      map[int]*streamChoice
	order        []int
	bytes, calls int
	disabled     bool
}

func NewStreamCapture(r *Replay, preserve bool) *StreamCapture {
	return &StreamCapture{replay: r, choices: make(map[int]*streamChoice), disabled: !preserve || r == nil}
}

func (s *StreamCapture) discard() {
	s.choices = nil
	s.order = nil
	s.bytes, s.calls = 0, 0
	s.disabled = true
}

func (s *StreamCapture) append(previous, fragment string) (string, bool) {
	// Match the upstream collector's conservative two-byte accounting. Reserve
	// before concatenation so one oversized provider fragment cannot grow state.
	if len(fragment) > (MaxBytes-s.bytes)/2 {
		s.discard()
		return "", false
	}
	s.bytes += len(fragment) * 2
	return previous + fragment, true
}

func index(v any, fallback int) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		if n >= 0 && n < float64(math.MaxInt) && math.Trunc(n) == n {
			return int(n)
		}
	case json.Number:
		if i, err := n.Int64(); err == nil && i >= 0 && i < int64(math.MaxInt) {
			return int(i)
		}
	}
	return fallback
}

func (s *StreamCapture) remember(ctx context.Context, choice *streamChoice) error {
	for _, i := range choice.order {
		if err := s.replay.Capture(ctx, choice.calls[i], choice.text); err != nil {
			return err
		}
	}
	return nil
}

func (s *StreamCapture) Observe(ctx context.Context, value map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disabled {
		return nil
	}
	choices, _ := value["choices"].([]any)
	for ordinal, raw := range choices {
		choice, _ := raw.(map[string]any)
		delta, ok := choice["delta"].(map[string]any)
		if !ok {
			continue
		}
		i := index(choice["index"], ordinal)
		state := s.choices[i]
		if state == nil {
			if len(s.choices) >= MaxEntries {
				s.discard()
				return nil
			}
			state = &streamChoice{calls: make(map[int]string)}
			s.choices[i] = state
			s.order = append(s.order, i)
		}
		text, _ := delta["reasoning_content"].(string)
		if text == "" {
			text, _ = delta["reasoning"].(string)
		}
		if text != "" {
			var ok bool
			state.text, ok = s.append(state.text, text)
			if !ok {
				return nil
			}
		}
		calls, _ := delta["tool_calls"].([]any)
		for ordinal, rawCall := range calls {
			call, _ := rawCall.(map[string]any)
			id, _ := call["id"].(string)
			if id == "" {
				continue
			}
			i := index(call["index"], ordinal)
			previous, exists := state.calls[i]
			if !exists {
				if s.calls >= MaxEntries {
					s.discard()
					return nil
				}
				s.calls++
				state.order = append(state.order, i)
			}
			if previous == id {
				continue
			}
			next, ok := s.append(previous, id)
			if !ok {
				return nil
			}
			state.calls[i] = next
		}
		if finish, _ := choice["finish_reason"].(string); finish != "" {
			if err := s.remember(ctx, state); err != nil {
				s.discard()
				return err
			}
		}
	}
	return nil
}

// Finish commits only successful unfinished choices and releases all buffers.
// Already completed choices remain valid even if the connection later fails.
func (s *StreamCapture) Finish(ctx context.Context, success bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.discard()
	if s.disabled || !success {
		return nil
	}
	for _, i := range s.order {
		if err := s.remember(ctx, s.choices[i]); err != nil {
			return err
		}
	}
	return nil
}
