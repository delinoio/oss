// SPDX-License-Identifier: Apache-2.0
package policy

import (
	"encoding/json"
	"strconv"
	"strings"
)

type ReasoningInterleaving string

const (
	InterleavingNone             ReasoningInterleaving = "none"
	InterleavingReasoningContent ReasoningInterleaving = "reasoning_content"
)

// ModelPolicy is the resolved admission and model-view projection. Nil means
// unclassified; false and an explicit empty ladder remain observable.
type ModelPolicy struct {
	ContextWindow, MaxInputTokens, MaxOutputTokens                                             *int64
	InputModalities, ReasoningEfforts                                                          []string
	DefaultReasoningEffort                                                                     string
	SupportsReasoning, SupportsThinkingToggle, SupportsReasoningEffort, DefaultThinkingEnabled *bool
	SupportsReasoningSummaries, SupportsVerbosity, SupportsServiceTier                         *bool
	DirectReasoningEffort                                                                      bool
	ResponsesUpstreamStreaming                                                                 *bool
	ResponsesTerminalRepair                                                                    json.RawMessage
	SupportsTools                                                                              *bool
	ReasoningInterleaving                                                                      *ReasoningInterleaving
}

func boolPtr(v any) *bool {
	b, ok := v.(bool)
	if !ok {
		return nil
	}
	return &b
}
func intPtr(v any) *int64 {
	if n, ok := number(v); ok && n > 0 && n == float64(int64(n)) {
		i := int64(n)
		return &i
	}
	return nil
}
func stringList(v any) []string {
	a, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(a))
	for _, x := range a {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
func first(a, b any) any {
	if a != nil {
		return a
	}
	return b
}

// ModelPolicy returns detached values. Observed limits must only narrow these caps.
func (p Resolved) ModelPolicy() ModelPolicy {
	context := p.modelValue("modelContextWindows")
	if context == nil && p.Wire == "anthropic" && strings.HasPrefix(strings.ToLower(p.Model), "claude-") {
		model := p.Model
		for {
			cut := strings.LastIndexByte(model, '-')
			if cut <= 0 {
				break
			}
			if _, e := strconv.ParseUint(model[cut+1:], 10, 64); e != nil {
				break
			}
			model = model[:cut]
			q := p
			q.Model = model
			if context = q.modelValue("modelContextWindows"); context != nil {
				break
			}
		}
	}
	m := ModelPolicy{ContextWindow: intPtr(first(context, p.fields["contextWindow"])), MaxInputTokens: intPtr(p.modelValue("modelMaxInputTokens")), MaxOutputTokens: intPtr(first(p.modelValue("modelMaxOutputTokens"), p.fields["defaultMaxOutputTokens"])), InputModalities: stringList(p.modelValue("modelInputModalities")), ReasoningEfforts: stringList(first(p.modelValue("modelReasoningEfforts"), p.fields["reasoningEfforts"])), DefaultReasoningEffort: str(p.modelValue("modelDefaultReasoningEfforts")), SupportsReasoningSummaries: boolPtr(p.modelValue("modelSupportsReasoningSummaries")), SupportsVerbosity: boolPtr(first(p.modelValue("modelSupportsVerbosity"), p.fields["supportsVerbosity"])), SupportsServiceTier: boolPtr(first(p.modelValue("modelSupportsServiceTier"), p.fields["supportsServiceTier"])), DirectReasoningEffort: p.in("directReasoningEffortModels", true), ResponsesUpstreamStreaming: boolPtr(p.modelValue("modelResponsesUpstreamStreaming"))}
	if m.MaxInputTokens != nil && m.ContextWindow != nil && *m.MaxInputTokens > *m.ContextWindow {
		v := *m.ContextWindow
		m.MaxInputTokens = &v
	}

	if p.fastOff {
		v := false
		m.SupportsServiceTier = &v
	}
	if len(m.InputModalities) == 0 && p.in("noVisionModels", false) {
		m.InputModalities = []string{"text"}
	}
	cap := p.capabilities()
	m.SupportsTools = boolPtr(cap["supportsTools"])
	m.SupportsReasoning = boolPtr(cap["supportsReasoning"])
	m.SupportsThinkingToggle = boolPtr(cap["supportsThinkingToggle"])
	m.SupportsReasoningEffort = boolPtr(cap["supportsReasoningEffort"])
	m.DefaultThinkingEnabled = boolPtr(cap["defaultThinkingEnabled"])
	if cap["supportsReasoningEffort"] == false {
		m.ReasoningEfforts = []string{}
		m.DefaultReasoningEffort = ""
	}
	if v := stringList(cap["inputModalities"]); len(v) > 0 {
		m.InputModalities = v
	}
	if p.modelValue("modelReasoningSummaryDelivery") != nil && m.SupportsReasoningSummaries == nil {
		b := true
		m.SupportsReasoningSummaries = &b
	}
	if x := p.modelValue("modelResponsesTerminalRepair"); x != nil {
		m.ResponsesTerminalRepair, _ = json.Marshal(x)
	}

	// The pinned client exporter projects interleaving from positive reasoning or
	// summary support; unknown axes must not become an explicit negative.
	if (m.SupportsReasoning != nil && *m.SupportsReasoning) || (m.SupportsReasoningSummaries != nil && *m.SupportsReasoningSummaries) {
		v := InterleavingReasoningContent
		m.ReasoningInterleaving = &v
	} else if m.SupportsReasoning != nil && !*m.SupportsReasoning {
		v := InterleavingNone
		m.ReasoningInterleaving = &v
	}
	return m
}
func (p Resolved) capabilities() map[string]any {
	cap := map[string]any{}
	for k, v := range obj(p.fields["modelCapabilities"]) {
		if strings.EqualFold(k, p.Model) {
			for a, b := range obj(v) {
				cap[a] = b
			}
			break
		}
	}
	if p.in("noReasoningModels", false) {
		for _, k := range []string{"supportsReasoning", "supportsThinkingToggle", "supportsReasoningEffort", "defaultThinkingEnabled"} {
			cap[k] = false
		}
		return cap
	}
	efforts := stringList(first(p.modelValue("modelReasoningEfforts"), p.fields["reasoningEfforts"]))
	strengths := false
	enabled := false
	em := obj(first(p.modelValue("modelReasoningEffortMap"), p.fields["reasoningEffortMap"]))
	for _, effort := range efforts {
		if v, ok := em[effort]; ok {
			effort = str(v)
		}
		switch effort {
		case "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
			strengths = true
		case "enabled", "adaptive":
			enabled = true
		}
	}
	if cap["supportsReasoningEffort"] == nil && strengths {
		cap["supportsReasoningEffort"] = true
	}
	if cap["supportsReasoning"] == nil && (cap["supportsThinkingToggle"] == true || cap["supportsReasoningEffort"] == true || enabled || p.in("thinkingBudgetModels", false)) {
		cap["supportsReasoning"] = true
	}
	if cap["supportsReasoning"] == false {
		cap["supportsThinkingToggle"] = false
		cap["supportsReasoningEffort"] = false
		cap["defaultThinkingEnabled"] = false
	}
	return cap
}
