// SPDX-License-Identifier: Apache-2.0
// Protocol mappings adapted from the sources identified in LICENSE-MIT.
package devin

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

type object = map[string]any

func obj(v any) object { m, _ := v.(map[string]any); return m }
func arr(v any) []any  { a, _ := v.([]any); return a }
func str(v any) string { s, _ := v.(string); return s }

type message struct {
	role                               string
	text                               string
	images                             [][]byte
	calls                              [][]byte
	callID                             string
	thinking, signature, signatureType string
	isError                            bool
}

func content(v any) (string, [][]byte, error) {
	if s, ok := v.(string); ok {
		return s, nil, nil
	}
	if v != nil && arr(v) == nil {
		return "", nil, badRequest()
	}
	var texts []string
	var images [][]byte
	for _, raw := range arr(v) {
		p := obj(raw)
		switch str(p["type"]) {
		case "text", "input_text", "output_text":
			texts = append(texts, str(p["text"]))
		case "image", "image_url", "input_image":
			mime, data := str(p["mimeType"]), str(p["base64Data"])
			if source := obj(p["source"]); source != nil {
				if str(source["type"]) != "base64" {
					return "", nil, inference.ErrUnsupported
				}
				mime, data = str(source["media_type"]), str(source["data"])
			}
			ref := str(p["image_url"])
			if ref == "" {
				ref = str(obj(p["image_url"])["url"])
			}
			if ref != "" {
				if strings.HasPrefix(ref, "data:") {
					header, body, ok := strings.Cut(strings.TrimPrefix(ref, "data:"), ";base64,")
					if !ok || body == "" {
						return "", nil, badRequest()
					}
					mime, data = header, body
				} else {
					texts = append(texts, "[image url: "+ref+"]")
					continue
				}
			}
			if data == "" {
				return "", nil, badRequest()
			}
			if mime == "" {
				mime = "image/png"
			}
			image := textField(textField(nil, 1, data), 2, mime)
			if caption := str(p["caption"]); caption != "" {
				image = textField(image, 3, caption)
			}
			images = append(images, image)
		case "tool_use", "tool_result", "thinking", "redacted_thinking":
			// The message normalizer processes these separately.
		default:
			return "", nil, inference.ErrUnsupported
		}
	}
	return strings.Join(texts, "\n"), images, nil
}

func toolCall(id, name, args string) []byte {
	return textField(textField(textField(nil, 1, id), 2, name), 3, args)
}

