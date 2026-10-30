// SPDX-License-Identifier: Apache-2.0
package policy

import (
	"encoding/json"
	"math"
	"net/url"
	"regexp"
	"strings"

	"github.com/delinoio/oss/packages/tairouter/providers"
)

var deepseek = regexp.MustCompile(`(?i)(?:^|/)deepseek-(?:v\d+(?:\.\d+)*-(?:flash|pro)|flash|pro)(?:$|[-_:])`)
var reflection = regexp.MustCompile(`(?i)(?:^|/)(?:reflection|stealth)(?:$|[-_:/.])`)
var bracketSuffix = regexp.MustCompile(`\[[^\]]*\]$`)

// TransformRequest runs after protocol translation and authenticated reasoning replay.
// It detaches JSON so retries and sibling routes cannot mutate each other's input.
func (p Resolved) TransformRequest(body map[string]any) (map[string]any, error) {
	b, e := json.Marshal(body)
	if e != nil {
		return nil, e
	}
	out, e := decode(b)
	if e != nil {
		return nil, e
	}
	switch p.Wire {
	case providers.Chat:
		e = p.chat(out)
	case providers.Responses:
		e = p.responses(out)
	case providers.Anthropic:
		p.thinking(out)
		if p.EscapeBuiltinToolNames() {
			escapeAnthropicNames(out)
		}
	}
	if flag(p.fields["modelSuffixBracketStrip"]) {
		out["model"] = bracketSuffix.ReplaceAllString(p.Model, "")
	}
	return out, e
}

