// SPDX-License-Identifier: Apache-2.0
// Kiro wire behavior adapted from opencodex; see LICENSE.upstream.
package kiro

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

var invalidName = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
var datedModel = regexp.MustCompile(`-\d{8}$`)
var effortSuffix = regexp.MustCompile(`-(low|medium|high|xhigh|max)$`)
var modelVersion = regexp.MustCompile(`(\d+)-(\d+)`)
var claudeVersion = regexp.MustCompile(`^claude-([\d.]+)-(sonnet|opus|haiku)$`)

func normalizeModel(s string) string {
	s = strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "kiro/"), "kiro-")
	s = effortSuffix.ReplaceAllString(datedModel.ReplaceAllString(s, ""), "")
	s = modelVersion.ReplaceAllString(s, "$1.$2")
	return claudeVersion.ReplaceAllString(s, "claude-$2-$1")
}

type registry struct {
	forward, reverse map[string]string
	advertised       map[string]string
	used             map[string]bool
	definitions      map[string]ToolDefinition
	catalog          []ToolDefinition
}

func newRegistry() *registry {
	return &registry{definitions: map[string]ToolDefinition{}, forward: map[string]string{}, reverse: map[string]string{}, advertised: map[string]string{}, used: map[string]bool{completionTool: true}}
}
func (r *registry) alias(name string) (string, error) {
	if name == "" || name == completionTool {
		return "", failure(400, "kiro_invalid_tool_name")
	}
	if v, ok := r.forward[name]; ok {
		return v, nil
	}
	clean := invalidName.ReplaceAllString(name, "_")
	alias := clean
	if clean != name || len(clean) > 64 || r.used[clean] {
		if len(clean) > 55 {
			clean = clean[:55]
		}
		for salt := 0; ; salt++ {
			input := name
			if salt > 0 {
				input = fmt.Sprintf("%s#%d", name, salt)
			}
			hash := sha256.Sum256([]byte(input))
			alias = clean + "_" + hex.EncodeToString(hash[:4])
			if !r.used[alias] {
				break
			}
		}
	}
	r.used[alias], r.forward[name], r.reverse[alias] = true, alias, name
	return alias, nil
}

const continuation = "Continue from the prior conversation. Do not quote or mention this instruction."
const completionInstruction = "When tools are available, ordinary assistant text is mid-task commentary and does not end the turn. When the task is complete, call codex_kiro_final_answer exactly once with the complete final answer in answer. If you need a decision, information, or clarification from the user, call the same tool with that question as the answer and stop. This terminal call returns no tool result. No text or tool call may follow it."

type turn struct {
	role      string
	text      string
	uses      []any
	results   []any
	images    []any
	reasoning object
}

