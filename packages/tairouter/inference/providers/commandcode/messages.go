// SPDX-License-Identifier: Apache-2.0
// Tool adjacency behavior adapted from opencodex; see LICENSE.upstream.
package commandcode

import (
	"encoding/json"
	"mime"
	"net/url"
	"path"
	"strings"
)

func parts(v any) []any {
	if s, ok := v.(string); ok {
		return []any{object{"type": "text", "text": s}}
	}
	var out []any
	for _, raw := range arr(v) {
		p := obj(raw)
		switch str(p["type"]) {
		case "text", "input_text", "output_text":
			out = append(out, object{"type": "text", "text": p["text"]})
		case "image_url", "input_image", "image":
			image := str(p["image_url"])
			if image == "" {
				image = str(obj(p["image_url"])["url"])
			}
			if image == "" {
				image = str(p["url"])
			}
			q := object{"type": "image", "image": image}
			if strings.HasPrefix(image, "data:") {
				if i := strings.IndexAny(image[5:], ";,"); i >= 0 {
					q["mediaType"] = image[5 : 5+i]
				}
			} else if u, e := url.Parse(image); e == nil {
				if mt := mime.TypeByExtension(path.Ext(u.Path)); strings.HasPrefix(mt, "image/") {
					q["mediaType"] = mt
				}
			}
			out = append(out, q)
		case "document":
			out = append(out, object{"type": "text", "text": p["text"]})
		default:
			out = append(out, object{"type": "text", "text": "[video]"})
		}
	}
	return out
}
func textParts(v any) (string, []any) {
	var b strings.Builder
	var images []any
	for _, raw := range parts(v) {
		p := obj(raw)
		if p["type"] == "image" {
			b.WriteString("[image]")
			images = append(images, p)
		} else {
			b.WriteString(str(p["text"]))
		}
	}
	return b.String(), images
}
func wireMessages(messages []any) ([]any, string, error) {
	out := []any{}
	system := []string{}
	type call struct{ id, name string }
	pending := []call{}
	carriers := []any{}
	result := func(id, name, kind, text string) {
		out = append(out, object{"role": "tool", "content": []any{object{"type": "tool-result", "toolCallId": id, "toolName": name, "output": object{"type": kind, "value": text}}}})
	}
	closeCalls := func() {
		for _, c := range pending {
			result(c.id, c.name, "error-text", "[ocx] no tool result was recorded for this tool call; execution status unknown.")
		}
		pending = nil
		out = append(out, carriers...)
		carriers = nil
	}
	for _, raw := range messages {
		m := obj(raw)
		role := str(m["role"])
		if role == "tool" {
			id := str(m["tool_call_id"])
			index := -1
			for i, c := range pending {
				if c.id == id {
					index = i
					break
				}
			}
			text, images := textParts(m["content"])
			if index < 0 {
				closeCalls()
				content := []any{object{"type": "text", "text": "[tool result without adjacent tool call: " + id + "]\n" + text}}
				content = append(content, images...)
				out = append(out, object{"role": "user", "content": content})
				continue
			}
			c := pending[index]
			pending = append(pending[:index], pending[index+1:]...)
			kind := "text"
			if m["is_error"] == true {
				kind = "error-text"
			}
			result(id, c.name, kind, text)
			if len(images) > 0 {
				carriers = append(carriers, object{"role": "user", "content": images})
			}
			continue
		}
		closeCalls()
		if role == "system" || role == "developer" {
			s, _ := textParts(m["content"])
			system = append(system, s)
			continue
		}
		content := parts(m["content"])
		if role == "assistant" {
			if thinking := str(m["reasoning_content"]); thinking != "" {
				content = append([]any{object{"type": "reasoning", "text": thinking}}, content...)
			}
			for _, raw := range arr(m["tool_calls"]) {
				c := obj(raw)
				f := obj(c["function"])
				id, name := str(c["id"]), str(f["name"])
				if id == "" || name == "" {
					return nil, "", fail(400, "invalid_tool_call")
				}
				var input any = object{}
				if s := str(f["arguments"]); s != "" {
					decoder := json.NewDecoder(strings.NewReader(s))
					decoder.UseNumber()
					if !json.Valid([]byte(s)) || decoder.Decode(&input) != nil {
						return nil, "", fail(400, "invalid_tool_arguments")
					}
				}
				content = append(content, object{"type": "tool-call", "toolCallId": id, "toolName": name, "input": input})
				pending = append(pending, call{id, name})
			}
		} else {
			role = "user"
		}
		out = append(out, object{"role": role, "content": content})
	}
	closeCalls()
	return out, strings.Join(system, "\n\n"), nil
}
func wireTools(m object) ([]any, string) {
	out := []any{}
	choice := m["tool_choice"]
	if choice == "none" {
		return out, ""
	}
	selected := str(obj(obj(choice)["function"])["name"])
	allowed := map[string]bool{}
	choiceObject := obj(choice)
	restricted := choiceObject["type"] == "allowed_tools"
	if restricted {
		for _, raw := range arr(choiceObject["tools"]) {
			t := obj(raw)
			name := str(obj(t["function"])["name"])
			if name == "" {
				name = str(t["name"])
			}
			allowed[name] = true
		}
	}
	instruction := ""
	if choice == "required" || choiceObject["mode"] == "required" {
		instruction = "Tool choice is required for this turn. Make at least one call from the advertised tool catalog before answering."
	}
	if selected != "" {
		instruction = "Tool choice is required for this turn. Call the advertised tool named " + selected + " before answering."
	}
	for _, raw := range arr(m["tools"]) {
		t := obj(raw)
		f := obj(t["function"])
		name := str(f["name"])
		if name == "" || (selected != "" && selected != name) || (restricted && !allowed[name]) {
			continue
		}
		out = append(out, object{"name": name, "description": f["description"], "input_schema": f["parameters"]})
	}
	return out, instruction
}

func toolNudge(tools []any) string {
	if len(tools) == 0 {
		return ""
	}
	names := []string{}
	for _, raw := range tools {
		names = append(names, "`"+str(obj(raw)["name"])+"`")
	}
	return "Tool contract: use the current tool catalog as ground truth. Valid tool names for this turn are exactly " + strings.Join(names, ", ") + ". These listed names are the complete top-level tool-call surface for this turn. Call only listed names with their listed argument keys; do not invent, translate, or rename tools. Names mentioned only in instructions, tool descriptions, argument descriptions, or nested helper APIs are not additional top-level tools. If a listed tool exposes nested helpers such as a tools.* API, call the listed parent tool and use those helpers only inside that tool's input. Count a tool call only after its tool result returns; batch independent read-only calls when the runtime supports it."
}
