// SPDX-License-Identifier: Apache-2.0
package ollama

import "encoding/json"

// normalizeTools lowers Chat namespace/custom declarations before the native codec.
// Responses and Messages already use the shared translator's function carriers.
func normalizeTools(b object) error {
	var tools []any
	var visit func([]any, string, int) error
	visit = func(input []any, namespace string, depth int) error {
		if depth > 8 || len(tools) > 4096 {
			return bad("ollama_tool_catalog_limit")
		}
		for _, v := range input {
			t := obj(v)
			if t["type"] == "namespace" {
				if e := visit(arr(t["tools"]), str(t["name"]), depth+1); e != nil {
					return e
				}
				continue
			}
			f := obj(t["function"])
			if t["type"] == "custom" {
				custom := obj(t["custom"])
				f = object{"name": custom["name"], "description": custom["description"], "namespace": custom["namespace"], "parameters": object{"type": "object", "properties": object{"input": object{"type": "string"}}, "required": []any{"input"}, "additionalProperties": false}}
				t = object{"type": "function", "function": f}
			}
			ns := namespace
			if ns == "" {
				ns = str(f["namespace"])
			}
			if ns != "" && f != nil {
				f["name"] = ns + "__" + str(f["name"])
				delete(f, "namespace")
			}
			tools = append(tools, t)
		}
		return nil
	}
	if e := visit(arr(b["tools"]), "", 0); e != nil {
		return e
	}
	if b["tools"] != nil {
		b["tools"] = tools
	}
	for _, v := range arr(b["messages"]) {
		m := obj(v)
		calls := arr(m["tool_calls"])
		for i, v := range calls {
			c := obj(v)
			f := obj(c["function"])
			if c["type"] == "custom" {
				custom := obj(c["custom"])
				args, _ := json.Marshal(object{"input": str(custom["input"])})
				f = object{"name": custom["name"], "namespace": custom["namespace"], "arguments": string(args)}
				c = object{"id": c["id"], "type": "function", "function": f}
				calls[i] = c
			}
			if ns := str(f["namespace"]); ns != "" {
				f["name"] = ns + "__" + str(f["name"])
				delete(f, "namespace")
			}
		}
	}
	return nil
}