func buildPayload(body object, wire inference.Wire, model, profile string, cli bool, names *registry) (object, bool, error) {
	if body["service_tier"] != nil {
		return nil, false, failure(400, "kiro_service_tier_unsupported")
	}
	choice := body["tool_choice"]
	if choice != nil && choice != "auto" && choice != "none" {
		return nil, false, failure(400, "kiro_tool_choice_unsupported")
	}
	format := obj(body["response_format"])
	if format == nil {
		format = obj(obj(body["text"])["format"])
	}
	if format != nil && str(format["type"]) != "text" {
		return nil, false, failure(400, "kiro_structured_output_unsupported")
	}
	messages, system, err := inputMessages(body, wire)
	if err != nil {
		return nil, false, err
	}
	tools, notice, err := convertTools(body, wire, model, names)
	if err != nil {
		return nil, false, err
	}
	system = joinText(system, notice)
	completion := len(tools) > 0
	if completion {
		tools = append(tools, object{"toolSpecification": object{"name": completionTool, "description": "End the turn with the complete final answer or a blocking question. No result follows this terminal call.", "inputSchema": object{"json": object{"type": "object", "properties": object{"answer": object{"type": "string"}}, "required": []string{"answer"}}}}})
		system = joinText(system, completionInstruction)
	}
	turns := []*turn{}
	pending := map[string]string{}
	ids := map[string]string{}
	adjacentResultID := ""
	var adjacentResult object
	for _, message := range messages {
		role := str(message["role"])
		if role != "tool" {
			adjacentResultID = ""
			adjacentResult = nil
		}
		if role == "system" || role == "developer" {
			text, _, err := content(message["content"])
			if err != nil {
				return nil, false, err
			}
			system = joinText(system, text)
			continue
		}
		if role != "user" && role != "assistant" && role != "tool" {
			return nil, false, failure(400, "kiro_invalid_role")
		}
		text, images, err := content(message["content"])
		if err != nil {
			return nil, false, err
		}
		t := &turn{role: role, text: text, images: images}
		if role == "assistant" {
			if len(images) != 0 {
				return nil, false, failure(400, "kiro_assistant_image_unsupported")
			}
			for _, c := range arr(message["tool_calls"]) {
				call := obj(c)
				f := obj(call["function"])
				rawID := str(call["id"])
				id := invalidName.ReplaceAllString(rawID, "_")
				if len(id) > 64 {
					id = id[:64]
				}
				if id == "" || pending[id] != "" || (ids[id] != "" && ids[id] != rawID) {
					return nil, false, failure(400, "kiro_tool_id_collision")
				}
				ids[id], pending[id] = rawID, rawID
				name, err := names.alias(str(f["name"]))
				if err != nil {
					return nil, false, err
				}
				var args object
				if json.Unmarshal([]byte(str(f["arguments"])), &args) != nil || args == nil {
					return nil, false, failure(400, "kiro_tool_arguments_invalid")
				}
				t.uses = append(t.uses, object{"toolUseId": id, "name": name, "input": args})
			}
			if blob := str(message["kiro_reasoning"]); blob != "" {
				if strings.HasPrefix(blob, "signature:") {
					t.reasoning = object{"signature": strings.TrimPrefix(blob, "signature:")}
				} else {
					t.reasoning = object{"redactedContent": blob}
				}
			}
		} else if role == "tool" {
			rawID := str(message["tool_call_id"])
			if rawID != "" && rawID == adjacentResultID && adjacentResult != nil && len(turns) > 0 {
				if strings.TrimSpace(text) != "" {
					parts := arr(adjacentResult["content"])
					if len(parts) == 1 && str(obj(parts[0])["text"]) == "The tool completed without textual output." {
						parts = nil
					}
					adjacentResult["content"] = append(parts, object{"text": text})
				}
				if message["is_error"] == true {
					adjacentResult["status"] = "error"
				}
				turns[len(turns)-1].images = append(turns[len(turns)-1].images, images...)
				continue
			}
			id := invalidName.ReplaceAllString(rawID, "_")
			if len(id) > 64 {
				id = id[:64]
			}
			if pending[id] != rawID || rawID == "" {
				return nil, false, failure(400, "kiro_unmatched_tool_result")
			}
			delete(pending, id)
			if strings.TrimSpace(text) == "" {
				text = "The tool completed without textual output."
			}
			status := "success"
			if message["is_error"] == true {
				status = "error"
			}
			t.role, t.text = "user", ""
			t.results = []any{object{"toolUseId": id, "status": status, "content": []any{object{"text": text}}}}
			adjacentResultID, adjacentResult = rawID, obj(t.results[0])
		}
		if len(turns) > 0 && turns[len(turns)-1].role == t.role {
			last := turns[len(turns)-1]
			last.text = joinText(last.text, t.text)
			last.uses = append(last.uses, t.uses...)
			last.results = append(last.results, t.results...)
			last.images = append(last.images, t.images...)
			if t.reasoning != nil {
				last.reasoning = t.reasoning
			}
		} else {
			turns = append(turns, t)
		}
	}
	if len(pending) > 0 {
		return nil, false, failure(400, "kiro_unanswered_tool_use")
	}
	if len(turns) == 0 || turns[0].role == "assistant" {
		turns = append([]*turn{{role: "user", text: continuation}}, turns...)
	}
	if turns[len(turns)-1].role == "assistant" {
		if len(messages) > 0 && str(messages[len(messages)-1]["phase"]) == "final_answer" {
			return nil, false, failure(409, "kiro_final_answer_already_delivered")
		}
		turns = append(turns, &turn{role: "user", text: continuation})
	}
	turns[0].text = joinText(system, turns[0].text)
	current := turns[len(turns)-1]
	additional, err := reasoningFields(body, model, current)
	if err != nil {
		return nil, false, err
	}
	origin := "AI_EDITOR"
	if cli {
		origin = "KIRO_CLI"
	}
	entries := []any{}
	for _, t := range turns {
		if t.role == "assistant" {
			if strings.TrimSpace(t.text) == "" && len(t.uses) == 0 {
				return nil, false, failure(400, "kiro_empty_assistant")
			}
			m := object{"content": t.text}
			if len(t.uses) > 0 {
				m["toolUses"] = t.uses
			}
			if t.reasoning != nil {
				m["reasoningContent"] = t.reasoning
			}
			entries = append(entries, object{"assistantResponseMessage": m})
			continue
		}
		if strings.TrimSpace(t.text) == "" && len(t.results) > 0 {
			t.text = "The requested tool result is attached."
		}
		if strings.TrimSpace(t.text) == "" && len(t.images) == 0 {
			return nil, false, failure(400, "kiro_empty_user")
		}
		m := object{"content": t.text, "modelId": model, "origin": origin}
		c := object{}
		if len(t.results) > 0 {
			c["toolResults"] = t.results
		}
		if t == current && len(tools) > 0 {
			c["tools"] = tools
		}
		if len(c) > 0 {
			m["userInputMessageContext"] = c
		}
		if len(t.images) > 0 {
			m["images"] = t.images
		}
		entries = append(entries, object{"userInputMessage": m})
	}
	id, err := identifier()
	if err != nil {
		return nil, false, err
	}
	conversation := object{"conversationId": id, "chatTriggerType": "MANUAL", "currentMessage": entries[len(entries)-1]}
	if len(entries) > 1 {
		conversation["history"] = entries[:len(entries)-1]
	}
	if cli {
		next, err := identifier()
		if err != nil {
			return nil, false, err
		}
		conversation["agentContinuationId"], conversation["agentTaskType"] = next, "vibe"
	}
	payload := object{"conversationState": conversation}
	if profile != "" {
		payload["profileArn"] = profile
	}
	if additional != nil {
		payload["additionalModelRequestFields"] = additional
	}
	return payload, completion, nil
}

