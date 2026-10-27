// SPDX-License-Identifier: Apache-2.0
package reasoningreplay

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNativeStreamCapture(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	r := fixtureReplay(t, f, fixtureScope())
	s := NewStreamCapture(r, true)
	events := []string{
		`{"choices":[{"index":0,"delta":{"reasoning_content":"first "}},{"index":1,"delta":{"reasoning":"second"}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_content":"thought","tool_calls":[{"index":0,"id":"call_"},{"index":1,"id":"parallel"}]}},{"index":1,"delta":{"tool_calls":[{"index":0,"id":"other"}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"1"},{"index":1,"id":"parallel"}]},"finish_reason":"tool_calls"}]}`,
	}
	for _, event := range events {
		if err := s.Observe(ctx, decode(t, event)); err != nil {
			t.Fatal(err)
		}
	}
	// Completed choice is visible before stream EOF; repeated IDs are not appended.
	for _, id := range []string{"call_1", "parallel"} {
		if text, err := r.Peek(ctx, id); err != nil || text != "first thought" {
			t.Fatal(text, err)
		}
	}
	if _, err := r.Peek(ctx, "other"); !errors.Is(err, ErrMissing) {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, true); err != nil {
		t.Fatal(err)
	}
	if text, err := r.Peek(ctx, "other"); err != nil || text != "second" {
		t.Fatal(text, err)
	}
	if len(s.choices) != 0 || s.bytes != 0 {
		t.Fatal("retained finished buffers")
	}
}
func TestStreamFailureAndBounds(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"failed", "bytes", "calls", "disabled"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			r := fixtureReplay(t, f, fixtureScope())
			s := NewStreamCapture(r, kind != "disabled")
			event := decode(t, `{"choices":[{"delta":{"reasoning_content":"thought","tool_calls":[{"id":"call"}]}}]}`)
			if err := s.Observe(ctx, event); err != nil {
				t.Fatal(err)
			}
			if kind == "bytes" {
				if err := s.Observe(ctx, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"reasoning_content": strings.Repeat("x", MaxBytes)}}}}); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "calls" {
				calls := make([]any, MaxEntries+1)
				for i := range calls {
					calls[i] = map[string]any{"index": i, "id": "extra"}
				}
				if err := s.Observe(ctx, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": calls}}}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Finish(ctx, kind != "failed"); err != nil {
				t.Fatal(err)
			}
			if _, err := r.Peek(ctx, "call"); !errors.Is(err, ErrMissing) {
				t.Fatal("partial capture survived", err)
			}
		})
	}
}
