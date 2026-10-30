// SPDX-License-Identifier: Apache-2.0
package google

import "github.com/delinoio/oss/packages/tairouter/inference"

func filterToolSubset(tools, choice any, wire inference.Wire) (any, any, error) {
	c := obj(choice)
	if c["type"] != "allowed_tools" {
		return tools, choice, nil
	}
	spec := c
	if wire == inference.Chat && obj(c["allowed_tools"]) != nil {
		spec = obj(c["allowed_tools"])
	}
	mode := text(spec["mode"])
	if mode == "" {
		mode = "auto"
	}
	if mode != "auto" && mode != "required" {
		return nil, nil, invalid()
	}
	list, ok := spec["tools"].([]any)
	if !ok || len(list) == 0 {
		return nil, nil, invalid()
	}
	names := map[string]bool{}
	for _, raw := range list {
		t := obj(raw)
		if t["type"] != "function" {
			return nil, nil, unsupported()
		}
		name := text(t["name"])
		if name == "" {
			name = text(obj(t["function"])["name"])
		}
		if !validSourceToolName(name) {
			return nil, nil, invalid()
		}
		names[name] = false
	}
	var selected []any
	for _, raw := range array(tools) {
		t := obj(raw)
		if wire == inference.Chat {
			t = obj(t["function"])
		}
		if _, ok := names[text(t["name"])]; ok {
			names[text(t["name"])] = true
			selected = append(selected, raw)
		}
	}
	for _, found := range names {
		if !found {
			return nil, nil, invalid()
		}
	}
	return selected, mode, nil
}