func messages(input object, wire inference.Wire) ([]message, error) {
	var out []message
	appendMessage := func(m message) {
		if m.isError {
			m.text = "ERROR: " + m.text
		}
		// Only adjacent tool results with the same ID may be combined.
		if m.role == "tool" && len(out) > 0 && out[len(out)-1].role == "tool" && out[len(out)-1].callID == m.callID {
			last := &out[len(out)-1]
			last.text += "\n\n" + m.text
			last.images = append(last.images, m.images...)
			last.isError = last.isError || m.isError
			return
		}
		out = append(out, m)
	}
	if wire == inference.Anthropic {
		text, images, err := content(input["system"])
		if err != nil || len(images) > 0 {
			return nil, badRequest()
		}
		if text != "" {
			appendMessage(message{role: "system", text: text})
		}
	}
	list := arr(input["messages"])
	if wire == inference.Responses {
		if s := str(input["instructions"]); s != "" {
			appendMessage(message{role: "system", text: s})
		}
		list = arr(input["input"])
		if s, ok := input["input"].(string); ok {
			list = []any{object{"role": "user", "content": s}}
		}
	}
	for _, raw := range list {
		m := obj(raw)
		if m == nil {
			return nil, badRequest()
		}
		if wire == inference.Anthropic && str(m["role"]) == "user" && arr(m["content"]) != nil {
			// Flush text/image segments at each result boundary. Moving all results
			// before all text would merge results that were not adjacent in input.
			var segment []any
			flush := func() error {
				if len(segment) == 0 {
					return nil
				}
				t, images, err := content(segment)
				if err != nil {
					return err
				}
				appendMessage(message{role: "user", text: t, images: images})
				segment = nil
				return nil
			}
			for _, rawPart := range arr(m["content"]) {
				p := obj(rawPart)
				if str(p["type"]) != "tool_result" {
					segment = append(segment, rawPart)
					continue
				}
				if err := flush(); err != nil {
					return nil, err
				}
				t, images, err := content(p["content"])
				if err != nil {
					return nil, err
				}
				appendMessage(message{role: "tool", callID: str(p["tool_use_id"]), text: t, images: images, isError: p["is_error"] == true})
			}
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if wire == inference.Responses {
			switch str(m["type"]) {
			case "additional_tools":
				continue
			case "tool_search_call":
				args := str(m["arguments"])
				if args == "" {
					b, _ := json.Marshal(m["arguments"])
					args = string(b)
				}
				appendMessage(message{role: "assistant", calls: [][]byte{toolCall(str(m["call_id"]), "tool_search", args)}})
				continue
			case "tool_search_output":
				payload := object{"tools": arr(m["tools"])}
				if status := str(m["status"]); status != "" {
					payload["status"] = status
				}
				b, _ := json.Marshal(payload)
				appendMessage(message{role: "tool", callID: str(m["call_id"]), text: string(b)})
				continue
			case "custom_tool_call":
				args, _ := json.Marshal(object{"input": str(m["input"])})
				appendMessage(message{role: "assistant", calls: [][]byte{toolCall(str(m["call_id"]), str(m["name"]), string(args))}})
				continue
			case "function_call":
				appendMessage(message{role: "assistant", calls: [][]byte{toolCall(str(m["call_id"]), str(m["name"]), str(m["arguments"]))}})
				continue
			case "function_call_output", "custom_tool_call_output":
				t, images, err := content(m["output"])
				if err != nil {
					return nil, err
				}
				appendMessage(message{role: "tool", callID: str(m["call_id"]), text: t, images: images})
				continue
			case "reasoning":
				// Encrypted reasoning requires the executor's owner-scoped envelope decoder.
				return nil, inference.ErrUnsupported
			case "", "message":
			default:
				return nil, inference.ErrUnsupported
			}
		}
		t, images, err := content(m["content"])
		if err != nil {
			return nil, err
		}
		item := message{role: str(m["role"]), text: t, images: images, callID: str(m["tool_call_id"]), thinking: str(m["reasoning_content"]), signature: str(m["reasoning_signature"]), signatureType: str(m["signature_type"]), isError: m["is_error"] == true}
		if item.role == "developer" {
			item.role = "system"
		}
		if item.role != "system" && item.role != "user" && item.role != "assistant" && item.role != "tool" {
			return nil, badRequest()
		}
		for _, rawCall := range arr(m["tool_calls"]) {
			call := obj(rawCall)
			fn := obj(call["function"])
			item.calls = append(item.calls, toolCall(str(call["id"]), str(fn["name"]), str(fn["arguments"])))
		}
		var results []message
		var thoughts []string
		var thinkingBlocks []object
		hasThinking := false
		for _, rawPart := range arr(m["content"]) {
			p := obj(rawPart)
			thinkingBlocks = append(thinkingBlocks, p)
			switch str(p["type"]) {
			case "tool_use":
				args, err := json.Marshal(p["input"])
				if err != nil {
					return nil, badRequest()
				}
				item.calls = append(item.calls, toolCall(str(p["id"]), str(p["name"]), string(args)))
			case "tool_result":
				text, images, err := content(p["content"])
				if err != nil {
					return nil, err
				}
				results = append(results, message{role: "tool", callID: str(p["tool_use_id"]), text: text, images: images, isError: p["is_error"] == true})
			case "thinking":
				hasThinking = true
				if text := str(p["thinking"]); text != "" {
					thoughts = append(thoughts, text)
				}
			case "redacted_thinking":
				return nil, inference.ErrUnsupported
			}
		}
		if len(thoughts) > 0 {
			item.thinking = strings.Join(thoughts, "\n")
		}
		// A single signature cannot attest joined independent thinking blocks.
		if hasThinking {
			item.signature = reasoningSignature(thinkingBlocks)
		}
		if strings.HasPrefix(item.signature, "devin-sig1:") {
			signature := DecodeSignature(item.signature)
			item.signatureType, item.signature = signature.Type, signature.Value
		}
		if len(results) > 0 {
			for _, result := range results {
				appendMessage(result)
			}
			if item.text == "" && len(item.images) == 0 {
				continue
			}
		}
		appendMessage(item)
	}
	return out, nil
}

func reasoningSignature(blocks []object) string {
	textIndex, textCount, signatureIndex, signatureCount := -1, 0, -1, 0
	for i, p := range blocks {
		if str(p["type"]) != "thinking" {
			continue
		}
		if str(p["thinking"]) != "" {
			textIndex = i
			textCount++
		} else if str(p["signature"]) != "" {
			signatureIndex = i
			signatureCount++
		}
	}
	if textCount == 1 && str(blocks[textIndex]["signature"]) != "" {
		return str(blocks[textIndex]["signature"])
	}
	if signatureCount == 1 && (textCount == 0 || textCount == 1 && signatureIndex == textIndex+1) {
		return str(blocks[signatureIndex]["signature"])
	}
	return ""
}

var suffixPattern = regexp.MustCompile(`-(?:none|minimal|low|medium|high|xhigh|max|1m|fast|priority)$`)
var swePattern = regexp.MustCompile(`^swe-2(?:-(?:medium|high|max))?$`)

// resolveModel is the pinned degraded path. Catalog-backed resolution belongs to
// authorized executor preflight; it must not trigger an unbudgeted HTTP lookup here.
func resolveModel(model, effort string) string {
	if index := strings.LastIndex(model, "/"); index >= 0 {
		model = model[index+1:]
	}
	model = strings.ReplaceAll(model, ".", "-")
	if swePattern.MatchString(model) {
		mapped := map[string]string{"none": "medium", "off": "medium", "minimal": "medium", "low": "medium", "medium": "medium", "high": "high", "xhigh": "max", "ultra": "max", "max": "max"}[strings.ToLower(effort)]
		if mapped != "" {
			return "swe-2-" + mapped
		}
	}
	if suffixPattern.MatchString(model) {
		return model
	}
	switch effort {
	case "low", "medium", "high", "xhigh", "max", "none", "1m", "max-1m", "none-1m", "fast":
	default:
		effort = "medium"
	}
	return model + "-" + effort
}

func numeric(input object, key string, fallback float64) (float64, error) {
	v, exists := input[key]
	if !exists {
		return fallback, nil
	}
	n, ok := v.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, badRequest()
	}
	return n, nil
}

