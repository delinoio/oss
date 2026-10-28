// SPDX-License-Identifier: Apache-2.0
// Schema normalization adapts the pinned google-tool-schema.ts; see UPSTREAM-LICENSE.
package google

import (
	"encoding/json"
	"net/url"
	"reflect"
	"sort"
	"strings"
)

type SchemaPolicy string

const (
	SchemaCompatible  SchemaPolicy = "compatible"
	SchemaRejectLossy SchemaPolicy = "reject-lossy"
)

// SchemaLossReport contains counts only. Never put schema, property or tool names
// into this report: it is safe for the central executor's structured diagnostics.
type SchemaLossReport struct {
	Version              int            `json:"version"`
	EndpointClass        string         `json:"endpointClass"`
	Lossy                bool           `json:"lossy"`
	Truncated            bool           `json:"truncated"`
	UncertainComparisons int            `json:"uncertainComparisons"`
	Categories           map[string]int `json:"categories"`
}

func (r SchemaLossReport) clone() SchemaLossReport {
	m := map[string]int{}
	for k, v := range r.Categories {
		m[k] = v
	}
	r.Categories = m
	return r
}
func (r *SchemaLossReport) add(category string, count int) {
	if count <= 0 {
		return
	}
	r.Lossy = true
	n := r.Categories[category] + count
	if n >= 255 {
		if n > 255 {
			r.Truncated = true
		}
		n = 255
	}
	r.Categories[category] = n
}
func (r *SchemaLossReport) uncertain() {
	if r.UncertainComparisons < 255 {
		r.UncertainComparisons++
	} else {
		r.Truncated = true
	}
}
func (r *SchemaLossReport) merge(other SchemaLossReport) {
	for k, v := range other.Categories {
		r.add(k, v)
	}
	for i := 0; i < other.UncertainComparisons; i++ {
		r.uncertain()
	}
	r.Truncated = r.Truncated || other.Truncated
}

type schemaCompiler struct {
	remaining      int
	budgetReported bool
	defs           object
	active         map[string]bool
	report         *SchemaLossReport
}

func (c *schemaCompiler) budget() {
	if !c.budgetReported {
		c.report.add("node-budget-widened", 1)
		c.budgetReported = true
	}
}
func schemaKeys(m object) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SanitizeToolSchema returns a new provider-compatible tree and a bounded loss
// report. References resolve only inside this schema's root definitions.
func SanitizeToolSchema(value any, cca bool) (object, SchemaLossReport) {
	endpoint := "ai-studio"
	if cca {
		endpoint = "cloud-code-assist"
	}
	r := SchemaLossReport{Version: 1, EndpointClass: endpoint, Categories: map[string]int{}}
	if value == nil {
		return object{"type": "object", "properties": object{}}, r
	}
	c := schemaCompiler{remaining: 1024, defs: object{}, active: map[string]bool{}, report: &r}
	root := obj(value)
	for _, bag := range []string{"$defs", "definitions"} {
		if v, exists := root[bag]; exists && obj(v) == nil {
			r.add("invalid-schema-widened", 1)
		}
		for k, v := range obj(root[bag]) {
			if _, exists := c.defs[k]; !exists {
				c.defs[k] = v
			}
		}
	}
	out := c.schema(value, 0, 0, false)
	if out == nil {
		out = object{}
	}
	if out["type"] != "object" {
		r.add("root-object-coerced", 1)
	}
	out["type"] = "object"
	if obj(out["properties"]) == nil {
		out["properties"] = object{}
	}
	return out, r
}

func (c *schemaCompiler) dropped(n object) {
	for _, k := range []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum"} {
		if _, ok := n[k]; ok {
			c.report.add("numeric-bound-dropped", 1)
		}
	}
	for _, k := range []string{"minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties"} {
		if v, ok := n[k]; ok {
			if strings.HasPrefix(k, "min") && v == json.Number("0") {
				continue
			}
			c.report.add("size-bound-dropped", 1)
		}
	}
	for _, k := range []string{"allOf", "oneOf", "not", "multipleOf", "pattern", "$dynamicRef", "$recursiveRef", "additionalProperties", "additionalItems", "uniqueItems", "contains", "minContains", "maxContains", "dependencies", "dependentRequired", "dependentSchemas", "patternProperties", "propertyNames", "unevaluatedProperties", "unevaluatedItems"} {
		if v, ok := n[k]; ok {
			if k == "additionalProperties" && v == true || k == "uniqueItems" && v == false {
				continue
			}
			c.report.add("unsupported-constraint-dropped", 1)
		}
	}
	for _, k := range []string{"if", "then", "else"} {
		if _, ok := n[k]; ok {
			c.report.add("conditional-dropped", 1)
		}
	}
	if v, ok := n["prefixItems"]; ok {
		if a, ok := v.([]any); !ok || len(a) > 0 {
			c.report.add("tuple-prefix-dropped", 1)
		}
	}
}
func (c *schemaCompiler) resolve(ref string) object {
	key := ""
	for _, prefix := range []string{"#/$defs/", "#/definitions/"} {
		if strings.HasPrefix(ref, prefix) {
			key = strings.TrimPrefix(ref, prefix)
			break
		}
	}
	if key == "" {
		return nil
	}
	key = strings.ReplaceAll(strings.ReplaceAll(key, "~1", "/"), "~0", "~")
	key, err := url.PathUnescape(key)
	if err != nil {
		return nil
	}
	return obj(c.defs[key])
}

