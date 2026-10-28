// SPDX-License-Identifier: Apache-2.0
package zed

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

func applyTranslatedOptions(out, b object, from, to inference.Wire) {
	effort := stringOf(b["reasoning_effort"])
	if v := stringOf(objectOf(b["reasoning"])["effort"]); v != "" {
		effort = v
	}
	if effort == "ultra" {
		effort = "max"
	}
	if effort != "" {
		switch to {
		case inference.Chat:
			out["reasoning_effort"] = effort
		case inference.Responses:
			out["reasoning"] = object{"effort": effort}
		case inference.Anthropic:
			if effort == "none" {
				out["thinking"] = object{"type": "disabled"}
			} else {
				budget := map[string]int{"minimal": 1024, "low": 4096, "medium": 8192, "high": 16384, "xhigh": 24576, "max": 32000}[effort]
				if budget == 0 {
					budget = 8192
				}
				out["thinking"] = object{"type": "enabled", "budget_tokens": budget}
				out["max_tokens"] = budget + 4096
			}
		}
	}
	if b["thinking"] != nil {
		out["thinking"] = b["thinking"]
	}
	format := objectOf(b["response_format"])
	if from == inference.Responses {
		format = objectOf(objectOf(b["text"])["format"])
	}
	if format != nil {
		if to == inference.Chat {
			if from == inference.Responses && format["type"] == "json_schema" {
				out["response_format"] = object{"type": "json_schema", "json_schema": format}
			} else {
				out["response_format"] = format
			}
		} else if to == inference.Responses {
			if format["type"] == "json_schema" && format["json_schema"] != nil {
				format = objectOf(format["json_schema"])
				format["type"] = "json_schema"
			}
			out["text"] = object{"format": format}
		} else {
			if format["type"] == "json_schema" && format["json_schema"] != nil {
				format = object{"type": "json_schema", "schema": objectOf(format["json_schema"])["schema"]}
			}
			config := objectOf(out["output_config"])
			if config == nil {
				config = object{}
			}
			config["format"] = format
			out["output_config"] = config
		}
	}
	stop := b["stop_sequences"]
	if stop == nil {
		stop = b["stop"]
	}
	if stop != nil {
		if to == inference.Anthropic {
			if s, ok := stop.(string); ok {
				stop = []any{s}
			}
			out["stop_sequences"] = stop
		} else {
			out["stop"] = stop
		}
	}
}

var claudeVersion = regexp.MustCompile(`(?i)(?:^|/)claude-([a-z]+)-([0-9]+)(?:[.-]([0-9]{1,2})(?:[^0-9]|$))?`)

func finishAnthropicOptions(out, b object) {
	effort := stringOf(b["reasoning_effort"])
	if v := stringOf(objectOf(b["reasoning"])["effort"]); v != "" {
		effort = v
	}
	if effort == "ultra" {
		effort = "max"
	}
	if effort == "" || b["thinking"] != nil {
		return
	}
	m := claudeVersion.FindStringSubmatch(stringOf(b["model"]))
	adaptive := false
	between := false
	if len(m) > 0 {
		major, _ := strconv.Atoi(m[2])
		minor, _ := strconv.Atoi(m[3])
		version := major*100 + minor
		switch m[1] {
		case "sonnet":
			adaptive = version >= 500
			between = version >= 505
		case "opus":
			adaptive = version >= 407
		case "fable":
			adaptive = true
		case "haiku":
			adaptive = version >= 505
		}
	}
	if effort == "none" {
		if between {
			out["thinking"] = object{"type": "between_tools"}
			delete(out, "temperature")
			delete(out, "top_p")
		}
		return
	}
	budget := map[string]int64{"minimal": 1024, "low": 4096, "medium": 8192, "high": 16384, "xhigh": 24576, "max": 32000}[effort]
	if budget == 0 {
		budget = 8192
	}
	if adaptive {
		if effort == "minimal" {
			effort = "low"
		}
		out["thinking"] = object{"type": "adaptive", "display": "summarized"}
		config := objectOf(out["output_config"])
		if config == nil {
			config = object{}
		}
		config["effort"] = effort
		out["output_config"] = config
		explicit := b["max_output_tokens"] != nil || b["max_completion_tokens"] != nil || b["max_tokens"] != nil
		if !explicit {
			out["max_tokens"] = budget + 8192
		}
	} else {
		maxTokens := budget + 8192
		for _, k := range []string{"max_output_tokens", "max_completion_tokens", "max_tokens"} {
			if n, ok := validCount(b[k]); ok && n > maxTokens {
				maxTokens = n
			}
		}
		out["max_tokens"] = maxTokens
		out["thinking"] = object{"type": "enabled", "budget_tokens": budget}
	}
	delete(out, "temperature")
	delete(out, "top_p")
}