func buildChat(input object, in inference.AdapterInput, meta []byte, options ...Options) ([]byte, error) {
	if in.Wire != inference.Chat && in.Wire != inference.Responses && in.Wire != inference.Anthropic {
		return nil, inference.ErrUnsupported
	}
	if input["previous_response_id"] != nil || input["conversation"] != nil {
		return nil, inference.ErrContinuation
	}
	for _, key := range []string{"audio", "modalities", "response_format", "text", "stop"} {
		if input[key] != nil {
			return nil, inference.ErrUnsupported
		}
	}
	items, err := messages(input, in.Wire)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, badRequest()
	}
	model := in.Target.Model
	if model == "" {
		model = str(input["model"])
	}
	if model == "" {
		return nil, badRequest()
	}
	effort := str(input["reasoning_effort"])
	if effort == "" {
		effort = str(obj(input["reasoning"])["effort"])
	}
	var opts Options
	if len(options) > 0 {
		opts = options[0]
	}
	resolved, err := ResolveModel(opts.Catalog, model, effort)
	if err != nil {
		return nil, err
	}
	model = resolved.UID
	caps := ResolveCaps(opts, resolved)
	if input["max_tokens"] == nil && input["max_output_tokens"] == nil && input["max_completion_tokens"] == nil && caps.Output > 0 {
		input["max_output_tokens"] = float64(caps.Output)
	}
	tools, err := buildTools(input, model)
	if err != nil {
		return nil, err
	}
	first := 0
	for first < len(items) && items[first].role == "system" {
		first++
	}
	if first == len(items) {
		first = 0
	}
	var system []string
	for _, m := range items[:first] {
		if m.text != "" {
			system = append(system, m.text)
		}
	}
	plan, err := planTools(input)
	if err != nil {
		return nil, err
	}
	if nudge := plan.nudge(); nudge != "" {
		system = append(system, nudge)
	}
	b := textField(blob(nil, 1, meta), 2, sanitizeInstructions(strings.Join(system, "\n\n")))
	var pending []string
	var collapsed []message
	for _, m := range items[first:] {
		if m.role == "system" {
			if m.text != "" {
				pending = append(pending, m.text)
			}
			continue
		}
		if len(pending) > 0 {
			prefix := "<system>\n" + strings.Join(pending, "\n\n") + "\n</system>"
			if m.role == "user" {
				m.text = prefix + "\n" + m.text
			} else {
				collapsed = append(collapsed, message{role: "user", text: prefix})
			}
			pending = nil
		}
		collapsed = append(collapsed, m)
	}
	if len(pending) > 0 {
		collapsed = append(collapsed, message{role: "user", text: "<system>\n" + strings.Join(pending, "\n\n") + "\n</system>"})
	}
	for _, m := range collapsed {
		id, err := uuid()
		if err != nil {
			return nil, err
		}
		source := uint64(1)
		if m.role == "assistant" {
			source = 2
		}
		if m.role == "tool" {
			source = 4
		}
		p := textField(number(textField(nil, 1, id), 2, source), 3, m.text)
		if source == 4 {
			if m.callID == "" {
				return nil, badRequest()
			}
			p = textField(p, 7, m.callID)
			if m.isError {
				p = number(p, 9, 1)
			}
		}
		for _, image := range m.images {
			p = blob(p, 10, image)
		}
		if source == 2 {
			for _, call := range m.calls {
				call, err = lowerHistoryCall(call, plan)
				if err != nil {
					return nil, err
				}
				p = blob(p, 6, call)
			}
			for _, f := range []struct {
				n int
				s string
			}{{11, m.thinking}, {12, m.signature}, {18, m.signatureType}} {
				if f.s != "" {
					p = textField(p, f.n, f.s)
				}
			}
		}
		b = blob(b, 3, p)
	}
	capKey := "max_tokens"
	if input["max_output_tokens"] != nil {
		capKey = "max_output_tokens"
	} else if input["max_completion_tokens"] != nil {
		capKey = "max_completion_tokens"
	}
	cap, err := numeric(input, capKey, 8192)
	if err != nil || cap <= 0 || cap > math.MaxInt32 || math.Trunc(cap) != cap {
		return nil, badRequest()
	}
	temperature, err := numeric(input, "temperature", 0.7)
	if err != nil {
		return nil, err
	}
	if temperature <= 0 {
		temperature = 0.0001
	}
	topK, err := numeric(input, "top_k", 40)
	if err != nil || topK < 0 || topK > math.MaxInt32 || math.Trunc(topK) != topK {
		return nil, badRequest()
	}
	topP, err := numeric(input, "top_p", 1)
	if err != nil {
		return nil, err
	}
	config := number(number(number(nil, 1, 1), 2, uint64(cap)), 3, 128000)
	config = double(number(double(config, 5, temperature), 7, uint64(topK)), 8, topP)
	b = blob(number(b, 7, 5), 8, config)
	for _, tool := range tools {
		b = blob(b, 10, tool)
	}
	b = blob(b, 13, number(nil, 1, 1))
	trajectory, err := uuid()
	if err != nil {
		return nil, err
	}
	cascade, err := uuid()
	if err != nil {
		return nil, err
	}
	if opts.cascadeID != "" {
		cascade = opts.cascadeID
	}
	if opts.trajectoryID != "" {
		trajectory = opts.trajectoryID
	}
	b = blob(b, 15, number(number(textField(nil, 1, trajectory), 2, 1), 3, 4))
	b = textField(b, 16, cascade)
	return textField(number(b, 20, 1), 21, model), nil
}

