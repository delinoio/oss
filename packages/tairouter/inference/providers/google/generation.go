// SPDX-License-Identifier: Apache-2.0
package google

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
)

var proFamily = regexp.MustCompile(`(^|[-.])pro([-.]|$)`)

func outputCap(model string) int64 {
	m := strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(m, "gemini") {
		if proFamily.MatchString(m) {
			return 65535
		}
		return 65536
	}
	if strings.HasPrefix(m, "claude") {
		return 64000
	}
	if strings.HasPrefix(m, "gpt-oss") {
		return 32768
	}
	return 0
}
func imageModel(model string) bool {
	switch model {
	case "gemini-3.1-flash-image", "gemini-2.0-flash-preview-image-generation", "gemini-3-pro-image-preview":
		return true
	}
	return false
}
func normalizedEffort(e string) string {
	switch strings.ToLower(e) {
	case "max", "xhigh", "ultra":
		return "high"
	case "minimal", "low", "medium", "high":
		return strings.ToLower(e)
	}
	return ""
}

// ResolveModel implements the pin's static aliases and effort defaults. Live
// discovered families and entitlement remain the owner's routing responsibility.
func ResolveModel(model, effort string, cca bool) (string, string) {
	e := normalizedEffort(effort)
	if !cca {
		switch model {
		case "gemini-3.7-flash", "gemini-3.6-flash":
			return model + "-tiered", e
		}
		return model, e
	}
	retired := map[string]string{"gemini-3.6-flash": "medium", "gemini-3.6-flash-low": "low", "gemini-3.6-flash-medium": "medium", "gemini-3.6-flash-high": "high", "gemini-3.5-flash-extra-low": "low", "gemini-3.5-flash-low": "medium", "gemini-3.5-flash-mid": "medium", "gemini-3.5-flash-high": "high", "gemini-3-flash-agent": "high"}
	if tier, ok := retired[model]; ok {
		if e == "" || e == "minimal" {
			e = tier
		}
		return "gemini-3.7-flash-tiered", e
	}
	switch model {
	case "gemini-3.1-pro-high", "gemini-3.1-pro-preview":
		return "gemini-pro-agent", ""
	case "gemini-3.1-pro-low", "gemini-pro-agent":
		return model, ""
	case "gemini-3.7-flash":
		if e == "" || e == "minimal" {
			e = "medium"
		}
		return "gemini-3.7-flash-tiered", e
	case "gemini-4-argon", "gemini-3.8-flash", "claude-opus-5-5", "claude-sonnet-5-5":
		if e == "" || e == "minimal" {
			e = "medium"
		}
		return model + "-" + e, ""
	case "gemini-3.1-pro":
		if e == "low" {
			return "gemini-3.1-pro-low", "low"
		}
		if e == "high" {
			return "gemini-pro-agent", "high"
		}
		return "gemini-pro-agent", ""
	}
	for _, suffix := range []string{"-low", "-medium", "-high"} {
		if strings.HasSuffix(model, suffix) {
			return model, ""
		}
	}
	if model == "claude-sonnet-4-6" || model == "claude-opus-4-6-thinking" {
		if e == "minimal" {
			e = ""
		}
		return model, e
	}
	return model, ""
}

func (a *Adapter) normalizeGeneration(body, root object, model string) string {
	gc := obj(body["generationConfig"])
	if gc == nil {
		gc = object{}
	}
	thinking := obj(gc["thinkingConfig"])
	effort := text(thinking["thinkingLevel"])
	ladder := a.options.ReasoningEfforts
	if ladder == nil {
		ladder = pinnedLadder(model, a.cca)
	}
	effort = mapEffort(effort, ladder)
	wire, level := ResolveModel(model, effort, a.cca)
	if !a.cca && a.options.DisableDirectModelRenames {
		wire = model
	}
	if cap := outputCap(wire); cap > 0 {
		if n, ok := gc["maxOutputTokens"].(int64); ok && n > cap {
			gc["maxOutputTokens"] = cap
		}
	}
	for key, max := range map[string]float64{"temperature": 2, "topP": 1} {
		if n, ok := gc[key].(json.Number); ok {
			value, err := n.Float64()
			if err != nil || value < 0 {
				delete(gc, key)
			} else if value > max {
				gc[key] = max
			}
		}
	}
	if stops := array(gc["stopSequences"]); stops != nil {
		seen := map[string]bool{}
		var out []any
		for _, v := range stops {
			s := text(v)
			if s != "" && !seen[s] {
				out = append(out, s)
				seen[s] = true
				if len(out) == 5 {
					break
				}
			}
		}
		if len(out) > 0 {
			gc["stopSequences"] = out
		} else {
			delete(gc, "stopSequences")
		}
	}
	eligible := a.cca
	if !a.cca {
		eligible = len(ladder) > 0
	}
	// The pinned compiler emits thinkingLevel/includeThoughts, not a budget.
	// Anthropic budget settings do not imply a supported Gemini thinkingBudget.
	delete(gc, "thinkingConfig")
	if eligible && !imageModel(model) {
		out := object{}
		if level != "" {
			out["thinkingLevel"] = level
		}
		if thinking["includeThoughts"] == true {
			out["includeThoughts"] = true
		}
		if len(out) > 0 {
			gc["thinkingConfig"] = out
		}
	}
	if imageModel(model) {
		gc["responseModalities"] = []any{"TEXT", "IMAGE"}
	}
	if len(gc) > 0 {
		body["generationConfig"] = gc
	} else {
		delete(body, "generationConfig")
	}
	return wire
}