func imageModel(model string) bool {
	switch model {
	case "gemini-3.1-flash-image", "gemini-2.0-flash-preview-image-generation", "gemini-3-pro-image-preview":
		return true
	}
	return false
}
func applyGooglePolicy(out, b object, model string, options RequestOptions) {
	gc := objectOf(out["generationConfig"])
	if gc == nil {
		gc = object{}
	}
	max := int64(0)
	for _, key := range []string{"max_output_tokens", "max_completion_tokens", "max_tokens"} {
		if n, ok := validCount(b[key]); ok && n > 0 {
			max = n
			break
		}
	}
	cap := int64(0)
	lower := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(lower, "gemini"):
		cap = 65536
		for _, part := range strings.FieldsFunc(lower, func(r rune) bool { return r == '-' || r == '.' }) {
			if part == "pro" {
				cap = 65535
			}
		}
	case strings.HasPrefix(lower, "claude"):
		cap = 64000
	case strings.HasPrefix(lower, "gpt-oss"):
		cap = 32768
	}
	if max > 0 {
		if cap > 0 && max > cap {
			max = cap
		}
		gc["maxOutputTokens"] = max
	}
	effort := stringOf(b["reasoning_effort"])
	if v := stringOf(objectOf(b["reasoning"])["effort"]); v != "" {
		effort = v
	}
	if effort == "ultra" {
		effort = "max"
	}
	eligible := model == "gemini-3.5-flash" || model == "gemini-3.6-flash"
	if options.ReasoningEfforts != nil {
		eligible = len(options.ReasoningEfforts) > 0
	}
	delete(gc, "thinkingConfig")
	if imageModel(model) {
		gc["responseModalities"] = []any{"TEXT", "IMAGE"}
	} else if eligible {
		thinking := object{}
		if _, mapped := options.ReasoningEffortMap[effort]; !mapped {
			switch effort {
			case "minimal":
				effort = "low"
			case "low", "medium", "high", "xhigh", "max":
			default:
				effort = ""
			}
		}
		if mapped, ok := options.ReasoningEffortMap[effort]; ok {
			effort = mapped
		} else if options.ReasoningEfforts != nil && effort != "" {
			ladder := []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}
			rank := func(s string) int {
				for i, v := range ladder {
					if s == v {
						return i
					}
				}
				return -1
			}
			chosen := ""
			lowest := ""
			for _, v := range options.ReasoningEfforts {
				if lowest == "" || rank(v) < rank(lowest) {
					lowest = v
				}
				if rank(v) <= rank(effort) && (chosen == "" || rank(v) > rank(chosen)) {
					chosen = v
				}
			}
			if chosen == "" {
				chosen = lowest
			}
			effort = chosen
			if mapped, ok := options.ReasoningEffortMap[effort]; ok {
				effort = mapped
			}
		} else if effort == "minimal" {
			effort = "low"
		}
		if effort != "" && effort != "__omit__" {
			thinking["thinkingLevel"] = effort
		}
		if options.ShowThinkingSummaries && objectOf(b["reasoning"])["summary"] != "none" {
			thinking["includeThoughts"] = true
		}
		if len(thinking) > 0 {
			gc["thinkingConfig"] = thinking
		}
	}
	if len(gc) > 0 {
		out["generationConfig"] = gc
	}
}

func googleGeneration(b object) object {
	gc := object{}
	for from, to := range map[string]string{"temperature": "temperature", "top_p": "topP"} {
		if b[from] != nil {
			gc[to] = b[from]
		}
	}
	for _, k := range []string{"max_output_tokens", "max_completion_tokens", "max_tokens"} {
		if b[k] != nil {
			gc["maxOutputTokens"] = b[k]
			break
		}
	}
	stop := b["stop_sequences"]
	if stop == nil {
		stop = b["stop"]
	}
	if stop != nil {
		if s, ok := stop.(string); ok {
			stop = []any{s}
		}
		gc["stopSequences"] = stop
	}
	f := objectOf(b["response_format"])
	if f == nil {
		f = objectOf(objectOf(b["text"])["format"])
	}
	if f != nil && f["type"] != "text" {
		gc["responseMimeType"] = "application/json"
		if f["type"] == "json_schema" {
			schema := f["schema"]
			if schema == nil {
				schema = objectOf(f["json_schema"])["schema"]
			}
			gc["responseJsonSchema"] = schema
		}
	}
	return gc
}

func jsonString(v any) string { b, _ := json.Marshal(v); return string(b) }
