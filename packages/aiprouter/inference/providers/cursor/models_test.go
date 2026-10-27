// SPDX-License-Identifier: Apache-2.0
package cursor

import (
	"bytes"
	"context"
	"reflect"
	"testing"
)

// Expectations mirror the pinned catalog/request-builder fixtures, including
// identity precedence and variant-specific effort ladders.
func TestPinnedModelSelections(t *testing.T) {
	for _, tc := range []struct {
		id, effort string
		fast       bool
		wire       string
		params     map[string]any
		max        bool
	}{
		{"cursor/auto", "", false, "default", nil, false},
		{"auto-intelligence", "", false, "default", map[string]any{"optimization": "intelligence"}, false},
		{"auto-balance", "", false, "default", map[string]any{"optimization": "balance"}, false},
		{"auto-cost", "", false, "default", map[string]any{"optimization": "cost"}, false},
		{"claude-4.6-opus", "", false, "claude-4.6-opus-max-thinking", nil, false},
		{"claude-opus-4-6", "low", false, "claude-opus-4-6-thinking-high", nil, false},
		{"claude-fable-5.1", "high", false, "claude-fable-5.1-thinking-high", nil, false},
		{"claude-5.1-fable", "medium", false, "claude-5.1-fable-medium-thinking", nil, false},
		{"claude-opus-5", "", true, "claude-opus-5-thinking-max-fast", nil, false},
		{"claude-opus-5-fast", "max", false, "claude-opus-5-high-fast", nil, false},
		{"claude-opus-5-thinking-fast", "max", false, "claude-opus-5-thinking-max-fast", nil, false},
		{"claude-4.6-opus-high-thinking", "low", false, "claude-4.6-opus-high-thinking", nil, false},
		{"claude-4.5-sonnet", "", false, "claude-4.5-sonnet-thinking", nil, false},
		{"claude-4-sonnet-1m", "", false, "claude-4-sonnet-1m", nil, false},
		{"grok-4.5", "", false, "cursor-grok-4.5-high", nil, false},
		{"grok-4.6", "xhigh", true, "grok-4.6", map[string]any{"effort": "xhigh", "fast": "true"}, false},
		{"cursor-grok-4.6-low-fast", "high", false, "grok-4.6", map[string]any{"effort": "low", "fast": "true"}, false},
		{"grok-4.7-fast", "", false, "grok-4.7-xhigh-fast", nil, false},
		{"gpt-5.1-codex-max", "", false, "gpt-5.1-codex-max-xhigh", nil, false},
		{"gpt-5.5-extra", "low", false, "gpt-5.5-extra-high", nil, false},
		{"gpt-5.2", "medium", false, "gpt-5.2-high", nil, false},
		{"gemini-3.6-flash", "minimal", false, "gemini-3.6-flash-minimal", nil, false},
		{"muse-spark-1.3", "max", false, "muse-spark-1.3-xhigh", nil, false},
		{"kimi-k3-1m", "medium", false, "kimi-k3-high", nil, true},
		{"kimi-k3", "ultra", false, "kimi-k3-max", nil, true},
		{"gpt-5.4", "ultra", false, "gpt-5.4-xhigh", nil, false},
		{"unknown-1m", "", false, "unknown-1m", nil, false},
	} {
		t.Run(tc.id+"/"+tc.effort, func(t *testing.T) {
			wire, params, max := selectModel(tc.id, tc.effort, tc.fast)
			if wire != tc.wire || !reflect.DeepEqual(params, tc.params) || max != tc.max {
				t.Fatalf("got %q %#v max=%t, want %q %#v max=%t", wire, params, max, tc.wire, tc.params, tc.max)
			}
		})
	}
}
func TestModelSelectionProtobuf(t *testing.T) {
	for _, id := range []string{"cursor/auto-cost", "grok-4.6-fast", "kimi-k3-1m", "claude-opus-5"} {
		t.Run(id, func(t *testing.T) {
			in := input(simpleRequest)
			in.Target.Model = id
			r, _, err := Prepare(context.Background(), in, nil)
			if err != nil {
				t.Fatal(err)
			}
			frame, err := ReadFrame(bytes.NewReader(r.Body))
			if err != nil {
				t.Fatal(err)
			}
			outer, _ := parse(frame.Payload)
			run, _ := outer.sub(1)
			wire, params, max := selectModel(id, "", false)
			if len(params) > 0 {
				if run.get(3).number != 0 {
					t.Fatal("parameterized request included legacy model details")
				}
				requested, _ := run.sub(9)
				if requested.str(1) != wire {
					t.Fatal("wrong requested model")
				}
				got := map[string]any{}
				for _, f := range requested {
					if f.number == 3 {
						p, _ := parse(f.data)
						got[p.str(1)] = p.str(2)
					}
				}
				if !reflect.DeepEqual(params, got) {
					t.Fatalf("wire parameters %#v want %#v", got, params)
				}
			} else {
				details, _ := run.sub(3)
				if details.str(1) != wire || (details.get(7).value == 1) != max {
					t.Fatalf("wrong details %+v", details)
				}
				if max {
					requested, _ := run.sub(9)
					if requested.get(2).value != 1 {
						t.Fatal("missing requested Max Mode")
					}
				}
			}
		})
	}
}