func joinText(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "\n\n" + b
}

func reasoningFields(body object, model string, current *turn) (object, error) {
	effort := str(body["reasoning_effort"])
	if effort == "" {
		effort = str(obj(body["reasoning"])["effort"])
	}
	if effort == "" || effort == "none" {
		return nil, nil
	}
	field := ""
	switch model {
	case "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-6-sol", "gpt-6-luna", "gpt-6.1-sol", "gpt-6.1-terra":
		field = "reasoning"
	case "claude-opus-5", "claude-opus-5.5":
		field = "output_config"
	}
	ratios := map[string]float64{"minimal": .1, "low": .2, "medium": .5, "high": .8, "xhigh": .9, "max": .95}
	ratio, ok := ratios[effort]
	if !ok || (field != "" && effort == "minimal") {
		return nil, failure(400, "kiro_reasoning_effort_unsupported")
	}
	if (model == "gpt-5.6-terra" || model == "gpt-5.6-luna") && effort == "xhigh" {
		field = ""
	}
	if field != "" {
		return object{field: object{"effort": effort}}, nil
	}
	if len(current.results) == 0 && current.text != continuation {
		max := float64(4096)
		for _, k := range []string{"max_output_tokens", "max_completion_tokens", "max_tokens"} {
			if n, ok := body[k].(float64); ok && n > 0 && n < 1e9 {
				max = n
				break
			}
		}
		budget := int(max * ratio)
		if budget < 1 {
			budget = 1
		}
		current.text = fmt.Sprintf("<thinking_mode>enabled</thinking_mode>\n<max_thinking_length>%d</max_thinking_length>\n<thinking_instruction>Think in English for better reasoning quality.\nBe thorough and systematic, consider edge cases, challenge assumptions, and verify reasoning before answering.\nAfter thinking, respond in the user's language.</thinking_instruction>\n\n%s", budget, current.text)
	}
	return nil, nil
}