// TransformChatRequest applies the Chat policy inside a specialized adapter.
func (p Resolved) TransformChatRequest(body map[string]any) (map[string]any, error) {
	p.Wire = providers.Chat
	return p.TransformRequest(body)
}
func (p Resolved) chat(b map[string]any) error {
	for _, x := range list(b["messages"]) {
		m := obj(x)
		if m == nil {
			continue
		}
		if m["role"] == "developer" && (flag(p.fields["foldDeveloperRoleToSystem"]) || (p.fields["foldDeveloperRoleToSystem"] == nil && p.Inbound != providers.InboundChat)) {
			m["role"] = "system"
		}
		if m["role"] == "tool" && flag(p.fields["annotateEmptyToolOutputs"]) && emptyText(m["content"]) {
			m["content"] = emptyOutput
		}
		if m["role"] != "assistant" {
			continue
		}
		calls := list(m["tool_calls"])
		preserve := p.in("preserveReasoningContentModels", false)
		if p.fields["preserveReasoningContentModels"] == nil {
			preserve = deepseek.MatchString(p.Model) || reflection.MatchString(p.Model)
		}
		placeholder := p.fields["requiresReasoningPlaceholderModels"] == nil || p.in("requiresReasoningPlaceholderModels", false)
		if preserve && !p.in("reasoningDetailsModels", false) && str(m["reasoning_content"]) == "" {
			if s := str(m["reasoning"]); s != "" {
				m["reasoning_content"] = s
			} else if placeholder && (len(calls) > 0 || (len(list(b["tools"])) > 0 && deepseek.MatchString(p.Model)) || reflection.MatchString(p.Model)) {
				m["reasoning_content"] = " "
			}
		}
		if p.Model == "cline-pass/deepseek-v4-flash" && len(calls) > 0 {
			m["content"] = ""
		}
	}
	if p.in("reasoningSplitModels", false) {
		b["reasoning_split"] = true
	}
	p.thinking(b)
	p.sampling(b)
	if p.in("noStructuredOutputModels", true) {
		delete(b, "response_format")
	}
	if p.in("noJsonSchemaModels", true) && obj(b["response_format"])["type"] == "json_schema" {
		b["response_format"] = map[string]any{"type": "json_object"}
	}
	if p.in("autoToolChoiceOnlyModels", false) && b["tool_choice"] != nil && b["tool_choice"] != "none" {
		b["tool_choice"] = "auto"
	}
	if len(list(b["tools"])) > 0 {
		switch p.fields["parallelToolCalls"] {
		case false:
			if p.BaseURL == "https://integrate.api.nvidia.com/v1" || flag(p.fields["pinParallelToolCallsFalse"]) {
				b["parallel_tool_calls"] = false
			} else {
				delete(b, "parallel_tool_calls")
			}
		case true:
			b["parallel_tool_calls"] = b["parallel_tool_calls"] != false
		default:
			if b["parallel_tool_calls"] != false {
				delete(b, "parallel_tool_calls")
			}
		}
	}
	if p.fields["promptCacheKey"] == false {
		delete(b, "prompt_cache_key")
	}
	if p.fields["chatServiceTier"] == false || p.fastOff || first(p.modelValue("modelSupportsServiceTier"), p.fields["supportsServiceTier"]) == false {
		delete(b, "service_tier")
	}
	if first(p.modelValue("modelSupportsVerbosity"), p.fields["supportsVerbosity"]) == false {
		delete(b, "verbosity")
	}
	u, _ := url.Parse(p.BaseURL)
	if u != nil && u.Hostname() == "api.deepseek.com" {
		for _, tool := range list(b["tools"]) {
			f := obj(obj(tool)["function"])
			if f["name"] == "Artifact" && str(f["namespace"]) == "" {
				f["parameters"] = relaxArtifact(f["parameters"])
				delete(f, "strict")
			}
		}
	}

	if u != nil && (u.Hostname() == "api.kimi.com" || u.Hostname() == "api.moonshot.ai" || u.Hostname() == "api.moonshot.cn") {
		budget := schemaBudget{nodes: 4096, expansions: 512, bytes: 1024 * 1024}
		for _, t := range list(b["tools"]) {
			f := obj(obj(t)["function"])
			if f != nil && f["parameters"] != nil {
				root := f["parameters"]
				f["parameters"] = normalizeSchema(root, root, &budget, 0, map[string]bool{})
			}
		}
	}
	return nil
}
func (p Resolved) sampling(b map[string]any) {
	for k, f := range map[string]string{"temperature": "noTemperatureModels", "top_p": "noTopPModels", "stop": "noStopModels", "presence_penalty": "noPenaltyModels", "frequency_penalty": "noPenaltyModels"} {
		if p.in(f, false) {
			delete(b, k)
		}
	}
}
func (p Resolved) thinking(b map[string]any) {
	cap := p.capabilities()
	toggle := p.in("thinkingToggleModels", false) || cap["supportsThinkingToggle"] == true
	efforts := first(p.modelValue("modelReasoningEfforts"), p.fields["reasoningEfforts"])
	_, hasEfforts := efforts.([]any)
	if p.in("noReasoningModels", false) || cap["supportsReasoning"] == false || (!toggle && hasEfforts && len(list(efforts)) == 0) {
		delete(b, "reasoning_effort")
		delete(b, "reasoning")
		delete(b, "thinking")
		return
	}
	effort := str(b["reasoning_effort"])
	if effort == "" {
		effort = str(obj(b["reasoning"])["effort"])
	}
	if effort == "" && p.Inbound != providers.InboundChat {
		effort = str(p.modelValue("modelDefaultReasoningEfforts"))
	}
	mapped := effort
	if mapped == "ultra" {
		mapped = "max"
	}
	em := obj(first(p.modelValue("modelReasoningEffortMap"), p.fields["reasoningEffortMap"]))
	if v, ok := em[mapped]; ok {
		mapped = str(v)
	}
	if mapped == "__omit__" {
		mapped = ""
		delete(b, "reasoning_effort")
		if r := obj(b["reasoning"]); r != nil {
			delete(r, "effort")
		}
	}
	enabled, explicit := obj(b["reasoning"])["enabled"].(bool)
	if toggle && cap["supportsThinkingToggle"] != false {
		kind := ""
		if explicit {
			if enabled {
				kind = "enabled"
			} else {
				kind = "disabled"
			}
		} else if effort == "none" {
			kind = "disabled"
		} else if b["thinking"] != nil {
			kind = ""
		} else if mapped == "enabled" || mapped == "disabled" || mapped == "adaptive" {
			kind = mapped
		} else if v, ok := cap["defaultThinkingEnabled"].(bool); ok {
			if v {
				kind = "enabled"
			} else {
				kind = "disabled"
			}
		}
		if kind != "" {
			b["thinking"] = map[string]any{"type": kind}
		}
		if cap["supportsReasoningEffort"] == false || mapped == "enabled" || mapped == "disabled" || mapped == "adaptive" {
			delete(b, "reasoning_effort")
			delete(b, "reasoning")
			return
		}
	}
	if cap["supportsThinkingToggle"] == false {
		delete(b, "thinking")
	}
	if cap["supportsReasoningEffort"] == false {
		delete(b, "reasoning_effort")
		delete(b, "reasoning")
		return
	}

	if p.Wire == providers.Chat && p.Inbound != providers.InboundChat && p.in("thinkingBudgetModels", false) && mapped != "" {
		budget := 32768.0
		for _, k := range []string{"max_completion_tokens", "max_tokens"} {
			if n, ok := number(b[k]); ok && n > 0 {
				budget = n
				break
			}
		}
		fraction, ok := map[string]float64{"low": 0.2, "medium": 0.5, "high": 0.75, "xhigh": 0.9, "max": 1}[mapped]
		if effort == "minimal" {
			b["thinking_budget"] = 0
		} else if ok {
			b["thinking_budget"] = math.Max(1, math.Floor(budget*fraction))
		}
		delete(b, "reasoning_effort")
		return
	}
	if p.fields["reasoningWireFormat"] == "gateway-object" && effort != "" {
		delete(b, "reasoning_effort")
		r := map[string]any{"enabled": effort != "none"}
		if explicit {
			r["enabled"] = enabled
		}
		if effort != "none" && mapped != "" {
			r["effort"] = mapped
		}
		b["reasoning"] = r
	} else if mapped != "" {
		if p.Wire == providers.Responses {
			r := obj(b["reasoning"])
			if r == nil {
				r = map[string]any{}
				b["reasoning"] = r
			}
			r["effort"] = mapped
			delete(b, "reasoning_effort")
		} else {
			b["reasoning_effort"] = mapped
		}
	}
}
func (p Resolved) responses(b map[string]any) error {
	if p.fields["supportsResponsesCustomTools"] == false {
		if e := lowerCustomTools(b); e != nil {
			return e
		}
	}
	if p.fields["supportsOpenAiWebSearchToolFields"] == false {
		strip := func(tools []any) {
			for _, x := range tools {
				m := obj(x)
				if m["type"] == "web_search" || m["type"] == "web_search_preview" {
					delete(m, "external_web_access")
					delete(m, "search_context_size")
				}
			}
		}
		strip(list(b["tools"]))
		for _, x := range list(b["input"]) {
			m := obj(x)
			if m["type"] == "additional_tools" {
				strip(list(m["tools"]))
			}
		}
	}

	p.sampling(b)
	p.thinking(b)
	if p.in("noStructuredOutputModels", true) {
		delete(obj(b["text"]), "format")
	}
	if p.in("noJsonSchemaModels", true) && obj(obj(b["text"])["format"])["type"] == "json_schema" {
		obj(b["text"])["format"] = map[string]any{"type": "json_object"}
	}

	if flag(p.fields["statelessResponses"]) {
		for _, k := range []string{"previous_response_id", "conversation", "background", "metadata", "prompt"} {
			delete(b, k)
		}
		b["store"] = false
	}
	if p.fastOff || first(p.modelValue("modelSupportsServiceTier"), p.fields["supportsServiceTier"]) == false {
		delete(b, "service_tier")
	}
	if first(p.modelValue("modelSupportsVerbosity"), p.fields["supportsVerbosity"]) == false {
		delete(obj(b["text"]), "verbosity")
	}
	if p.modelValue("modelSupportsReasoningSummaries") == false {
		delete(obj(b["reasoning"]), "summary")
	}

	if obj(b["reasoning"])["summary"] == "none" {
		delete(obj(b["reasoning"]), "summary")
	}
	delete(obj(b["reasoning"]), "enabled")
	strictPlaintext := flag(p.fields["preserveResponsesReasoningContent"]) && flag(p.fields["requiresAdjacentResponsesToolResults"])
	input, ok := b["input"].([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(input))
	for _, x := range input {
		m := obj(x)
		if m == nil {
			out = append(out, x)
			continue
		}
		if m["type"] == "reasoning" {
			if flag(p.fields["dropResponsesReasoningItems"]) {
				continue
			}
			delete(m, "status")
			if strictPlaintext {
				delete(m, "encrypted_content")
				hasText := false
				for _, part := range list(m["content"]) {
					r := obj(part)
					if r["type"] == "reasoning_text" && str(r["text"]) != "" {
						hasText = true
					}
				}
				if !hasText {
					parts := []any{}
					for _, part := range list(m["summary"]) {
						r := obj(part)
						if r["type"] == "summary_text" && str(r["text"]) != "" {
							parts = append(parts, map[string]any{"type": "reasoning_text", "text": r["text"]})
						}
					}
					if len(parts) == 0 {
						parts = append(parts, map[string]any{"type": "reasoning_text", "text": " "})
					}
					m["content"] = parts
				}
			}

			if _, ok := m["summary"]; !ok {
				m["summary"] = []any{}
			}
			if strings.HasPrefix(str(m["encrypted_content"]), "ocxr1:") {
				delete(m, "encrypted_content")
			}
			if !flag(p.fields["preserveResponsesReasoningContent"]) {
				if len(list(m["content"])) > 0 {
					m["content"] = []any{}
				}
			}
		}

		if flag(p.fields["annotateEmptyToolOutputs"]) && (m["type"] == "function_call_output" || m["type"] == "custom_tool_call_output") && emptyText(m["output"]) {
			m["output"] = emptyOutput
		}
		out = append(out, x)
	}
	if flag(p.fields["statelessResponses"]) || flag(p.fields["requiresPairedResponsesToolResults"]) {
		out = pairOutputs(out, flag(p.fields["statelessResponses"]) || b["previous_response_id"] == nil)
	}
	if strictPlaintext {
		out = backfillToolReasoning(out)
	}
	b["input"] = out
	if flag(p.fields["requiresAdjacentResponsesToolResults"]) {
		b["input"] = adjacent(out)
	}
	return nil
}
