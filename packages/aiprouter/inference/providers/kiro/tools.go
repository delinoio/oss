// SPDX-License-Identifier: Apache-2.0
// Tool lowering and catalog admission adapted from pinned opencodex; see LICENSE.upstream.
package kiro

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

type normalizedTool struct {
	definition  ToolDefinition
	schema      object
	description string
	loaded      bool
}

func wireName(namespace, name string) string {
	if namespace == "" || namespace == "functions" {
		return name
	}
	return namespace + "__" + name
}

func normalizeTools(body object, wire inference.Wire) ([]normalizedTool, error) {
	var out []normalizedTool
	var visit func([]any, string, bool) error
	visit = func(list []any, namespace string, loaded bool) error {
		for _, raw := range list {
			t := obj(raw)
			if t == nil {
				return failure(400, "kiro_invalid_tool")
			}
			kind := str(t["type"])
			if kind == "namespace" {
				if namespace != "" {
					return failure(400, "kiro_nested_namespace")
				}
				if err := visit(arr(t["tools"]), str(t["name"]), loaded); err != nil {
					return err
				}
				continue
			}
			if f := obj(t["function"]); kind == "function" && f != nil {
				t = f
			}
			toolNamespace := namespace
			if toolNamespace == "functions" {
				toolNamespace = ""
			}
			n := normalizedTool{definition: ToolDefinition{Name: str(t["name"]), Namespace: toolNamespace, Kind: FunctionTool}, schema: obj(t["parameters"]), description: str(t["description"]), loaded: loaded}
			if wire == inference.Anthropic && n.schema == nil {
				n.schema = obj(t["input_schema"])
			}
			switch kind {
			case "custom":
				n.definition.Kind = CustomTool
				description := "Raw freeform input for this tool."
				if n.definition.Name == "apply_patch" {
					description = "Raw tool input. For apply_patch, begin exactly with `*** Begin Patch` (no trailing `***`), then use its standard patch envelope."
				}
				n.schema = object{"type": "object", "properties": object{"input": object{"type": "string", "description": description}}, "required": []any{"input"}}
			case "tool_search":
				n.definition.Name, n.definition.Kind = "tool_search", SearchTool
				if n.description == "" {
					n.description = "Search for additional tools to load for the next turn."
				}
				if n.schema == nil {
					n.schema = object{"type": "object", "properties": object{"query": object{"type": "string", "description": "Search query for tools to load."}, "limit": object{"type": "number", "description": "Maximum number of tools to return."}}, "required": []any{"query"}}
				}
			case "image_generation", "image_gen":
				n.definition.Name, n.definition.Kind, n.definition.Namespace = "image_gen", HostedImageTool, ""
				n.description = "Generate an image from a text prompt. Returns temporary image artifact URLs. Use when the user asks to create or draw an image."
				n.schema = object{"type": "object", "properties": object{"prompt": object{"type": "string", "description": "Detailed image generation prompt. Required."}, "n": object{"type": "integer"}, "aspect_ratio": object{"type": "string", "enum": []any{"1:1", "16:9", "9:16", "4:3", "3:4", "auto"}}}, "required": []any{"prompt"}}
			case "web_search", "web_search_preview":
				n.definition.Name, n.definition.Kind, n.definition.Namespace = "web_search", HostedSearchTool, ""
				n.description = "Search the web for current, real-world, or post-training-cutoff information. Returns a concise answer synthesized from live results, with sources."
				n.schema = object{"type": "object", "properties": object{"query": object{"type": "string"}, "queries": object{"type": "array", "items": object{"type": "string"}}}}
			}
			if n.definition.Name == "" {
				return inference.ErrUnsupported
			}
			n.definition.WireName = wireName(n.definition.Namespace, n.definition.Name)
			if n.definition.Kind == HostedImageTool || n.definition.Kind == HostedSearchTool {
				n.definition.Configuration, _ = json.Marshal(t)
			}
			// Hosted image identity takes precedence over root client aliases, in
			// either declaration order. A namespaced image_gen stays independent.
			if n.definition.Namespace == "" && n.definition.Name == "image_gen" {
				insert := -1
				for i := 0; i < len(out); i++ {
					if out[i].definition.Namespace == "" && out[i].definition.Name == "image_gen" {
						if out[i].definition.Kind == HostedImageTool && n.definition.Kind != HostedImageTool {
							insert = -2
							break
						}
						if n.definition.Kind == HostedImageTool {
							if insert == -1 {
								insert = i
							}
							out = append(out[:i], out[i+1:]...)
							i--
						}
					}
				}
				if insert == -2 {
					continue
				}
				if insert >= 0 {
					out = append(out, normalizedTool{})
					copy(out[insert+1:], out[insert:])
					out[insert] = n
					continue
				}
			}
			out = append(out, n)
		}
		return nil
	}
	if err := visit(arr(body["tools"]), "", false); err != nil {
		return nil, err
	}
	for _, raw := range arr(body["input"]) {
		m := obj(raw)
		if str(m["type"]) == "additional_tools" || str(m["type"]) == "tool_search_output" {
			if err := visit(arr(m["tools"]), "", true); err != nil {
				return nil, err
			}
		}
	}
	// Search is a distinct identity even when an ordinary function has its name.
	used := map[string]bool{}
	for _, n := range out {
		if n.definition.Kind != SearchTool {
			used[n.definition.WireName] = true
		}
	}
	for i := range out {
		n := &out[i]
		if n.definition.Kind == SearchTool {
			name := "tool_search"
			if used[name] {
				name = "opencodex_tool_search"
			}
			for suffix := 2; used[name]; suffix++ {
				name = fmt.Sprintf("opencodex_tool_search_%d", suffix)
			}
			n.definition.WireName = name
			used[name] = true
		}
	}
	seen := map[string]normalizedTool{}
	unique := []normalizedTool{}
	for _, n := range out {
		if prior, ok := seen[n.definition.WireName]; ok {
			sa, _ := json.Marshal(prior.schema)
			sb, _ := json.Marshal(n.schema)
			if prior.definition.Kind != n.definition.Kind || string(prior.definition.Configuration) != string(n.definition.Configuration) || prior.description != n.description || string(sa) != string(sb) {
				return nil, failure(400, "kiro_ambiguous_tool_catalog")
			}
			if n.loaded {
				for i := range unique {
					if unique[i].definition.WireName == n.definition.WireName {
						unique[i].loaded = true
						break
					}
				}
			}
			continue
		}
		seen[n.definition.WireName] = n
		unique = append(unique, n)
	}
	return unique, nil
}

