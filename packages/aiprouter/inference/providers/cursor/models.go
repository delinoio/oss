// SPDX-License-Identifier: Apache-2.0
// Model facts and alias behavior adapted from the pinned MIT source; see LICENSE.upstream.
package cursor

import (
	"regexp"
	"strings"
)

type modelVariant uint8

const (
	regular modelVariant = iota
	thinking
	fast
	thinkingFast
)

type modelCapability struct {
	defaultKind     modelVariant
	prefix, maxMode bool
	variants        map[modelVariant]string
}

// This is the pinned catalog, not a claim of current account entitlement.
// Unknown model identities pass through; destination discovery remains host-owned.
var modelCapabilities = map[string]modelCapability{
	"claude-4.5-opus":    {defaultKind: thinking, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "high", thinking: "high"}},
	"claude-4.6-opus":    {defaultKind: thinking, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "high max", thinking: "high max"}},
	"claude-4.6-sonnet":  {defaultKind: thinking, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "medium", thinking: "medium"}},
	"claude-4.5-sonnet":  {defaultKind: thinking, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "", thinking: ""}},
	"claude-4-sonnet":    {defaultKind: thinking, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "", thinking: ""}},
	"claude-fable-5":     {defaultKind: thinking, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high xhigh max", thinking: "low medium high xhigh max"}},
	"claude-fable-5-1":   {defaultKind: thinking, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high xhigh max", thinking: "low medium high xhigh max"}},
	"claude-haiku-5-5":   {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high xhigh max"}},
	"claude-sonnet-5-5":  {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high xhigh max"}},
	"claude-sonnet-5":    {defaultKind: thinking, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high xhigh max", thinking: "low medium high xhigh max"}},
	"claude-opus-4-7":    {defaultKind: thinking, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high xhigh max", thinking: "low medium high xhigh max", fast: "low medium high xhigh max", thinkingFast: "low medium high xhigh max"}},
	"claude-opus-4-8":    {defaultKind: thinking, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high xhigh max", thinking: "low medium high xhigh max", fast: "low medium high xhigh max", thinkingFast: "low medium high xhigh max"}},
	"claude-opus-5":      {defaultKind: thinking, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high xhigh max", thinking: "low medium high xhigh max", fast: "low medium high", thinkingFast: "low medium high xhigh max"}},
	"claude-opus-5-5":    {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high xhigh max", fast: "low medium high xhigh max"}},
	"glm-5.2":            {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "high max"}},
	"glm-5.3":            {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low high max"}},
	"gemini-3.6-flash":   {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "minimal low medium high"}},
	"gemini-3.7-flash":   {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high"}},
	"gemini-3.8-flash":   {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high"}},
	"gemini-4-argon":     {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high"}},
	"muse-spark-1.3":     {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "minimal low medium high xhigh"}},
	"kimi-k3":            {defaultKind: regular, prefix: false, maxMode: true, variants: map[modelVariant]string{regular: "low high max"}},
	"grok-4.5":           {defaultKind: regular, prefix: true, maxMode: false, variants: map[modelVariant]string{regular: "low medium high", fast: "low medium high"}},
	"grok-4.6":           {defaultKind: regular, prefix: true, maxMode: false, variants: map[modelVariant]string{regular: "low medium high xhigh", fast: "low medium high xhigh"}},
	"grok-4.7":           {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high xhigh", fast: "low medium high xhigh"}},
	"gpt-5.1":            {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low high"}},
	"gpt-5.1-codex-max":  {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high xhigh"}},
	"gpt-5.1-codex-mini": {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low high"}},
	"gpt-5.2":            {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low high xhigh"}},
	"gpt-5.2-codex":      {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low high xhigh"}},
	"gpt-5.3-codex":      {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low high xhigh"}},
	"gpt-5.4":            {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high xhigh"}},
	"gpt-5.4-mini":       {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high xhigh"}},
	"gpt-5.4-nano":       {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high xhigh"}},
	"gpt-5.5":            {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high"}},
	"gpt-5.5-extra":      {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "high"}},
	"gpt-5.6-sol":        {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high xhigh max"}},
	"gpt-5.6-terra":      {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high xhigh max"}},
	"gpt-5.6-luna":       {defaultKind: regular, prefix: false, maxMode: false, variants: map[modelVariant]string{regular: "low medium high xhigh max"}},
}

type modelIdentity struct {
	base, source, effort       string
	kind                       modelVariant
	versionFirst, ultra, known bool
}

var claudeAnthropic = regexp.MustCompile(`^claude-(fable|haiku|opus|sonnet)-(\d+(?:[.-]\d+)*)$`)
var claudeVersionFirst = regexp.MustCompile(`^claude-(\d+(?:\.\d+)*)-(fable|haiku|opus|sonnet)$`)
var modelLevels = []string{"extra-high", "minimal", "medium", "xhigh", "none", "low", "high", "max"}

func modelBase(stem string) (string, string, bool) {
	if m := claudeAnthropic.FindStringSubmatch(stem); m != nil {
		old := "claude-" + strings.ReplaceAll(m[2], "-", ".") + "-" + m[1]
		if _, ok := modelCapabilities[old]; ok {
			return old, stem, false
		}
		return "claude-" + m[1] + "-" + strings.ReplaceAll(m[2], ".", "-"), stem, false
	}
	if m := claudeVersionFirst.FindStringSubmatch(stem); m != nil {
		if _, ok := modelCapabilities[stem]; ok {
			return stem, stem, true
		}
		return "claude-" + m[2] + "-" + strings.ReplaceAll(m[1], ".", "-"), stem, true
	}
	return stem, stem, false
}
func stripModelLevel(s string) (string, string) {
	// Preserve literal catalog identities such as gpt-5.1-codex-max.
	if _, ok := modelCapabilities[s]; ok {
		return s, ""
	}
	for _, l := range modelLevels {
		if strings.HasSuffix(s, "-"+l) {
			return strings.TrimSuffix(s, "-"+l), l
		}
	}
	return s, ""
}
func parseModel(id string) modelIdentity {
	id = strings.TrimSpace(id)
	unknown := modelIdentity{base: id, source: id}
	if id == "claude-4-sonnet-1m" {
		return unknown
	}
	stem := id
	if strings.HasPrefix(stem, "claude-") {
		stem = strings.ToLower(stem)
	}
	base, source, version := modelBase(stem)
	if cap, ok := modelCapabilities[base]; ok {
		return modelIdentity{base: base, source: source, versionFirst: version, kind: cap.defaultKind, known: true}
	}
	if strings.HasPrefix(stem, "cursor-") {
		p := parseModel(strings.TrimPrefix(stem, "cursor-"))
		if p.known {
			return p
		}
	}
	if strings.HasSuffix(stem, "-1m") {
		base := strings.TrimSuffix(stem, "-1m")
		if _, ok := modelCapabilities[base]; ok {
			return modelIdentity{base: base, source: base, ultra: true, known: true}
		}
	}
	isFast := strings.HasSuffix(stem, "-fast")
	if isFast {
		stem = strings.TrimSuffix(stem, "-fast")
	}
	isThinking := strings.HasSuffix(stem, "-thinking")
	effort := ""
	if isThinking {
		stem, effort = stripModelLevel(strings.TrimSuffix(stem, "-thinking"))
	} else {
		stem, effort = stripModelLevel(stem)
		if strings.HasSuffix(stem, "-thinking") {
			isThinking = true
			stem = strings.TrimSuffix(stem, "-thinking")
		}
	}
	base, source, version = modelBase(stem)
	if _, ok := modelCapabilities[base]; !ok {
		return unknown
	}
	kind := regular
	if isThinking {
		kind = thinking
	}
	if isFast {
		if isThinking {
			kind = thinkingFast
		} else {
			kind = fast
		}
	}
	return modelIdentity{base: base, source: source, versionFirst: version, effort: effort, kind: kind, known: true}
}
func selectEffort(levels, requested string) string {
	tiers := strings.Fields(levels)
	if len(tiers) == 0 {
		return ""
	}
	requested = strings.ToLower(requested)
	if requested == "ultra" {
		requested = "max"
	}
	for _, tier := range tiers {
		if tier == requested {
			return requested
		}
	}
	switch requested {
	case "none", "minimal", "low":
		return tiers[0]
	case "medium":
		return tiers[(len(tiers)-1)/2]
	default:
		return tiers[len(tiers)-1]
	}
}
func selectModel(id, effort string, wantFast bool) (string, map[string]any, bool) {
	id = strings.TrimPrefix(id, "cursor/")
	if id == "auto" {
		return "default", nil, false
	}
	for _, level := range []string{"cost", "balance", "intelligence"} {
		if id == "auto-"+level {
			return "default", map[string]any{"optimization": level}, false
		}
	}
	p := parseModel(id)
	if !p.known {
		return id, nil, false
	}
	cap := modelCapabilities[p.base]
	kind := p.kind
	if wantFast {
		next := fast
		if kind == thinking || kind == thinkingFast {
			next = thinkingFast
		}
		if _, ok := cap.variants[next]; ok {
			kind = next
		}
	}
	if p.effort != "" {
		effort = p.effort
	}
	levels, ok := cap.variants[kind]
	if !ok {
		return p.base, nil, false
	}
	rung := selectEffort(levels, effort)
	maxMode := cap.maxMode && (p.ultra || strings.EqualFold(effort, "ultra"))
	if kind == fast && cap.prefix {
		return p.base, map[string]any{"effort": rung, "fast": "true"}, maxMode
	}
	wire := p.source
	if kind == thinking || kind == thinkingFast {
		if p.versionFirst && rung != "" {
			wire += "-" + rung + "-thinking"
		} else {
			wire += "-thinking"
			if rung != "" {
				wire += "-" + rung
			}
		}
	} else if rung != "" {
		wire += "-" + rung
	}
	if kind == fast || kind == thinkingFast {
		wire += "-fast"
	}
	if cap.prefix && kind == regular {
		wire = "cursor-" + wire
	}
	return wire, nil, maxMode
}
