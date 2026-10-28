// SPDX-License-Identifier: Apache-2.0
// Adapted from opencodex openai-chat/reasoning-replay.ts; see LICENSE-MIT.
package reasoningreplay

import (
	"context"
	"encoding/json"
	"regexp"
)

var deepSeek = regexp.MustCompile(`(?i)(^|/)deepseek-(v[0-9]+(\.[0-9]+)*-(flash|pro)|flash|pro)($|[-_:])`)
var reflection = regexp.MustCompile(`(?i)(^|/)(reflection|stealth)($|[-_:/.])`)

func IsDeepSeekThinkingModel(model string) bool   { return deepSeek.MatchString(model) }
func IsReflectionThinkingModel(model string) bool { return reflection.MatchString(model) }

// Policy is resolved per selected model. Explicit preserve/placeholder overrides
// belong to the provider policy owner. Details skips native replay as upstream does;
// translated serializers can use ForCalls and their structured details encoder.
type Policy struct {
	Preserve, Placeholder, Details bool
	Model                          string
	HasTools                       bool
}

func DefaultPolicy(model string, hasTools bool) Policy {
	preserve := IsDeepSeekThinkingModel(model) || IsReflectionThinkingModel(model)
	return Policy{Preserve: preserve, Placeholder: preserve, Model: model, HasTools: hasTools}
}

// RepairChat copies only changed assistant maps. It preserves supplied reasoning,
// aliases reasoning to reasoning_content, and restores missing tool-call blocks.
// maxGrowthBytes bounds escaped JSON growth before any changed maps are allocated.
// Tool-less placeholders match the pinned model policy; missing required tool-call
// state returns ErrMissing instead of the upstream memory-cache placeholder.
func (r *Replay) RepairChat(ctx context.Context, messages []any, policy Policy, maxGrowthBytes int64) ([]any, error) {
	if !policy.Preserve || policy.Details {
		return messages, nil
	}
	type change struct {
		index int
		text  string
	}
	changes := make([]change, 0)
	var growth int64
	for i, raw := range messages {
		m, ok := raw.(map[string]any)
		if !ok || m["role"] != "assistant" {
			continue
		}
		if text, _ := m["reasoning_content"].(string); text != "" {
			continue
		}
		text, _ := m["reasoning"].(string)
		if text == "" {
			calls, _ := m["tool_calls"].([]any)
			if len(calls) == 0 && !(policy.HasTools && IsDeepSeekThinkingModel(policy.Model)) && !IsReflectionThinkingModel(policy.Model) {
				continue
			}
			ids := make([]string, 0, len(calls))
			for _, rawCall := range calls {
				call, ok := rawCall.(map[string]any)
				if !ok {
					continue
				}
				if id, ok := call["id"].(string); ok && id != "" {
					ids = append(ids, id)
				}
			}
			if len(calls) > 0 && len(ids) != len(calls) {
				return nil, ErrScope
			}
			if len(ids) > 0 {
				var err error
				text, err = r.ForCalls(ctx, ids)
				if err != nil {
					return nil, err
				}
			} else if policy.Placeholder {
				text = " "
			}
		}
		if text == "" {
			continue
		}
		value, err := json.Marshal(text)
		if err != nil {
			return nil, err
		}
		delta := int64(len(value))
		if old, exists := m["reasoning_content"]; exists {
			b, err := json.Marshal(old)
			if err != nil {
				return nil, err
			}
			delta -= int64(len(b))
		} else {
			delta += int64(len(`"reasoning_content":`) + 1)
		}
		if delta > maxGrowthBytes-growth {
			return nil, ErrBudget
		}
		growth += delta
		changes = append(changes, change{i, text})
	}
	if len(changes) == 0 {
		return messages, nil
	}
	out := append([]any(nil), messages...)
	for _, c := range changes {
		m := messages[c.index].(map[string]any)
		copy := make(map[string]any, len(m)+1)
		for k, v := range m {
			copy[k] = v
		}
		copy["reasoning_content"] = c.text
		out[c.index] = copy
	}
	return out, nil
}

// CaptureChat records native buffered choices, retaining no content or arguments.
func (r *Replay) CaptureChat(ctx context.Context, response map[string]any) error {
	choices, _ := response["choices"].([]any)
	for _, raw := range choices {
		choice, _ := raw.(map[string]any)
		message, _ := choice["message"].(map[string]any)
		text, _ := message["reasoning_content"].(string)
		if text == "" {
			text, _ = message["reasoning"].(string)
		}
		if text == "" {
			continue
		}
		calls, _ := message["tool_calls"].([]any)
		for _, rawCall := range calls {
			call, _ := rawCall.(map[string]any)
			id, _ := call["id"].(string)
			if id == "" {
				continue
			}
			if err := r.Capture(ctx, id, text); err != nil {
				return err
			}
		}
	}
	return nil
}