// Cognition rejects these instruction phrases. Restrict the rewrite to system
// instructions and tool descriptions; changing user data or arguments corrupts it.
var rewrites = []struct {
	re          *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`\bTakes a task_id parameter identifying the task\b`), "Accepts a task_id parameter identifying the task"},
	{regexp.MustCompile(`(?i)\bRuns\s+a\s+command\s+in\s+a\s+PTY,?\s+returning\s+output\s+or\s+a\s+session\s+ID\s+for\s+ongoing\s+interaction\b`), "Executes a command in a PTY, returning output or a session ID for ongoing interaction"},
	{regexp.MustCompile(`(?i)\bWrites\s+characters\s+to\s+an\s+existing\s+unified\s+exec\s+session\s+and\s+returns\s+recent\s+output\b`), "Sends characters to an existing unified exec session and returns recent output"},
	{regexp.MustCompile("(?i)\\basking\\s+the\\s+user\\s+if\\s+they\\s+want\\s+to\\s+allow\\s+the\\s+action\\s+in\\s+`?justification`?\\s+parameter\\b"), "asking the user whether to allow the action in the `justification` parameter"},
}

func sanitizeInstructions(s string) string {
	for _, rewrite := range rewrites {
		s = rewrite.re.ReplaceAllString(s, rewrite.replacement)
	}
	return s
}
func truncateUnits(s string, n int) string {
	units := utf16.Encode([]rune(s))
	if len(units) > n {
		units = units[:n]
	}
	return string(utf16.Decode(units))
}

