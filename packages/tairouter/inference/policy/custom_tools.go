// SPDX-License-Identifier: Apache-2.0
package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/delinoio/oss/packages/tairouter/providers"
)

var ErrCustomTool = errors.New("invalid provider custom tool compatibility")

type customIdentity struct {
	name, namespace string
	custom          bool
}

func toolIdentity(t map[string]any, namespace string) (string, customIdentity) {
	name := str(t["name"])
	if namespace == "" {
		namespace = str(t["namespace"])
	}
	wire := name
	if namespace != "" && namespace != "functions" {
		wire = namespace + "__" + name
	}
	return wire, customIdentity{name, namespace, t["type"] == "custom"}
}
func customCatalog(b map[string]any) (map[string]customIdentity, error) {
	names := map[string]customIdentity{}
	var visit func([]any, string, int) error
	visit = func(tools []any, namespace string, depth int) error {
		if depth > 8 {
			return ErrCustomTool
		}
		for _, x := range tools {
			t := obj(x)
			if t["type"] == "namespace" {
				if e := visit(list(t["tools"]), str(t["name"]), depth+1); e != nil {
					return e
				}
				continue
			}
			if t["type"] != "custom" && t["type"] != "function" {
				continue
			}
			wire, id := toolIdentity(t, namespace)
			if wire == "" {
				continue
			}
			if old, exists := names[wire]; exists && old != id {
				return ErrCustomTool
			}
			names[wire] = id
		}
		return nil
	}
	for _, tools := range [][]any{list(b["tools"]), list(b["additional_tools"])} {
		if e := visit(tools, "", 0); e != nil {
			return nil, e
		}
	}
	for _, x := range list(b["input"]) {
		m := obj(x)
		if m["type"] == "additional_tools" || m["type"] == "tool_search_output" {
			if e := visit(list(m["tools"]), "", 0); e != nil {
				return nil, e
			}
		}
	}

	for _, x := range list(b["input"]) {
		m := obj(x)
		if m["type"] == "custom_tool_call" {
			wire, id := toolIdentity(m, "")
			id.custom = true
			if wire != "" {
				if old, ok := names[wire]; ok && old != id {
					return nil, ErrCustomTool
				}
				names[wire] = id
			}
		}
	}
	return names, nil
}
func lowerCustomTools(b map[string]any) error {
	names, e := customCatalog(b)
	if e != nil {
		return e
	}
	hasCustom := false
	for _, id := range names {
		hasCustom = hasCustom || id.custom
	}
	if !hasCustom {
		for _, x := range list(b["input"]) {
			if obj(x)["type"] == "custom_tool_call_output" {
				return ErrCustomTool
			}
		}
		return nil
	}
	var tools func([]any, string) []any
	tools = func(in []any, namespace string) []any {
		out := []any{}
		for _, x := range in {
			m := obj(x)
			if m["type"] == "namespace" {
				out = append(out, tools(list(m["tools"]), str(m["name"]))...)
				continue
			}
			wire, id := toolIdentity(m, namespace)
			if id.name != "" && (m["type"] == "custom" || m["type"] == "function") {
				m["name"] = wire
				delete(m, "namespace")
			}
			if id.custom {
				m["type"] = "function"
				delete(m, "format")
				m["parameters"] = map[string]any{"type": "object", "properties": map[string]any{"input": map[string]any{"type": "string", "description": "Raw input for this client-executed custom tool."}}, "required": []any{"input"}, "additionalProperties": false}
			}
			out = append(out, x)
		}
		return out
	}
	if a, ok := b["tools"].([]any); ok {
		b["tools"] = tools(a, "")
	}
	if a, ok := b["additional_tools"].([]any); ok {
		b["additional_tools"] = tools(a, "")
	}
	callIDs := map[string]bool{}
	for _, x := range list(b["input"]) {
		m := obj(x)
		if m["type"] == "additional_tools" || m["type"] == "tool_search_output" {
			m["tools"] = tools(list(m["tools"]), "")
		}
		if m["type"] == "function_call" && str(m["namespace"]) != "" {
			wire, _ := toolIdentity(m, "")
			m["name"] = wire
			delete(m, "namespace")
		}
		if m["type"] == "custom_tool_call" {
			wire, _ := toolIdentity(m, "")
			if declared, ok := names[wire]; !ok || !declared.custom {
				return ErrCustomTool
			}
			raw, ok := m["input"].(string)
			if !ok {
				return ErrCustomTool
			}
			args, _ := json.Marshal(map[string]any{"input": raw})
			m["type"] = "function_call"
			m["name"] = wire
			delete(m, "namespace")
			delete(m, "input")
			delete(m, "id")
			m["arguments"] = string(args)
			callIDs[str(m["call_id"])] = true
		}
	}
	for _, x := range list(b["input"]) {
		m := obj(x)
		if m["type"] == "custom_tool_call_output" {
			if !callIDs[str(m["call_id"])] {
				return ErrCustomTool
			}
			m["type"] = "function_call_output"
		}
	}
	var choice func(map[string]any)
	choice = func(m map[string]any) {
		if m["type"] == "custom" || m["type"] == "function" {
			wire, _ := toolIdentity(m, "")
			if _, ok := names[wire]; ok {
				m["type"] = "function"
				m["name"] = wire
				delete(m, "namespace")
			}
		}
		for _, x := range list(m["tools"]) {
			choice(obj(x))
		}
	}
	choice(obj(b["tool_choice"]))
	return nil
}

