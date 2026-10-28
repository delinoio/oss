// SPDX-License-Identifier: Apache-2.0
package hosted

import (
	"encoding/json"
	"github.com/delinoio/oss/packages/tairouter/inference"
	"strings"
)

type object = map[string]any

func obj(v any) object { m, _ := v.(map[string]any); return m }
func arr(v any) []any  { a, _ := v.([]any); return a }
func str(v any) string { s, _ := v.(string); return s }
func decode(b []byte) (object, error) {
	var m object
	if !json.Valid(b) || json.Unmarshal(b, &m) != nil || m == nil {
		return nil, fault(400, "hosted_invalid_json")
	}
	return m, nil
}

type tool struct {
	kind        Kind
	declaration json.RawMessage
}

func kind(t string) Kind {
	switch t {
	case "web_search", "web_search_preview":
		return Search
	case "image_generation", "image_gen":
		return Image
	}
	return ""
}
func name(k Kind) string {
	switch k {
	case Search:
		return "web_search"
	case Image:
		return "image_gen"
	case Video:
		return "video_gen"
	}
	return ""
}
func synthetic(k Kind, wire inference.Wire) object {
	p := object{"type": "object", "properties": object{"prompt": object{"type": "string"}}, "required": []any{"prompt"}}
	desc := "Generate media and return temporary downloadable artifacts."
	if k == Search {
		p = object{"type": "object", "properties": object{"query": object{"type": "string"}, "queries": object{"type": "array", "items": object{"type": "string"}}}}
		desc = "Search the web for current information with sources."
	}
	if k == Image {
		p["properties"].(object)["n"] = object{"type": "integer", "minimum": 1, "maximum": 4}
		p["properties"].(object)["aspect_ratio"] = object{"type": "string", "enum": []any{"1:1", "16:9", "9:16", "4:3", "3:4", "auto"}}
	}
	if k == Video {
		p["properties"].(object)["duration"] = object{"type": "integer", "minimum": 1, "maximum": 15}
		p["properties"].(object)["resolution"] = object{"type": "string", "enum": []any{"480p", "720p"}}
		p["properties"].(object)["aspect_ratio"] = object{"type": "string", "enum": []any{"16:9", "9:16", "1:1", "4:3", "3:4", "3:2", "2:3"}}
	}
	fn := object{"name": name(k), "description": desc, "parameters": p}
	switch wire {
	case inference.Chat:
		return object{"type": "function", "function": fn}
	case inference.Anthropic:
		return object{"name": name(k), "description": desc, "input_schema": p}
	default:
		fn["type"] = "function"
		return fn
	}
}

// catalog only intercepts explicitly declared hosted capabilities. Function names
// alone never authorize remote execution. Ambiguous function collisions fail closed.
func catalog(body object, wire inference.Wire, video bool, native map[Kind]bool, enabled map[Kind]bool) (map[string]tool, error) {
	active := map[string]tool{}
	tools := arr(body["tools"])
	for _, v := range tools {
		d := obj(v)
		if k := kind(str(d["type"])); k != "" && !native[k] && (enabled == nil || enabled[k]) {
			raw, _ := json.Marshal(d)
			active[name(k)] = tool{k, raw}
		}
	}
	if video && !native[Video] && (enabled == nil || enabled[Video]) {
		active[name(Video)] = tool{Video, json.RawMessage(`{"type":"video_generation"}`)}
	}
	if len(active) == 0 {
		return active, nil
	}
	out := []any{}
	added := map[string]bool{}
	for _, v := range tools {
		d := obj(v)
		if k := kind(str(d["type"])); k != "" && !native[k] && (enabled == nil || enabled[k]) {
			n := name(k)
			if !added[n] {
				out = append(out, synthetic(k, wire))
				added[n] = true
			}
			continue
		}
		n := str(d["name"])
		if f := obj(d["function"]); f != nil {
			n = str(f["name"])
		}
		if _, ok := active[n]; ok {
			return nil, fault(400, "hosted_tool_name_collision")
		}
		out = append(out, v)
	}
	if video && !native[Video] && (enabled == nil || enabled[Video]) {
		out = append(out, synthetic(Video, wire))
	}
	body["tools"] = out
	choice := obj(body["tool_choice"])
	if k := kind(str(choice["type"])); k != "" {
		if wire == inference.Chat {
			body["tool_choice"] = object{"type": "function", "function": object{"name": name(k)}}
		} else if wire == inference.Anthropic {
			body["tool_choice"] = object{"type": "tool", "name": name(k)}
		} else {
			body["tool_choice"] = object{"type": "function", "name": name(k)}
		}
	}
	return active, nil
}
func queries(arguments object) ([]string, error) {
	var values []any
	if q, ok := arguments["queries"]; ok {
		values = arr(q)
		if values == nil {
			return nil, fault(400, "hosted_invalid_queries")
		}
	} else {
		values = []any{arguments["query"]}
	}
	if len(values) == 0 || len(values) > 32 {
		return nil, fault(400, "hosted_invalid_queries")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, v := range values {
		q := strings.Join(strings.Fields(str(v)), " ")
		if q == "" || len(q) > 8192 {
			return nil, fault(400, "hosted_invalid_query")
		}
		key := strings.ToLower(q)
		if !seen[key] {
			out = append(out, q)
			seen[key] = true
		}
	}
	return out, nil
}
