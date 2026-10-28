// SPDX-License-Identifier: Apache-2.0
package main

import "strings"

var clientEffortOrder = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}

func boolIs(p *bool, value bool) bool { return p != nil && *p == value }
func hasEffort(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
func canonicalEfforts(values []string) []string {
	if values == nil {
		return nil
	}
	out := []string{}
	for _, known := range clientEffortOrder {
		for _, value := range values {
			if strings.ToLower(strings.TrimSpace(value)) == known {
				out = append(out, known)
				break
			}
		}
	}
	return out
}
func exportedEfforts(m ClientModel) []string {
	if boolIs(m.SupportsReasoning, false) {
		return []string{}
	}
	efforts := canonicalEfforts(m.Efforts)
	if efforts == nil && boolIs(m.ReasoningEffortsKnown, true) {
		efforts = []string{}
	}
	if boolIs(m.SupportsReasoningEffort, false) {
		if boolIs(m.SupportsThinkingToggle, true) {
			return []string{"none", "high"}
		}
		return []string{}
	}
	if boolIs(m.SupportsThinkingToggle, true) {
		out := []string{"none"}
		for _, e := range efforts {
			if e != "none" {
				out = append(out, e)
			}
		}
		if len(out) == 1 {
			out = append(out, "high")
		}
		return out
	}
	if boolIs(m.SupportsThinkingToggle, false) && efforts != nil {
		out := []string{}
		for _, e := range efforts {
			if e != "none" {
				out = append(out, e)
			}
		}
		return out
	}
	return efforts
}
func exportedDefault(m ClientModel) *string {
	efforts := exportedEfforts(m)
	value := ""
	if boolIs(m.SupportsThinkingToggle, true) && !boolIs(m.SupportsReasoning, false) {
		if boolIs(m.DefaultThinkingEnabled, false) {
			value = "none"
			return &value
		}
		strengths := false
		for _, e := range canonicalEfforts(m.Efforts) {
			strengths = strengths || e != "none"
		}
		if boolIs(m.SupportsReasoningEffort, false) || !strengths {
			if boolIs(m.DefaultThinkingEnabled, true) {
				value = "high"
				return &value
			}
			return nil
		}
		if boolIs(m.DefaultThinkingEnabled, true) && m.DefaultReasoningEffort != nil && *m.DefaultReasoningEffort == "none" {
			return nil
		}
	}
	if m.DefaultReasoningEffort == nil {
		return nil
	}
	value = strings.ToLower(strings.TrimSpace(*m.DefaultReasoningEffort))
	if !hasEffort(clientEffortOrder, value) || efforts != nil && !hasEffort(efforts, value) {
		return nil
	}
	return &value
}
func declaredReasoning(m ClientModel) *bool {
	if m.SupportsReasoning != nil {
		return m.SupportsReasoning
	}
	positive := boolIs(m.SupportsThinkingToggle, true) || boolIs(m.SupportsReasoningEffort, true) || boolIs(m.SupportsReasoningSummaries, true)
	if !boolIs(m.SupportsReasoningEffort, false) {
		for _, e := range canonicalEfforts(m.Efforts) {
			positive = positive || e != "none"
		}
	}
	if positive {
		return &positive
	}
	return nil
}
func interleaving(m ClientModel) bool {
	if m.ReasoningInterleaving != nil {
		return *m.ReasoningInterleaving == ClientInterleavingContent
	}
	return boolIs(declaredReasoning(m), true) || boolIs(m.SupportsReasoningSummaries, true)
}

// Reserve every exact service selector first. A fast sibling is authorized only
// by explicit service metadata, never by a model name or a local capability guess.
func normalizedExportModels(models []ClientModel) []ClientModel {
	out := make([]ClientModel, 0, len(models))
	seen := map[string]bool{}
	for _, m := range models {
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		if len(m.InputModalities) > 0 {
			m.Vision = false
			for _, input := range m.InputModalities {
				m.Vision = m.Vision || input == "image"
			}
		}
		if m.SupportsTools != nil {
			m.Tools = *m.SupportsTools
		}
		m.DefaultReasoningEffort = exportedDefault(m)
		m.SupportsReasoning = declaredReasoning(m)
		m.Efforts = exportedEfforts(m)
		if m.SupportsTools == nil && m.Tools {
			value := true
			m.SupportsTools = &value
		}
		out = append(out, m)
	}
	for _, m := range append([]ClientModel(nil), out...) {
		if !boolIs(m.FastRowAvailable, true) || seen[m.ID+"--fast"] {
			continue
		}
		m.ID += "--fast"
		m.Name += " Fast"
		value := false
		m.FastRowAvailable = &value
		seen[m.ID] = true
		out = append(out, m)
	}
	return out
}

// Some native schemas require an output budget whenever context is declared.
// The pinned fallback is a serializer requirement, not an asserted model limit.
func modelOutput(m ClientModel) int {
	n := m.Output
	if n <= 0 {
		n = 32000
	}
	if m.Context > 0 && n > m.Context {
		n = m.Context
	}
	return n
}
func selectableEfforts(m ClientModel) []string {
	var out []string
	for _, e := range m.Efforts {
		if e != "none" {
			out = append(out, e)
		}
	}
	return out
}
func applyOpenCodeReasoning(row object, m ClientModel) {
	reasoning := declaredReasoning(m)
	if reasoning != nil {
		row["reasoning"] = *reasoning
	}
	if m.DefaultReasoningEffort != nil {
		row["options"] = object{"reasoningEffort": *m.DefaultReasoningEffort}
	}
	if interleaving(m) {
		row["interleaved"] = object{"field": "reasoning_content"}
	}
	variants := object{}
	suppress := boolIs(m.SupportsThinkingToggle, true) || m.SupportsReasoningEffort != nil || boolIs(m.SupportsReasoning, false) || (boolIs(m.SupportsThinkingToggle, false) && m.Efforts != nil)
	if suppress || (len(m.Efforts) == 0 && (boolIs(reasoning, true) || boolIs(m.SupportsReasoningEffort, false))) {
		for _, e := range clientEffortOrder {
			if e != "ultra" || suppress {
				variants[e] = object{"disabled": true}
			}
		}
	}
	if boolIs(m.SupportsThinkingToggle, false) {
		variants["none"] = object{"disabled": true}
	}
	for _, e := range m.Efforts {
		variants[e] = object{"reasoningEffort": e}
	}
	if len(variants) > 0 || m.Efforts != nil {
		row["variants"] = variants
	}
}