var mergeSchemaKeys = []string{"type", "nullable", "description", "format", "enum", "const", "properties", "items", "required", "anyOf"}

func (c *schemaCompiler) mergeRef(target, overlay object) (object, bool) {
	m := object{}
	if v, ok := target["$ref"]; ok {
		m["$ref"] = v
	}
	enumOverride := false
	for _, k := range mergeSchemaKeys {
		v, over := overlay[k]
		old, exists := target[k]
		if over {
			if exists && k != "description" {
				if k == "enum" && array(v) != nil && array(old) != nil {
					enumOverride = true
					c.enumOverlay(array(old), array(v))
				} else {
					c.compareLoss(k, v, old, "ref-overlay-replaced")
				}
			}
			m[k] = v
		} else if exists {
			m[k] = old
		}
	}
	return m, enumOverride
}

func (c *schemaCompiler) schema(value any, depth, refs int, preserveNull bool) object {
	return c.walk(value, depth, refs, preserveNull, false)
}
func (c *schemaCompiler) walk(value any, depth, refs int, preserveNull, enumOverride bool) object {
	if c.remaining <= 0 {
		c.budget()
		return nil
	}
	c.remaining--
	if depth >= 24 {
		c.report.add("depth-limit-widened", 1)
		return object{}
	}
	n := obj(value)
	if n == nil {
		c.report.add("invalid-schema-widened", 1)
		return object{}
	}
	c.dropped(n)
	if ref, exists := n["$ref"]; exists {
		if _, ok := ref.(string); !ok {
			c.report.add("invalid-schema-widened", 1)
		} else if refs >= 16 {
			c.report.add("dereference-limit-widened", 1)
		} else if target := c.resolve(text(ref)); target != nil {
			if c.active[text(ref)] {
				c.report.add("recursive-ref-widened", 1)
				return object{}
			}
			c.active[text(ref)] = true
			c.dropped(target)
			merged, override := c.mergeRef(target, n)
			out := c.walk(merged, depth, refs+1, preserveNull, override)
			delete(c.active, text(ref))
			return out
		} else {
			c.report.add("invalid-schema-widened", 1)
		}
	}
	out := object{}
	types := array(n["type"])
	if types == nil {
		types = []any{n["type"]}
	} else if len(types) == 0 {
		c.report.add("unsupported-type-widened", 1)
	}
	null := false
	seen := map[string]bool{}
	for _, v := range types {
		if v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok {
			c.report.add("unsupported-type-widened", 1)
			continue
		}
		s = strings.ToLower(s)
		switch s {
		case "null":
			null = true
		case "object", "array", "string", "number", "integer", "boolean":
			seen[s] = true
			if out["type"] == nil {
				out["type"] = s
			}
		default:
			c.report.add("unsupported-type-widened", 1)
		}
	}
	if len(seen) > 1 {
		c.report.add("type-union-collapsed", 1)
	}
	if null {
		if out["type"] != nil {
			out["nullable"] = true
		} else if preserveNull {
			out["type"] = "null"
		} else {
			out["nullable"] = true
			c.report.add("unsupported-type-widened", 1)
		}
	}
	if v, exists := n["nullable"]; exists {
		if b, ok := v.(bool); ok {
			if !b && null {
				c.report.add("nullability-overridden", 1)
			}
			out["nullable"] = b
		} else {
			c.report.add("invalid-schema-widened", 1)
		}
	}
	for _, k := range []string{"description", "format"} {
		if v, ok := n[k].(string); ok {
			out[k] = v
		} else if _, exists := n[k]; exists && k == "format" {
			c.report.add("invalid-schema-widened", 1)
		}
	}
	values, hasEnum := n["enum"]
	if hasEnum {
		if a, ok := values.([]any); !ok {
			c.report.add("invalid-schema-widened", 1)
		} else if len(a) == 0 {
			c.report.add("enum-constraint-dropped", 1)
		}
	}
	if v, exists := n["const"]; exists {
		if s, ok := v.(string); !ok {
			c.report.add("const-value-filtered", 1)
		} else if hasEnum {
			a := uniqueStrings(array(values))
			if len(a) != 1 || a[0] != s {
				c.report.add("const-value-filtered", 1)
			}
		} else {
			values = []any{s}
		}
	}
	if vals := array(values); vals != nil {
		if !enumOverride {
			for _, v := range vals {
				if _, ok := v.(string); !ok {
					c.report.add("enum-value-filtered", 1)
				}
			}
		}
		if a := uniqueStrings(vals); len(a) > 0 {
			out["enum"] = a
		}
	}
	if v, exists := n["properties"]; exists {
		if props := obj(v); props != nil {
			p := object{}
			for _, k := range schemaKeys(props) {
				if c.remaining <= 0 {
					c.budget()
					break
				}
				if child := c.schema(props[k], depth+1, refs, false); child != nil {
					p[k] = child
				}
			}
			out["properties"] = p
		} else {
			c.report.add("invalid-schema-widened", 1)
		}
	}
	if v, exists := n["required"]; exists {
		if a, ok := v.([]any); ok {
			var required []any
			seen := map[string]bool{}
			for _, v := range a {
				s, ok := v.(string)
				if !ok {
					c.report.add("invalid-schema-widened", 1)
					continue
				}
				if seen[s] {
					continue
				}
				seen[s] = true
				if _, ok := obj(out["properties"])[s]; ok {
					required = append(required, s)
				} else {
					c.report.add("unsupported-constraint-dropped", 1)
				}
			}
			if len(required) > 0 {
				out["required"] = required
			}
		} else {
			c.report.add("invalid-schema-widened", 1)
		}
	}
	if c.remaining > 0 {
		if v, exists := n["items"]; exists {
			if array(v) != nil {
				c.report.add("tuple-prefix-dropped", 1)
			} else if obj(v) != nil {
				if child := c.schema(v, depth+1, refs, false); child != nil {
					out["items"] = child
				}
			} else {
				c.report.add("invalid-schema-widened", 1)
			}
		}
	} else if n["items"] != nil {
		c.budget()
	}
	if v, exists := n["anyOf"]; exists {
		if c.remaining <= 0 {
			c.budget()
		} else {
			normalized := c.union(v, depth, refs)
			for k, v := range normalized {
				if old, exists := out[k]; exists {
					c.compareLoss(k, old, v, "union-sibling-replaced")
				}
				out[k] = v
			}
		}
	}
	if out["type"] == "array" && out["items"] == nil {
		if c.remaining <= 0 {
			c.budget()
			return nil
		}
		c.remaining--
		out["items"] = object{"type": "string"}
	}
	return out
}

