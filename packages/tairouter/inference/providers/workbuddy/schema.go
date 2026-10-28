// SPDX-License-Identifier: Apache-2.0
// Schema policy adapted from opencodex 88aaa754; see LICENSE.upstream.
package workbuddy

import (
	"encoding/json"
	"strings"
)

type object = map[string]any

func obj(v any) object { m, _ := v.(map[string]any); return m }
func arr(v any) []any  { a, _ := v.([]any); return a }
func str(v any) string { s, _ := v.(string); return s }

func schema(v any, depth int) (object, error) {
	if depth > 64 {
		return nil, bad("workbuddy_schema_depth")
	}
	if v == false {
		return object{"not": object{}}, nil
	}
	in := obj(v)
	out := object{}
	for k, c := range in {
		out[k] = c
		switch k {
		case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
			if m := obj(c); m != nil {
				n := object{}
				for name, s := range m {
					x, e := schema(s, depth+1)
					if e != nil {
						return nil, e
					}
					n[name] = x
				}
				out[k] = n
			}
		case "anyOf", "oneOf", "allOf", "prefixItems":
			if a, ok := c.([]any); ok {
				n := []any{}
				for _, s := range a {
					x, e := schema(s, depth+1)
					if e != nil {
						return nil, e
					}
					n = append(n, x)
				}
				out[k] = n
			}
		case "items", "additionalProperties", "unevaluatedProperties", "contains", "not", "if", "then", "else", "propertyNames":
			if _, ok := c.(bool); ok && (k == "additionalProperties" || k == "unevaluatedProperties") {
				continue
			}
			if a, ok := c.([]any); ok && k == "items" {
				n := []any{}
				for _, s := range a {
					x, e := schema(s, depth+1)
					if e != nil {
						return nil, e
					}
					n = append(n, x)
				}
				out[k] = object{"anyOf": n}
			} else {
				x, e := schema(c, depth+1)
				if e != nil {
					return nil, e
				}
				out[k] = x
			}
		}
	}
	if p, ok := out["pattern"].(string); ok {
		var b strings.Builder
		for i := 0; i < len(p); i++ {
			if p[i] == '\\' && i+1 < len(p) {
				if p[i+1] == '\\' {
					b.WriteString(`\\`)
					i++
					continue
				}
				if p[i+1] == '0' && (i+2 == len(p) || p[i+2] < '0' || p[i+2] > '9') {
					b.WriteString(`\u0000`)
					i++
					continue
				}
			}
			b.WriteByte(p[i])
		}
		out["pattern"] = b.String()
	}
	if _, exists := out["required"]; exists {
		if _, ok := out["required"].([]any); !ok {
			delete(out, "required")
		}
	}
	if t, ok := out["type"].(string); ok {
		t = strings.ToLower(t)
		switch t {
		case "string", "number", "integer", "boolean", "object", "array", "null":
			out["type"] = t
		default:
			delete(out, "type")
		}
	}
	if values, ok := out["enum"].([]any); ok {
		if len(values) == 0 {
			delete(out, "enum")
		} else if _, exists := out["type"]; !exists {
			groups := map[string][]any{}
			order := []string{}
			for _, v := range values {
				typ := "object"
				switch v.(type) {
				case nil:
					typ = "null"
				case string:
					typ = "string"
				case bool:
					typ = "boolean"
				case json.Number, float64:
					typ = "number"
				case []any:
					typ = "array"
				}
				if _, ok := groups[typ]; !ok {
					order = append(order, typ)
				}
				groups[typ] = append(groups[typ], v)
			}
			if len(order) == 1 {
				out["type"] = order[0]
			} else {
				variants := []any{}
				for _, typ := range order {
					variants = append(variants, object{"type": typ, "enum": groups[typ]})
				}
				out["allOf"] = append(arr(out["allOf"]), object{"anyOf": variants})
				delete(out, "enum")
			}
		}
	}
	return out, nil
}

// ToolParameters normalizes schema positions only. Defaults and examples remain data.
func ToolParameters(v any) (object, error) {
	out, e := schema(v, 0)
	if e != nil {
		return nil, e
	}
	props := object{}
	for k, v := range obj(out["properties"]) {
		props[k] = v
	}
	required := []any{}
	seen := map[string]bool{}
	add := func(v any) {
		if s, ok := v.(string); ok && !seen[s] {
			seen[s] = true
			required = append(required, s)
		}
	}
	for _, v := range arr(out["required"]) {
		add(v)
	}
	for _, k := range []string{"anyOf", "oneOf", "allOf"} {
		for _, v := range arr(out[k]) {
			r := obj(v)
			for name, p := range obj(r["properties"]) {
				props[name] = p
			}
			if k == "allOf" {
				for _, v := range arr(r["required"]) {
					add(v)
				}
			}
		}
		delete(out, k)
	}
	out["type"] = "object"
	out["properties"] = props
	if len(required) > 0 {
		out["required"] = required
	}
	return out, nil
}