func convertTools(body object, wire inference.Wire, model string, names *registry) ([]any, string, error) {
	normalized, err := normalizeTools(body, wire)
	if err != nil {
		return nil, "", err
	}
	type candidate struct {
		tool            normalizedTool
		converted       any
		alias           string
		index, priority int
	}
	candidates := []candidate{}
	for i, t := range normalized {
		alias, err := names.alias(t.definition.WireName)
		if err != nil {
			return nil, "", err
		}
		if body["tool_choice"] == "none" {
			continue
		}
		schema := sanitizeSchema(t.schema, 0)
		if schema == nil {
			return nil, "", failure(400, "kiro_schema_too_deep")
		}
		flattenSchema(schema)
		description := t.description
		if description == "" {
			description = "Tool: " + t.definition.Name
		}
		limit := 1024
		if model == "gpt-5.6-sol" {
			limit = 9216
		}
		runes := []rune(description)
		if len(runes) > limit {
			description = string(runes[:limit-1]) + "…"
		}
		priority := 3
		if t.loaded {
			priority = 0
		} else if t.definition.Kind == CustomTool && t.definition.Name == "exec" && t.definition.Namespace == "" {
			priority = 1
		} else if t.definition.Kind == SearchTool {
			priority = 2
		}
		candidates = append(candidates, candidate{t, object{"toolSpecification": object{"name": alias, "description": description, "inputSchema": object{"json": schema}}}, alias, i, priority})
	}
	encodedSize := func(c []candidate) int {
		values := []any{}
		for _, v := range c {
			values = append(values, v.converted)
		}
		b, _ := json.Marshal(values)
		return len(b)
	}
	if len(candidates) > 48 || encodedSize(candidates) > 96000 {
		sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].priority < candidates[j].priority })
	}
	reserved := -1
	for i, c := range candidates {
		if c.tool.definition.Kind == CustomTool && c.tool.definition.Name == "exec" && c.tool.definition.Namespace == "" {
			reserved = i
			break
		}
	}
	admitted := map[int]bool{}
	filled := []candidate{}
	for i, c := range candidates {
		if i == reserved {
			continue
		}
		projected := append(append([]candidate(nil), filled...), c)
		if reserved >= 0 {
			projected = append(projected, candidates[reserved])
		}
		if len(projected) > 48 || encodedSize(projected) > 96000 {
			break
		}
		filled = append(filled, c)
		admitted[c.index] = true
	}
	if reserved >= 0 {
		if encodedSize([]candidate{candidates[reserved]}) > 96000 {
			return nil, "", failure(400, "kiro_exec_schema_limit")
		}
		admitted[candidates[reserved].index] = true
	}
	tools := []any{}
	omitted := []string{}
	for _, c := range candidates {
		if admitted[c.index] {
			tools = append(tools, c.converted)
			names.advertised[c.alias] = c.tool.definition.WireName
			names.definitions[c.alias] = c.tool.definition
			names.catalog = append(names.catalog, c.tool.definition)
		} else {
			omitted = append(omitted, c.alias)
		}
	}
	notice := ""
	if len(omitted) > 0 {
		listed := omitted
		if len(listed) > 12 {
			listed = listed[:12]
		}
		summary := strings.Join(listed, ", ")
		if len(omitted) > 12 {
			summary += fmt.Sprintf(", and %d more", len(omitted)-12)
		}
		notice = fmt.Sprintf("[opencodex] Kiro's outbound catalog budget allows %d of %d client tools this turn. Omitted and unavailable this turn: %s.", len(tools), len(candidates), summary)
	}
	return tools, notice, nil
}