func uniqueStrings(a []any) []any {
	var out []any
	seen := map[string]bool{}
	for _, v := range a {
		if s, ok := v.(string); ok && !seen[s] {
			out = append(out, s)
			seen[s] = true
		}
	}
	return out
}
func (c *schemaCompiler) union(v any, depth, refs int) object {
	a, ok := v.([]any)
	if !ok || len(a) == 0 {
		c.report.add("invalid-schema-widened", 1)
		return object{}
	}
	var branches []object
	var nonnull []object
	nulls := 0
	for _, v := range a {
		if c.remaining <= 0 {
			c.budget()
			return object{}
		}
		b := c.schema(v, depth+1, refs, true)
		if b == nil {
			return object{}
		}
		branches = append(branches, b)
		if b["type"] == "null" && len(b) == 1 {
			nulls++
		} else {
			nonnull = append(nonnull, b)
		}
	}
	if len(nonnull) == 1 && nulls > 0 {
		out := nonnull[0]
		out["nullable"] = true
		return out
	}
	t := branches[0]["type"]
	var enums []any
	enumOnly := t != "null"
	for _, b := range branches {
		if b["type"] != t || array(b["enum"]) == nil {
			enumOnly = false
		}
		for k := range b {
			if k != "type" && k != "enum" {
				enumOnly = false
			}
		}
		enums = append(enums, array(b["enum"])...)
	}
	if enumOnly {
		out := object{"enum": uniqueStrings(enums)}
		if t != nil {
			out["type"] = t
		}
		return out
	}
	c.report.add("union-widened", 1)
	return object{}
}

