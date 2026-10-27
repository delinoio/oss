// SPDX-License-Identifier: Apache-2.0
package workbuddy

import "github.com/delinoio/oss/packages/aiprouter/inference"

// toolMetadata derives restoration exclusively from this admitted request catalog.
// It preserves namespace and custom-tool identity through the Chat intermediary.
func toolMetadata(in inference.AdapterInput) (map[string]object, error) {
	b, e := decode(in.Body)
	if e != nil {
		return nil, e
	}
	out := map[string]object{}
	var visit func([]any, string, int) error
	visit = func(list []any, ns string, depth int) error {
		if depth > 64 || len(out) > 4096 {
			return bad("workbuddy_tool_limit")
		}
		for _, v := range list {
			t := obj(v)
			kind := str(t["type"])
			if kind == "namespace" {
				if e := visit(arr(t["tools"]), str(t["name"]), depth+1); e != nil {
					return e
				}
				continue
			}
			f := t
			if in.Wire == inference.Chat {
				if obj(t["function"]) != nil {
					f = obj(t["function"])
				} else if obj(t["custom"]) != nil {
					f = obj(t["custom"])
				}
			}
			if kind == "" {
				kind = "function"
			}
			name := str(f["name"])
			namespace := ns
			if namespace == "" {
				namespace = str(f["namespace"])
			}
			if namespace == "functions" {
				namespace = ""
			}
			canonical := name
			if namespace != "" {
				canonical = namespace + "__" + name
			}
			if name != "" {
				if _, ok := out[canonical]; ok {
					return bad("workbuddy_ambiguous_tool_catalog")
				}
				out[canonical] = object{"tool_kind": kind, "tool_name": name, "tool_namespace": namespace}
			}
		}
		return nil
	}
	catalog := append([]any{}, arr(b["tools"])...)
	catalog = append(catalog, arr(b["additional_tools"])...)
	for _, v := range arr(b["input"]) {
		m := obj(v)
		if m["type"] == "tool_search_output" || m["type"] == "additional_tools" {
			catalog = append(catalog, arr(m["tools"])...)
		}
	}
	if e = visit(catalog, "", 0); e != nil {
		return nil, e
	}
	return out, nil
}