func pinnedLadder(model string, cca bool) []string {
	if cca {
		switch model {
		case "gemini-4-argon", "gemini-3.8-flash", "gemini-3.7-flash", "claude-opus-5-5", "claude-sonnet-5-5":
			return []string{"low", "medium", "high"}
		case "gemini-3.1-pro":
			return []string{"low", "high"}
		case "claude-sonnet-4-6", "claude-opus-4-6-thinking":
			return []string{"low", "medium", "high", "max"}
		}
	} else {
		switch model {
		case "gemini-4-argon", "gemini-3.8-flash", "gemini-3.7-flash", "gemini-3.1-pro-preview":
			return []string{"low", "medium", "high"}
		case "gemini-3.5-flash", "gemini-3.6-flash":
			return []string{"minimal", "low", "medium", "high"}
		}
	}
	return nil
}
func mapEffort(e string, ladder []string) string {
	if e == "" || e == "none" {
		return ""
	}
	if e == "ultra" {
		e = "max"
	}
	if e == "minimal" {
		e = "low"
	}
	if ladder == nil {
		return e
	}
	if len(ladder) == 0 {
		return ""
	}
	rank := func(s string) int {
		for i, v := range []string{"low", "medium", "high", "xhigh", "max", "ultra"} {
			if s == v {
				return i
			}
		}
		return -1
	}
	best := ladder[0]
	bestRank := rank(best)
	requested := rank(e)
	for _, v := range ladder {
		if v == e {
			return e
		}
		r := rank(v)
		if r <= requested && r > bestRank {
			best = v
			bestRank = r
		}
	}
	return best
}

func pruneProgress(parts []any) []any {
	hasCall := false
	for _, p := range parts {
		if obj(p)["functionCall"] != nil {
			hasCall = true
			break
		}
	}
	if !hasCall {
		return parts
	}
	var out []any
	after, kept := false, false
	for _, raw := range parts {
		p := obj(raw)
		if p["functionCall"] != nil {
			after = true
		}
		if s, ok := p["text"].(string); ok && !after && p["thought"] != true {
			if kept {
				continue
			}
			lead := progressLead(s)
			if lead == "" {
				continue
			}
			copy := object{}
			for k, v := range p {
				copy[k] = v
			}
			copy["text"] = lead
			out = append(out, copy)
			kept = true
			continue
		}
		out = append(out, raw)
	}
	return out
}
func jsSpace(r rune) bool { return unicode.IsSpace(r) || r == '\ufeff' }
func progressLead(s string) string {
	s = strings.TrimFunc(s, jsSpace)
	if s == "" {
		return ""
	}
	r := []rune(s)
	end := len(r)
	contentEnd := 0
	closing := func(c rune) bool { return strings.ContainsRune("\"'”’)]*_`」』", c) }
	for i := 0; i < len(r); i++ {
		c := r[i]
		boundary := false
		j := i + 1
		if c == '\n' {
			boundary = true
		} else if strings.ContainsRune(".!?。！？।॥۔؟։።။", c) {
			for j < len(r) && closing(r[j]) {
				j++
			}
			boundary = !strings.ContainsRune(".!?", c) || j == len(r) || jsSpace(r[j])
		}
		if !jsSpace(c) {
			contentEnd = i + 1
		}
		if !boundary {
			continue
		}
		if c != '\n' {
			contentEnd = j
		}
		start := contentEnd - 5
		if start < 0 {
			start = 0
		}
		suffix := strings.ToLower(string(r[start:contentEnd]))
		exception := false
		for _, abbr := range []string{"e.g.", "i.e.", "etc.", "vs."} {
			if strings.HasSuffix(suffix, abbr) {
				pos := contentEnd - len([]rune(abbr))
				if pos == 0 || jsSpace(r[pos-1]) {
					exception = true
				}
			}
		}
		if contentEnd <= 4 && contentEnd > 1 && r[contentEnd-1] == '.' {
			token := r[:contentEnd-1]
			digits := len(token) <= 3
			for _, v := range token {
				digits = digits && v >= '0' && v <= '9'
			}
			letter := len(token) == 1 && (token[0] >= 'a' && token[0] <= 'z' || token[0] >= 'A' && token[0] <= 'Z')
			exception = exception || digits || letter
		}
		if !exception {
			end = j
			break
		}
		i = j - 1
	}
	lead := strings.TrimFunc(string(r[:end]), jsSpace)
	if strings.HasPrefix(lead, "```") {
		return ""
	}
	return lead
}