// Bounded comparisons keep strict-mode uncertainty distinct from proven loss.
func boundedEqual(a, b any, budget *int, depth int) int {
	if depth >= 24 || *budget <= 0 {
		return -1
	}
	*budget--
	if am := obj(a); am != nil {
		bm := obj(b)
		if bm == nil || len(am) != len(bm) {
			return 0
		}
		for _, k := range schemaKeys(am) {
			*budget--
			v, ok := bm[k]
			if !ok {
				return 0
			}
			if eq := boundedEqual(am[k], v, budget, depth+1); eq != 1 {
				return eq
			}
		}
		return 1
	}
	if aa, ok := a.([]any); ok {
		bb, ok := b.([]any)
		if !ok || len(aa) != len(bb) {
			return 0
		}
		for i := range aa {
			*budget--
			if eq := boundedEqual(aa[i], bb[i], budget, depth+1); eq != 1 {
				return eq
			}
		}
		return 1
	}
	if reflect.DeepEqual(a, b) {
		return 1
	}
	return 0
}
func setEqual(a, b []any, budget *int) int {
	unique := func(values []any) ([]any, int) {
		var out []any
		for _, v := range values {
			*budget--
			if *budget <= 0 {
				return nil, -1
			}
			found := false
			for _, old := range out {
				eq := boundedEqual(v, old, budget, 0)
				if eq < 0 {
					return nil, -1
				}
				if eq == 1 {
					found = true
					break
				}
			}
			if !found {
				out = append(out, v)
			}
		}
		return out, 1
	}
	aa, ok := unique(a)
	if ok < 0 {
		return -1
	}
	bb, ok := unique(b)
	if ok < 0 {
		return -1
	}
	if len(aa) != len(bb) {
		return 0
	}
	for _, v := range aa {
		found := false
		for _, w := range bb {
			eq := boundedEqual(v, w, budget, 0)
			if eq < 0 {
				return -1
			}
			if eq == 1 {
				found = true
				break
			}
		}
		if !found {
			return 0
		}
	}
	return 1
}
func (c *schemaCompiler) compareLoss(key string, a, b any, category string) {
	budget := 1024
	eq := 0
	if key == "enum" && array(a) != nil && array(b) != nil {
		eq = setEqual(array(a), array(b), &budget)
	} else if key == "type" || key == "required" {
		aa := array(a)
		if aa == nil {
			aa = []any{a}
		}
		bb := array(b)
		if bb == nil {
			bb = []any{b}
		}
		normalize := func(v []any) []any {
			r := make([]any, len(v))
			for i, x := range v {
				if s, ok := x.(string); ok && key == "type" {
					r[i] = strings.ToLower(s)
				} else {
					r[i] = x
				}
			}
			return r
		}
		eq = setEqual(normalize(aa), normalize(bb), &budget)
	} else {
		eq = boundedEqual(a, b, &budget, 0)
	}
	if eq < 0 {
		c.report.uncertain()
	} else if eq == 0 {
		c.report.add(category, 1)
	}
}
func (c *schemaCompiler) enumOverlay(target, overlay []any) {
	budget := 1024
	stringsInTarget := map[string]bool{}
	emitted := map[string]bool{}
	for _, v := range target {
		budget--
		if budget <= 0 {
			c.report.uncertain()
			return
		}
		if s, ok := v.(string); ok {
			stringsInTarget[s] = true
		}
	}
	for _, v := range overlay {
		budget--
		if budget <= 0 {
			c.report.uncertain()
			return
		}
		if s, ok := v.(string); ok {
			emitted[s] = true
		}
	}
	widens := false
	for s := range emitted {
		if !stringsInTarget[s] {
			widens = true
		}
	}
	var filtered []any
	count := 0
	for _, v := range overlay {
		if _, ok := v.(string); ok {
			continue
		}
		duplicate := false
		for _, old := range filtered {
			eq := boundedEqual(v, old, &budget, 0)
			if eq < 0 {
				c.report.uncertain()
				return
			}
			if eq == 1 {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		filtered = append(filtered, v)
		for _, candidate := range target {
			eq := boundedEqual(v, candidate, &budget, 0)
			if eq < 0 {
				c.report.uncertain()
				return
			}
			if eq == 1 {
				count++
				break
			}
		}
	}
	if widens {
		c.report.add("ref-overlay-replaced", 1)
	}
	c.report.add("enum-value-filtered", count)
}
