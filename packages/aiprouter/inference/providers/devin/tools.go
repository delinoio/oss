// SPDX-License-Identifier: Apache-2.0
// Adapted protocol policy: see LICENSE-MIT.
package devin

import (
	"encoding/json"
	"strings"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

type ToolKind string

const (
	FunctionTool ToolKind = "function"
	CustomTool   ToolKind = "custom"
	SearchTool   ToolKind = "tool_search"
)

type toolDefinition struct {
	name, namespace, description string
	parameters                   any
	kind                         ToolKind
}

func (t toolDefinition) canonical() string {
	if t.namespace != "" {
		return t.namespace + "__" + t.name
	}
	return t.name
}

// ResponseContext contains no credentials. Keep it request-scoped: identical bare
// names in two requests must never share an alias table or custom-tool policy.
type ResponseContext struct {
	Inbound      inference.Wire
	Model        string
	RequiredTool bool
	tools        []toolDefinition
	aliases      map[string]string
}

func (r *ResponseContext) RestoreToolName(name string) (string, error) {
	if r == nil {
		return name, nil
	}
	canonical, exists := r.aliases[name]
	if exists && canonical == "" {
		return "", &inference.Error{Status: 502, Code: "devin_ambiguous_tool_name"}
	}
	if exists {
		return canonical, nil
	}
	return name, nil
}

// ToolProjection is the completed client call. For CustomTool, Input is the raw
// freeform string and Name is the Responses bare name; Namespace retains identity.
type ToolProjection struct {
	Kind                              ToolKind
	Name, Namespace, Arguments, Input string
}

func (r *ResponseContext) ProjectToolCall(name, arguments string) (ToolProjection, error) {
	if r == nil {
		return ToolProjection{}, &inference.Error{Status: 502, Code: "devin_missing_response_context"}
	}
	canonical, err := r.RestoreToolName(name)
	if err != nil {
		return ToolProjection{}, err
	}
	for _, t := range r.tools {
		if t.canonical() == canonical {
			p := ToolProjection{Kind: t.kind, Name: t.name, Namespace: t.namespace, Arguments: arguments}
			if t.kind == CustomTool {
				var args object
				if len(arguments) > maxFrame || json.Unmarshal([]byte(arguments), &args) != nil {
					return ToolProjection{}, errWire
				}
				var ok bool
				p.Input, ok = args["input"].(string)
				if !ok {
					return ToolProjection{}, errWire
				}
			}
			return p, nil
		}
	}
	return ToolProjection{}, &inference.Error{Status: 502, Code: "devin_undeclared_tool"}
}

func planTools(input object) (*ResponseContext, error) {
	r := &ResponseContext{aliases: map[string]string{}}
	var all []toolDefinition
	var add func(object, string, int) error
	add = func(t object, namespace string, depth int) error {
		if depth > 64 || len(all) >= 4096 {
			return errLimit
		}
		if str(t["type"]) == "namespace" {
			ns := str(t["name"])
			if ns == "functions" {
				ns = ""
			}
			for _, child := range arr(t["tools"]) {
				if err := add(obj(child), ns, depth+1); err != nil {
					return err
				}
			}
			return nil
		}
		typ := str(t["type"])
		if typ == "web_search" || typ == "web_search_preview" || typ == "image_generation" || typ == "image_gen" {
			return &inference.Error{Status: 501, Code: "devin_hosted_tool_requires_executor"}
		}
		if fn := obj(t["function"]); fn != nil {
			t = fn
		}
		name := str(t["name"])
		if typ == "tool_search" {
			name = "tool_search"
		}
		if name == "" {
			return badRequest()
		}
		def := toolDefinition{name: name, namespace: namespace, description: str(t["description"]), kind: FunctionTool}
		def.parameters = t["parameters"]
		if def.parameters == nil {
			def.parameters = t["input_schema"]
		}
		if typ == "tool_search" {
			def.kind = SearchTool
			if _, exists := t["description"]; !exists {
				def.description = "Search for additional tools to load for the next turn."
			}
			if obj(def.parameters) == nil {
				def.parameters = object{"type": "object", "properties": object{"query": object{"type": "string", "description": "Search query for tools to load."}, "limit": object{"type": "number", "description": "Maximum number of tools to return."}}, "required": []any{"query"}}
			}
		}
		if typ == "custom" {
			def.kind = CustomTool
			desc := "Raw freeform input for this tool."
			if name == "apply_patch" {
				desc = "Raw tool input. For apply_patch, begin exactly with `*** Begin Patch` (no trailing `***`), then use its standard patch envelope."
			}
			def.parameters = object{"type": "object", "properties": object{"input": object{"type": "string", "description": desc}}, "required": []any{"input"}}
		} else {
			params := obj(def.parameters)
			if params == nil {
				params = object{}
			}
			params["type"] = "object"
			def.parameters = params
		}
		for _, old := range all {
			if old.canonical() == def.canonical() {
				if old.name == def.name && old.namespace == def.namespace && old.kind == def.kind {
					return nil
				}
				return &inference.Error{Status: 400, Code: "devin_ambiguous_tool_catalog"}
			}
		}
		all = append(all, def)
		return nil
	}
	for _, raw := range arr(input["tools"]) {
		if err := add(obj(raw), "", 0); err != nil {
			return nil, err
		}
	}
	for _, raw := range arr(input["input"]) {
		item := obj(raw)
		if str(item["type"]) != "additional_tools" {
			continue
		}
		for _, tool := range arr(item["tools"]) {
			if err := add(obj(tool), "", 0); err != nil {
				return nil, err
			}
		}
	}
	choice := input["tool_choice"]
	c := obj(choice)
	mode := str(choice)
	if mode == "" {
		mode = str(c["type"])
	}
	var selectors []string
	switch mode {
	case "none":
		all = nil
	case "required", "any":
		r.RequiredTool = true
	case "function", "custom", "tool", "tool_search":
		name := str(c["name"])
		if mode == "tool_search" {
			name = "tool_search"
		}
		if name == "" {
			name = str(obj(c["function"])["name"])
		}
		selectors = []string{name}
		r.RequiredTool = true
	case "allowed_tools":
		for _, v := range arr(c["tools"]) {
			name := str(obj(v)["name"])
			if str(obj(v)["type"]) == "tool_search" {
				name = "tool_search"
			}
			if name == "" {
				name = str(obj(obj(v)["function"])["name"])
			}
			selectors = append(selectors, name)
		}
		r.RequiredTool = c["mode"] == "required"
		if len(selectors) == 0 {
			all = nil
		}
	case "", "auto":
	default:
		return nil, badRequest()
	}
	if len(selectors) > 0 {
		selected := map[string]bool{}
		for _, name := range selectors {
			var matches []toolDefinition
			for _, t := range all {
				if name == t.name || name == t.canonical() {
					matches = append(matches, t)
				}
			}
			if len(matches) != 1 {
				return nil, &inference.Error{Status: 400, Code: "devin_invalid_tool_selector"}
			}
			selected[matches[0].canonical()] = true
		}
		for _, t := range all {
			if selected[t.canonical()] {
				r.tools = append(r.tools, t)
			}
		}
	} else {
		r.tools = all
	}
	for _, t := range r.tools {
		for _, alias := range []string{t.name, t.canonical()} {
			old, ok := r.aliases[alias]
			if !ok {
				r.aliases[alias] = t.canonical()
			} else if old != t.canonical() {
				r.aliases[alias] = ""
			}
		}
	}
	return r, nil
}

func (r *ResponseContext) nudge() string {
	if len(r.tools) == 0 {
		return ""
	}
	names := make([]string, 0, len(r.tools))
	seenNames := map[string]bool{}
	for _, t := range r.tools {
		if !seenNames[t.name] {
			names = append(names, "`"+t.name+"`")
			seenNames[t.name] = true
		}
	}
	codeMode, shellBridge := false, false
	available := map[string]bool{}
	for _, t := range r.tools {
		available[t.name] = true
		if t.namespace == "" && t.name == "exec" && t.kind == CustomTool {
			codeMode = true
		}
		if t.namespace == "" && (t.name == "exec_command" || t.name == "shell_command") {
			shellBridge = true
		}
	}
	nested := "If a listed tool exposes nested helpers such as a tools.* API, call the listed parent tool and use those helpers only inside that tool's input."
	if codeMode && !shellBridge {
		nested = "`exec` is Codex code mode: its body is JavaScript evaluated in a V8 isolate. Nested helpers are called INSIDE that body as `await tools.<name>(...)`, for example `await tools.exec_command({cmd: \"ls\"})` or `await tools.codex_app__list_threads({})`. Absence from the top-level catalog or from `exec`'s description is not absence: deferred helpers stay callable on `tools.<name>`. Discover them from the isolate global `ALL_TOOLS`, not `tools.ALL_TOOLS`. Do not skip an available nested helper because it is omitted from the listed top-level names. Nothing in the isolate is echoed automatically: a bare trailing `await tools.<name>(...)` or final expression value is DISCARDED, and the cell reports empty output. Pass anything you need to read to `text(...)` (or `notify(...)`) in the same cell — for example `text(JSON.stringify(await tools.exec_command({cmd: 'ls'})))` — and treat an empty result as your own missing `text(...)` call rather than a failed command or lost context. Nested `tools.apply_patch(input)` is host-executed: the string must begin exactly with `*** Begin Patch` and end with `*** End Patch`, each marker line being three asterisks, one space, the two words, then end of line with no further asterisks. OpenCodex does not rewrite JavaScript inside exec, so extra asterisks on a marker line are rejected by Codex before the file is touched. Host contract for the nested helpers: `tools.apply_patch(patch)` takes exactly one string, never an object such as `{input: ...}`; the patch text opens with the bare marker line `*** Begin Patch` and closes with the bare marker line `*** End Patch`, written without a code fence, prose, or extra asterisks on those lines (blank lines or indentation around the markers are tolerated; a decorated or missing marker is rejected). The isolate has no `import`, `require`, or module loader; use the globals the exec tool description lists (for example `tools`, `text`, `notify`, `store`/`load`, `ALL_TOOLS`). For a command that may outlive `yield_time_ms`, let `tools.exec_command` return a `session_id` and poll it on later calls with `tools.write_stdin({session_id, chars: \"\"})` instead of blocking a shell in a sleep loop."
	}
	var unavailable []string
	for _, name := range []string{"Read", "Grep", "Glob", "Bash", "LS"} {
		if !available[name] {
			unavailable = append(unavailable, "`"+name+"`")
		}
	}
	neighbor := ""
	if len(unavailable) > 0 {
		neighbor = " Do not use neighboring-agent tool names " + strings.Join(unavailable, ", ") + " unless this turn's catalog lists those exact names."
	}
	return "Tool contract: use the current tool catalog as ground truth. Valid tool names for this turn are exactly " + strings.Join(names, ", ") + ". These listed names are the complete top-level tool-call surface for this turn. Call only listed names with their listed argument keys; do not invent, translate, or rename tools. Names mentioned only in instructions, tool descriptions, argument descriptions, or nested helper APIs are not additional top-level tools. " + nested + neighbor + " If you need shell, file search, file read, edit, or discovery behavior, choose the listed tool that provides that capability. Count a tool call only after its tool result returns; batch independent read-only calls when the runtime supports it."
}

func lowerHistoryCall(b []byte, plan *ResponseContext) ([]byte, error) {
	var id, name, args string
	if err := walk(b, func(f field) error {
		if f.wire == 2 {
			switch f.num {
			case 1:
				id = string(f.b)
			case 2:
				name = string(f.b)
			case 3:
				args = string(f.b)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	for _, t := range plan.tools {
		if name == t.canonical() {
			name = t.name
			break
		}
	}
	return toolCall(id, name, args), nil
}
