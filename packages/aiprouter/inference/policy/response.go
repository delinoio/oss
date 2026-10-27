// SPDX-License-Identifier: Apache-2.0
package policy

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/delinoio/oss/packages/aiprouter/providers"
)

// ItemIDRepair is client-facing only. Capture raw upstream continuation state first.
type ItemIDRepair struct {
	Message                  []string `json:"message,omitempty"`
	Reasoning                []string `json:"reasoning,omitempty"`
	RepairMissingTerminalIDs *bool    `json:"repairMissingTerminalIds,omitempty"`
	RepairInvalidIDs         *bool    `json:"repairInvalidIds,omitempty"`
}

func (p Resolved) ItemIDRepair() *ItemIDRepair {
	m := obj(p.fields["responsesItemIdRepair"])
	if m == nil {
		return nil
	}
	raw, _ := json.Marshal(m)
	var out ItemIDRepair
	_ = json.Unmarshal(raw, &out)
	return &out
}
func (p Resolved) ShowThinkingSummary() *bool {
	v, ok := p.fields["showThinkingSummary"].(bool)
	if !ok {
		return nil
	}
	return &v
}
func (p Resolved) SupportsResponsesCustomTools() *bool {
	v, ok := p.fields["supportsResponsesCustomTools"].(bool)
	if !ok {
		return nil
	}
	return &v
}
func (p Resolved) EscapeBuiltinToolNames() bool {
	return flag(p.fields["escapeBuiltinToolNames"]) && p.Auth != providers.OAuth
}

// NewResponseRewriter creates request-local client-facing ID state. Never share
// this function between concurrent responses. Tool IDs and call IDs are untouched.
func (p Resolved) NewResponseRewriter() func(map[string]any) (map[string]any, error) {
	config := p.ItemIDRepair()
	var scope [16]byte
	_, err := rand.Read(scope[:])
	scopeID := hex.EncodeToString(scope[:])
	ids := map[string]string{}
	rawIDs := map[string]string{}
	return func(in map[string]any) (map[string]any, error) {
		if err != nil {
			return nil, err
		}
		raw, e := json.Marshal(in)
		if e != nil {
			return nil, e
		}
		b, e := decode(raw)
		if e != nil {
			return nil, e
		}
		if p.Wire == providers.Anthropic && p.EscapeBuiltinToolNames() {
			restoreAnthropicNames(b)
		}
		if config == nil || p.Wire != providers.Responses {
			return b, nil
		}
		rewriteItem := func(index int, item map[string]any) error {
			kind := str(item["type"])
			prefix := ""
			placeholders := config.Message
			switch kind {
			case "message":
				prefix = "msg_"
			case "reasoning":
				prefix = "rs_"
				placeholders = config.Reasoning
			default:
				return nil
			}
			key := fmt.Sprintf("%s:%d", kind, index)
			old, exists := item["id"].(string)
			mapped := ids[key]
			if mapped == "" && old != "" {
				replace := config.RepairInvalidIDs != nil && *config.RepairInvalidIDs && !strings.HasPrefix(old, prefix)
				for _, v := range placeholders {
					if v == old {
						replace = true
					}
				}
				if replace {
					mapped = fmt.Sprintf("%socx_%s_%d", prefix, scopeID, index)
				} else if config.RepairMissingTerminalIDs != nil && *config.RepairMissingTerminalIDs {
					mapped = old
				}
				if mapped != "" {
					if len(ids) >= 4096 || len(old) > 4096 {
						return errors.New("provider item ID repair budget exceeded")
					}
					ids[key] = mapped
					if old != mapped {
						rawIDs[fmt.Sprintf("%d:%s", index, old)] = mapped
					}
				}
			}
			if mapped != "" && (exists || (config.RepairMissingTerminalIDs != nil && *config.RepairMissingTerminalIDs)) {
				item["id"] = mapped
			}
			return nil
		}
		snapshot := func(r map[string]any) error {
			for i, x := range list(r["output"]) {
				if m := obj(x); m != nil {
					if e := rewriteItem(i, m); e != nil {
						return e
					}
				}
			}
			return nil
		}
		if e := snapshot(b); e != nil {
			return nil, e
		}
		if e := snapshot(obj(b["response"])); e != nil {
			return nil, e
		}
		n, ok := number(b["output_index"])
		if ok && n >= 0 && n == float64(int(n)) {
			i := int(n)
			if m := obj(b["item"]); m != nil {
				if e := rewriteItem(i, m); e != nil {
					return nil, e
				}
			}
			typ := str(b["type"])
			kind := ""
			switch typ {
			case "response.content_part.added", "response.content_part.done", "response.output_text.annotation.added", "response.output_text.delta", "response.output_text.done", "response.refusal.delta", "response.refusal.done":
				kind = "message"
			case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done", "response.reasoning_summary_text.delta", "response.reasoning_summary_text.done", "response.reasoning_text.delta", "response.reasoning_text.done":
				kind = "reasoning"
			}
			if kind != "" {
				old, exists := b["item_id"].(string)
				if exists {
					if mapped := rawIDs[fmt.Sprintf("%d:%s", i, old)]; mapped != "" {
						b["item_id"] = mapped
					}
				} else if config.RepairMissingTerminalIDs != nil && *config.RepairMissingTerminalIDs {
					if mapped := ids[fmt.Sprintf("%s:%d", kind, i)]; mapped != "" {
						b["item_id"] = mapped
					}
				}
			}
		}
		return b, nil
	}
}
func restoreAnthropicNames(b map[string]any) {
	for _, x := range list(b["content"]) {
		m := obj(x)
		if m["type"] == "tool_use" {
			m["name"] = strings.TrimPrefix(str(m["name"]), "cx_")
		}
	}
	if m := obj(b["content_block"]); m["type"] == "tool_use" {
		m["name"] = strings.TrimPrefix(str(m["name"]), "cx_")
	}
}
func escapeAnthropicNames(b map[string]any) {
	escape := func(m map[string]any) {
		if s := str(m["name"]); s != "" && !strings.HasPrefix(s, "cx_") {
			m["name"] = "cx_" + s
		}
	}
	for _, x := range list(b["tools"]) {
		escape(obj(x))
	}
	if m := obj(b["tool_choice"]); m["type"] == "tool" {
		escape(m)
	}
	for _, x := range list(b["messages"]) {
		for _, part := range list(obj(x)["content"]) {
			m := obj(part)
			if m["type"] == "tool_use" {
				escape(m)
			}
		}
	}
}

// ChatEOFTolerance exposes the pinned adapter EOF policy; it never authorizes
// completing a structurally incomplete stream.
func (p Resolved) ChatEOFTolerance() *bool { return boolPtr(p.fields["openaiChatEofTolerance"]) }

// TerminalRepairPolicy applies only after the stream has a structurally complete
// output graph. A quiet interval alone never authorizes synthetic success.
type TerminalRepairPolicy struct {
	GraceMS int64 `json:"graceMs"`
}

func (p Resolved) ResponsesTerminalRepair() *TerminalRepairPolicy {
	raw := p.modelValue("modelResponsesTerminalRepair")
	if raw == nil {
		return nil
	}
	n, ok := number(obj(raw)["graceMs"])
	if !ok || n <= 0 || n != float64(int64(n)) {
		return nil
	}
	return &TerminalRepairPolicy{GraceMS: int64(n)}
}
