// SPDX-License-Identifier: Apache-2.0
package policy

import "encoding/json"

// Only original custom/freeform declarations authorize bare-text tool recovery.
// Ordinary function schemas with an input property do not authorize this repair.
func FreeformTools(raw json.RawMessage) map[string]string {
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		return nil
	}
	result := map[string]string{}
	var visit func([]any, string, int)
	visit = func(tools []any, namespace string, depth int) {
		if depth > 8 {
			return
		}
		for _, v := range tools {
			t, ok := v.(map[string]any)
			if !ok {
				continue
			}
			kind, _ := t["type"].(string)
			if kind == "namespace" {
				nested, _ := t["tools"].([]any)
				ns, _ := t["name"].(string)
				visit(nested, ns, depth+1)
				continue
			}
			if kind != "custom" && kind != "freeform" {
				continue
			}
			definition := t
			if d, ok := t["custom"].(map[string]any); ok {
				definition = d
			}
			name, _ := definition["name"].(string)
			if name == "" {
				continue
			}
			ns := namespace
			if ns == "" {
				ns, _ = definition["namespace"].(string)
			}
			wireName := name
			if ns != "" && ns != "functions" {
				wireName = ns + "__" + name
			}
			result[wireName] = name
		}
	}
	tools, _ := body["tools"].([]any)
	visit(tools, "", 0)
	additional, _ := body["additional_tools"].([]any)
	visit(additional, "", 0)
	input, _ := body["input"].([]any)
	for _, item := range input {
		m, _ := item.(map[string]any)
		if m["type"] == "additional_tools" || m["type"] == "tool_search_output" {
			nested, _ := m["tools"].([]any)
			visit(nested, "", 0)
		}
	}
	return result
}