func buildTools(input object, model string) ([][]byte, error) {
	var out [][]byte
	plan, err := planTools(input)
	if err != nil {
		return nil, err
	}
	for _, tool := range plan.tools {
		name := tool.name
		desc := sanitizeInstructions(tool.description)
		if len(utf16.Encode([]rune(desc))) > 6998 {
			desc = truncateUnits(desc, 6974) + "\n…(truncated for cloud)"
		}
		params := tool.parameters
		if strings.HasPrefix(strings.ToLower(model), "gemini-") || strings.HasPrefix(strings.ToUpper(model), "MODEL_GOOGLE_GEMINI_") {
			var err error
			params, err = rewriteSchema(params, 0)
			if err != nil {
				return nil, err
			}
		}
		encoded, err := json.Marshal(params)
		if err != nil {
			return nil, badRequest()
		}
		out = append(out, textField(textField(textField(nil, 1, name), 2, desc), 3, string(encoded)))
	}
	return out, nil
}

func rewriteSchema(v any, depth int) (any, error) {
	if depth > 64 {
		return nil, badRequest()
	}
	if a, ok := v.([]any); ok {
		for i, x := range a {
			y, err := rewriteSchema(x, depth+1)
			if err != nil {
				return nil, err
			}
			a[i] = y
		}
		return a, nil
	}
	m := obj(v)
	if m == nil {
		return v, nil
	}
	for key, x := range m {
		switch key {
		case "enum", "const", "default", "examples", "example":
			continue
		case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies":
			for name, schema := range obj(x) {
				if _, ok := schema.([]any); ok {
					continue
				}
				y, err := rewriteSchema(schema, depth+1)
				if err != nil {
					return nil, err
				}
				obj(x)[name] = y
			}
		default:
			y, err := rewriteSchema(x, depth+1)
			if err != nil {
				return nil, err
			}
			m[key] = y
		}
	}
	types, ok := m["type"].([]any)
	if !ok {
		return m, nil
	}
	delete(m, "type")
	if len(types) == 0 {
		return m, nil
	}
	var branches []any
	for _, typ := range types {
		if typ != "null" {
			branches = append(branches, object{"type": typ})
		}
	}
	allowsNull := len(branches) < len(types)
	if values, ok := m["enum"].([]any); ok {
		found := false
		for _, value := range values {
			if value == nil {
				found = true
			}
		}
		allowsNull = allowsNull && found
	}
	if value, exists := m["const"]; exists && value != nil {
		allowsNull = false
	}
	if allowsNull || len(branches) == 0 {
		branches = append(branches, object{"type": "null"})
	}
	constraint := object{"anyOf": branches}
	if len(branches) == 1 {
		constraint = obj(branches[0])
	}
	if _, ok := m["anyOf"].([]any); ok {
		m["allOf"] = append(arr(m["allOf"]), constraint)
	} else {
		for k, x := range constraint {
			m[k] = x
		}
	}
	return m, nil
}
