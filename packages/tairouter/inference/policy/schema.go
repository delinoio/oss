// SPDX-License-Identifier: Apache-2.0
package policy

import (
	"encoding/json"
	"strconv"
	"strings"
)

type schemaBudget struct{ nodes, expansions, bytes int }

func normalizeSchema(v, root any, b *schemaBudget, depth int, active map[string]bool) any {
	if depth > 64 || b.nodes <= 0 {
		return map[string]any{}
	}
	b.nodes--
	if a, ok := v.([]any); ok {
		out := make([]any, len(a))
		for i, x := range a {
			out[i] = normalizeSchema(x, root, b, depth+1, active)
		}
		return out
	}
	m := obj(v)
	if m == nil {
		return v
	}
	ref := str(m["$ref"])
	if ref != "" && len(m) > 1 {
		bare := map[string]any{"$ref": ref}
		if active[ref] || b.expansions <= 0 {
			return bare
		}
		target := pointer(root, ref)
		if obj(target) == nil {
			return bare
		}
		raw, _ := json.Marshal(target)
		if len(raw) > b.bytes {
			return bare
		}
		b.bytes -= len(raw)
		b.expansions--
		active[ref] = true
		out := obj(normalizeSchema(target, root, b, depth+1, active))
		delete(active, ref)
		if out == nil {
			out = map[string]any{}
		}
		for k, x := range m {
			if k == "$ref" {
				continue
			}
			value := x
			if !schemaData(k) {
				value = normalizeSchema(x, root, b, depth+1, active)
			}
			switch k {
			case "required":
				a := append([]any{}, list(out[k])...)
				for _, n := range list(value) {
					found := false
					for _, old := range a {
						if old == n {
							found = true
						}
					}
					if !found {
						a = append(a, n)
					}
				}
				out[k] = a
			case "properties":
				old := obj(out[k])
				if old == nil {
					old = map[string]any{}
				}
				for name, prop := range obj(value) {
					if prior, exists := old[name]; exists {
						old[name] = map[string]any{"allOf": []any{prior, prop}}
					} else {
						old[name] = prop
					}
				}
				out[k] = old
			default:
				if a, ok := number(out[k]); ok {
					if n, ok := number(value); ok {
						switch k {
						case "minimum", "exclusiveMinimum", "minLength", "minItems", "minProperties":
							if a > n {
								value = out[k]
							}
						case "maximum", "exclusiveMaximum", "maxLength", "maxItems", "maxProperties":
							if a < n {
								value = out[k]
							}
						}
					}
				}
				out[k] = value
			}
		}
		return out
	}
	out := map[string]any{}
	for k, x := range m {
		if schemaData(k) {
			out[k] = x
		} else {
			out[k] = normalizeSchema(x, root, b, depth+1, active)
		}
	}
	return out
}
func schemaData(k string) bool {
	switch k {
	case "default", "const", "enum", "examples", "example":
		return true
	}
	return false
}
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, e := n.Float64()
		return f, e == nil
	case float64:
		return n, true
	}
	return 0, false
}
func pointer(root any, ref string) any {
	if !strings.HasPrefix(ref, "#/") {
		return nil
	}
	v := root
	for _, k := range strings.Split(ref[2:], "/") {
		k = strings.ReplaceAll(strings.ReplaceAll(k, "~1", "/"), "~0", "~")
		if m := obj(v); m != nil {
			v = m[k]
		} else if a := list(v); a != nil {
			i, e := strconv.Atoi(k)
			if e != nil || i < 0 || i >= len(a) {
				return nil
			}
			v = a[i]
		} else {
			return nil
		}
	}
	return v
}

// Artifact is a provider-owned built-in. Its documented relaxed schema must not
// weaken unrelated tools or literal data inside defaults and examples.
func relaxArtifact(v any) any {
	type task struct {
		value  any
		names  bool
		assign func(any)
	}
	var result any
	pending := []task{{value: v, assign: func(x any) { result = x }}}
	for len(pending) > 0 {
		n := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if a, ok := n.value.([]any); ok {
			out := make([]any, len(a))
			n.assign(out)
			for i, x := range a {
				pending = append(pending, task{value: x, assign: func(v any) { out[i] = v }})
			}
			continue
		}
		m := obj(n.value)
		if m == nil {
			n.assign(n.value)
			continue
		}
		out := map[string]any{}
		n.assign(out)
		for k, x := range m {
			if !n.names && (k == "pattern" || k == "anyOf") {
				continue
			}
			bag := false
			switch k {
			case "properties", "$defs", "definitions", "patternProperties", "dependentSchemas":
				bag = true
			}
			child := false
			switch k {
			case "items", "additionalProperties", "additionalItems", "allOf", "oneOf", "not", "if", "then", "else", "contains", "propertyNames", "prefixItems", "unevaluatedItems", "unevaluatedProperties":
				child = true
			}
			if n.names || bag || child {
				pending = append(pending, task{value: x, names: !n.names && bag, assign: func(v any) { out[k] = v }})
			} else {
				out[k] = x
			}
		}
	}
	return result
}