// NewResponseRewriterForRequest also reverses provider-required custom-tool
// lowering using the original catalog. Argument fragments remain private until
// their JSON string is complete; malformed custom arguments fail explicitly.
func (p Resolved) NewResponseRewriterForRequest(original json.RawMessage) func(map[string]any) (map[string]any, error) {
	base := p.NewResponseRewriter()
	source, e := decode(original)
	names, err := customCatalog(source)
	if e != nil {
		err = e
	}
	enabled := p.Wire == providers.Responses && p.fields["supportsResponsesCustomTools"] == false
	type call struct {
		id   customIdentity
		args string
	}
	calls := map[string]*call{}
	retained := 0
	restore := func(m map[string]any, complete bool) error {
		if m["type"] != "function_call" {
			return nil
		}
		id, ok := names[str(m["name"])]
		if !ok {
			return nil
		}
		if id.namespace != "" {
			m["namespace"] = id.namespace
		}
		m["name"] = id.name
		if !id.custom {
			return nil
		}
		m["type"] = "custom_tool_call"
		if v, ok := m["id"]; ok {
			m["id"] = customItemID(v)
		}
		args := str(m["arguments"])
		delete(m, "arguments")
		if !complete {
			m["input"] = ""
			return nil
		}
		a, e := decode([]byte(args))
		if e != nil {
			return ErrCustomTool
		}
		input, ok := a["input"].(string)
		if !ok {
			return ErrCustomTool
		}
		m["input"] = input
		return nil
	}
	return func(in map[string]any) (map[string]any, error) {
		b, e := base(in)
		if e != nil {
			return nil, e
		}
		if !enabled {
			return b, nil
		}
		if err != nil {
			return nil, err
		}
		for _, snapshot := range []map[string]any{b, obj(b["response"])} {
			for _, x := range list(snapshot["output"]) {
				if e := restore(obj(x), true); e != nil {
					return nil, e
				}
			}
		}
		key := fmt.Sprint(b["output_index"])
		kind := str(b["type"])
		if item := obj(b["item"]); item != nil && item["type"] == "function_call" {
			id, ok := names[str(item["name"])]
			if ok && id.custom {
				if len(calls) >= 128 && calls[key] == nil {
					return nil, ErrCustomTool
				}
				if calls[key] == nil {
					calls[key] = &call{id: id, args: str(item["arguments"])}
					retained += len(calls[key].args)
					if retained > 2*1024*1024 {
						return nil, ErrCustomTool
					}
				}
				if e := restore(item, kind != "response.output_item.added"); e != nil {
					return nil, e
				}
			}
		}
		if c := calls[key]; c != nil {
			switch kind {
			case "response.function_call_arguments.delta":
				if retained+len(str(b["delta"])) > 2*1024*1024 {
					return nil, ErrCustomTool
				}
				retained += len(str(b["delta"]))
				c.args += str(b["delta"])
				b["type"] = "response.custom_tool_call_input.delta"
				b["delta"] = ""
				b["item_id"] = customItemID(b["item_id"])
			case "response.function_call_arguments.done":
				args := str(b["arguments"])
				if args == "" {
					args = c.args
				}
				a, e := decode([]byte(args))
				if e != nil {
					return nil, ErrCustomTool
				}
				input, ok := a["input"].(string)
				if !ok {
					return nil, ErrCustomTool
				}
				b["type"] = "response.custom_tool_call_input.done"
				delete(b, "arguments")
				b["input"] = input
				b["item_id"] = customItemID(b["item_id"])
				retained -= len(c.args)
				c.args = ""
			}
		}
		return b, nil
	}
}
func customItemID(v any) any {
	if s, ok := v.(string); ok && strings.HasPrefix(s, "fc_") {
		return "ctc_" + strings.TrimPrefix(s, "fc_")
	}
	return v
}
