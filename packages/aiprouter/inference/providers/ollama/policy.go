// SPDX-License-Identifier: Apache-2.0
package ollama

import (
	"encoding/json"
	"strings"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

// PolicyHeader carries trusted, resolved model policy from the credential owner.
// The adapter removes it before transport. Caller HTTP headers never supply it.
const PolicyHeader = "X-Aiprouter-Ollama-Policy"

// Policy describes the selected model, not a provider-wide capability claim.
type Policy struct {
	DefaultMaxOutputTokens *int              `json:"default_max_output_tokens,omitempty"`
	NoTemperature          bool              `json:"no_temperature,omitempty"`
	NoTopP                 bool              `json:"no_top_p,omitempty"`
	NoPenalty              bool              `json:"no_penalty,omitempty"`
	NoReasoning            bool              `json:"no_reasoning,omitempty"`
	SupportedReasoning     []string          `json:"supported_reasoning,omitempty"`
	ReasoningMap           map[string]string `json:"reasoning_map,omitempty"`
	StripModelSuffix       bool              `json:"strip_model_suffix,omitempty"`
}

func policyFor(in inference.AdapterInput) (Policy, error) {
	var p Policy
	raw := in.Credential.Headers.Get(PolicyHeader)
	if raw == "" {
		return p, nil
	}
	if len(raw) > 64<<10 {
		return p, bad("ollama_invalid_policy")
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(&p); e != nil {
		return p, bad("ollama_invalid_policy")
	}
	if _, e := decode([]byte(raw)); e != nil {
		return p, bad("ollama_invalid_policy")
	}
	return p, nil
}
func applyPolicy(b object, p Policy) error {
	if p.DefaultMaxOutputTokens != nil && b["max_tokens"] == nil && b["max_completion_tokens"] == nil {
		b["max_completion_tokens"] = *p.DefaultMaxOutputTokens
	}
	if p.NoTemperature {
		delete(b, "temperature")
	}
	if p.NoTopP {
		delete(b, "top_p")
	}
	if p.NoPenalty {
		delete(b, "presence_penalty")
		delete(b, "frequency_penalty")
	}
	requested, ok := b["reasoning_effort"]
	if !ok {
		if thinking := obj(b["thinking"]); thinking != nil {
			requested = thinking["type"]
			ok = requested != nil
		}
	}
	if !ok {
		return nil
	}
	value := str(requested)
	if value == "ultra" {
		value = "max"
	}
	if p.ReasoningMap[value] == "__omit__" {
		delete(b, "reasoning_effort")
		return nil
	}
	if value == "none" || p.NoReasoning {
		b["reasoning_effort"] = "none"
		return nil
	}
	if mapped, exists := p.ReasoningMap[value]; exists {
		if mapped == "xhigh" || mapped == "ultra" {
			mapped = "max"
		}
		b["reasoning_effort"] = mapped
		return nil
	}
	if value == "minimal" {
		value = "low"
	}
	ladder := []string{"low", "medium", "high", "xhigh", "max", "ultra"}
	rank := func(s string) int {
		for i, v := range ladder {
			if s == v {
				return i
			}
		}
		return -1
	}
	if len(p.SupportedReasoning) > 0 && rank(value) >= 0 {
		best := ""
		for _, v := range p.SupportedReasoning {
			if rank(v) >= 0 && rank(v) <= rank(value) && (best == "" || rank(v) > rank(best)) {
				best = v
			}
		}
		if best == "" {
			for _, v := range p.SupportedReasoning {
				if rank(v) >= 0 && (best == "" || rank(v) < rank(best)) {
					best = v
				}
			}
		}
		if best != "" {
			value = best
		}
	}
	if mapped, exists := p.ReasoningMap[value]; exists {
		value = mapped
	}
	if value == "__omit__" {
		delete(b, "reasoning_effort")
		return nil
	}
	if value == "xhigh" || value == "ultra" {
		value = "max"
	}
	b["reasoning_effort"] = value
	return nil
}
