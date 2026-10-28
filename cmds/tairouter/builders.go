// SPDX-License-Identifier: Apache-2.0
package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type object = map[string]any

const providerID = "tairouter"
const placeholder = "tairouter-loopback"

func frag(path []string, value any) Fragment { return Fragment{path, value} }
func providerFragment(root string, value any) []Fragment {
	return []Fragment{frag([]string{root, providerID}, value)}
}
func selector(key, value string) (string, error) {
	if strings.ContainsAny(value, ",]\r\n") {
		return "", errors.New("model identity cannot be represented by the client's ownership selector")
	}
	return "[" + key + "=" + value + "]", nil
}
func modelRows(models []ClientModel) []any {
	rows := []any{}
	for _, m := range models {
		input := clientInput(m, false)
		row := object{"id": m.ID, "name": m.Name, "input": input}
		if m.Context > 0 {
			row["contextWindow"] = m.Context
		}
		if m.Context > 0 {
			row["maxTokens"] = modelOutput(m)
		}
		rows = append(rows, row)
	}
	return rows
}
func contributions(id, path, base, key string, models []ClientModel, doc object) ([]Fragment, error) {
	models = compatibleClientModels(id, normalizedExportModels(models))
	if len(models) == 0 {
		return nil, errors.New("no routed models are available")
	}
	for _, m := range models {
		if m.ID == "" || strings.Contains(m.ID, "${") || strings.Contains(m.Name, "${") {
			return nil, errors.New("unsafe model catalog identity")
		}
	}
	pi := object{"baseUrl": base, "api": "openai-completions", "apiKey": key, "models": modelRows(models)}
	switch id {
	case "pi", "prime", "aside", "omo", "gentle-shell", "omp", "gajae":
		rows := pi["models"].([]any)
		for i, m := range models {
			row := rows[i].(object)
			if len(m.Efforts) > 0 {
				row["reasoning"] = true
				switch id {
				case "omp":
					var efforts []string
					for _, v := range m.Efforts {
						if v != "none" && v != "ultra" {
							efforts = append(efforts, v)
						}
					}
					if len(efforts) > 0 {
						thinking := object{"mode": "effort", "efforts": efforts}
						if m.DefaultReasoningEffort != nil && hasEffort(efforts, *m.DefaultReasoningEffort) {
							thinking["defaultLevel"] = *m.DefaultReasoningEffort
						}
						row["thinking"] = thinking
					} else {
						delete(row, "reasoning")
					}
				case "gajae":
					var efforts []string
					for _, v := range m.Efforts {
						if v != "none" && v != "ultra" {
							efforts = append(efforts, v)
						}
					}
					if len(efforts) > 0 {
						row["thinking"] = object{"mode": "effort", "minLevel": efforts[0], "maxLevel": efforts[len(efforts)-1], "levels": efforts}
						row["compat"] = object{"supportsReasoningEffort": true}
					} else {
						delete(row, "reasoning")
					}
				default:
					levels := object{}
					for _, v := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
						levels[v] = nil
						wire := v
						if v == "off" {
							wire = "none"
						}
						for _, offered := range m.Efforts {
							if offered == wire || (v == "max" && offered == "ultra") {
								levels[v] = offered
								break
							}
						}
					}
					row["thinkingLevelMap"] = levels
				}
			}
		}
		if id == "pi" || id == "omo" || id == "gentle-shell" {
			pi["compat"] = object{"sendSessionAffinityHeaders": true, "supportsDeveloperRole": false}
		}
		return providerFragment("providers", pi), nil
	case "qoder", "qoder-cn":
		rows := []any{}
		for _, m := range models {
			window := m.Context
			if window == 0 {
				window = 128000
			}
			rows = append(rows, object{"model": m.ID, "displayName": m.Name, "contextWindow": window})
		}
		return providerFragment("providers", object{"type": "openai-compatible", "protocol": "openai", "displayName": "TaiRouter", "baseUrl": base, "apiKey": key, "defaultModel": models[0].ID, "models": rows}), nil
	case "workbuddy", "workbuddy-ai":
		var f []Fragment
		for _, m := range models {
			s, e := selector("id", m.ID)
			if e != nil {
				return nil, e
			}
			row := object{"id": m.ID, "name": "TaiRouter: " + m.Name, "vendor": "OpenAI", "url": base + "/chat/completions", "apiKey": key}
			if m.SupportsTools != nil {
				row["supportsToolCall"] = *m.SupportsTools
			}
			if len(m.InputModalities) > 0 || m.Vision {
				row["supportsImages"] = m.Vision
			}
			if m.SupportsReasoning != nil {
				row["supportsReasoning"] = *m.SupportsReasoning
			}
			if m.MaxInputTokens != nil && *m.MaxInputTokens > 0 {
				row["maxInputTokens"] = *m.MaxInputTokens
			}
			if m.Output > 0 {
				row["maxOutputTokens"] = m.Output
			}
			target := []string{"models", s}
			if _, ok := doc["$sequence"]; ok {
				target = []string{s}
			}
			f = append(f, frag(target, row))
		}
		return f, nil
	case "opencode", "kilo":
		catalog := object{}
		v2 := object{}
		for _, m := range models {
			row := object{"name": m.Name}
			if m.SupportsTools != nil {
				row["tool_call"] = *m.SupportsTools
			}
			if m.Vision {
				row["attachment"] = true
			}
			if len(m.InputModalities) > 0 {
				input := clientInput(m, true)
				attachment := false
				for _, v := range input {
					attachment = attachment || v != "text"
				}
				if len(input) > 0 {
					row["attachment"] = attachment
					row["modalities"] = object{"input": input, "output": []string{"text"}}
				}
			}
			if m.Context > 0 {
				limit := object{"context": m.Context, "output": modelOutput(m)}
				if m.MaxInputTokens != nil && *m.MaxInputTokens > 0 {
					input := *m.MaxInputTokens
					if input > uint64(m.Context) {
						input = uint64(m.Context)
					}
					limit["input"] = input
				}
				row["limit"] = limit
			}
			modern := []any{}
			for _, effort := range m.Efforts {
				modern = append(modern, object{"id": effort, "settings": object{"reasoningEffort": effort}})
			}
			applyOpenCodeReasoning(row, m)
			catalog[m.ID] = row
			caps := object{}
			if m.SupportsTools != nil {
				caps["tools"] = *m.SupportsTools
			}
			if len(m.InputModalities) > 0 && len(clientInput(m, true)) > 0 {
				caps["input"] = clientInput(m, true)
				caps["output"] = []string{"text"}
			}
			modernRow := object{"name": m.Name, "variants": modern}
			if m.SupportsTools != nil {
				modernRow["capabilities"] = caps
			}
			if interleaving(m) {
				modernRow["compatibility"] = object{"reasoningField": "reasoning_content"}
			}
			if m.DefaultReasoningEffort != nil {
				modernRow["settings"] = object{"reasoningEffort": *m.DefaultReasoningEffort}
			}
			if limit, ok := row["limit"]; ok {
				modernRow["limit"] = limit
			}
			v2[m.ID] = modernRow
		}
		connection := object{"baseURL": base, "apiKey": key}
		f := providerFragment("provider", object{"npm": "@ai-sdk/openai-compatible", "name": "TaiRouter", "options": connection, "models": catalog})
		if id == "opencode" {
			f = append(f, frag([]string{"providers", providerID}, object{"package": "@opencode/ai/providers/openai-compatible", "name": "TaiRouter", "settings": connection, "models": v2}))
		}
		return f, nil
	case "hermes":
		catalog := object{}
		for _, m := range models {
			row := object{}
			if len(m.InputModalities) > 0 || m.Vision {
				row["supports_vision"] = m.Vision
			}
			catalog[m.ID] = row
		}
		return providerFragment("providers", object{"api": base, "api_key": key, "api_mode": "chat_completions", "session_affinity_header": "session-id", "discover_models": false, "models": catalog}), nil
	case "openclaw":
		rows := []any{}
		for _, m := range models {
			row := object{"id": m.ID, "name": m.Name}
			if m.Context > 0 {
				row["contextWindow"] = m.Context
			}
			if len(m.InputModalities) > 0 || m.Vision {
				input := []string{}
				for _, v := range clientInput(m, true) {
					if v != "pdf" {
						input = append(input, v)
					}
				}
				if len(input) > 0 {
					row["input"] = input
				}
			}
			rows = append(rows, row)
		}
		pi["models"] = rows
		return []Fragment{frag([]string{"models", "providers", providerID}, pi)}, nil
	case "kimi":
		f := providerFragment("providers", object{"type": "openai", "base_url": base, "api_key": key})
		for _, m := range models {
			if m.Context <= 0 {
				continue
			}
			row := object{"provider": providerID, "model": m.ID, "max_context_size": m.Context, "display_name": m.Name}
			if m.Vision {
				row["capabilities"] = []string{"image_in"}
			}
			f = append(f, frag([]string{"models", providerID + "/" + m.ID}, row))
		}
		if len(f) == 1 {
			return nil, errors.New("Kimi needs a model with a known context limit")
		}
		return f, nil
	case "dsh":
		rows := []any{}
		for _, m := range models {
			row := object{"id": m.ID, "name": m.Name, "input": dshClientInput(m)}
			if m.Context > 0 {
				row["contextWindow"] = m.Context
			}
			efforts := object{}
			for _, e := range []string{"low", "medium", "high", "xhigh", "max"} {
				if hasEffort(m.Efforts, e) {
					efforts[e] = e
				}
			}
			if _, ok := efforts["max"]; !ok && hasEffort(m.Efforts, "ultra") {
				efforts["max"] = "ultra"
			}
			if len(efforts) > 0 {
				row["reasoningEfforts"] = efforts
			}
			rows = append(rows, row)
		}
		return []Fragment{frag([]string{"llm-pi-ai", "providers", providerID}, object{"displayName": "TaiRouter", "api": "openai-responses", "baseURL": base, "headers": object{"Authorization": "Bearer " + key}, "models": rows})}, nil
	case "mcode":
		catalog := object{}
		for _, m := range models {
			row := object{}
			if m.Context > 0 {
				row["limit"] = object{"context": m.Context}
			}
			if len(m.Efforts) > 0 {
				var efforts []string
				for _, e := range m.Efforts {
					if e != "none" {
						efforts = append(efforts, e)
					}
				}
				row["thinking"] = object{"effortOptions": efforts}
			}
			catalog[m.ID] = row
		}
		return providerFragment("custom_provider", object{"name": "TaiRouter", "kind": "custom", "enabled": true, "api": "anthropic-messages", "options": object{"apiKey": key, "baseURL": strings.TrimSuffix(base, "/v1"), "authMode": "api-key"}, "models": catalog}), nil
	case "zcode":
		if strings.HasSuffix(path, "provider_config.json") {
			if doc["schemaVersion"] != float64(1) {
				return nil, errors.New("ZCode provider store schema is not established")
			}
			ids := []string{}
			for _, m := range models {
				ids = append(ids, m.ID)
			}
			f := []Fragment{frag([]string{"config", "providerConfigRules", "providerRules", "[providerId=tairouter]"}, object{"providerId": providerID, "enabled": true, "providerName": "TaiRouter", "config": object{"group": "standard-personal", "access": object{"type": "api-key", "apiKey": key}, "api": object{"type": "openai-responses", "baseUrl": base}, "personalModelIds": ids, "modelOrder": ids}})}
			for _, m := range models {
				s, e := selector("modelId", m.ID)
				if e != nil {
					return nil, e
				}
				s = "[providerId=tairouter," + s[1:]
				props := object{}
				if m.Context > 0 {
					props["contextWindow"] = m.Context
				}
				if m.Vision {
					props["inputFormat"] = object{"supportsImage": true}
				}
				config := object{"properties": props}
				efforts := selectableEfforts(m)
				if len(efforts) > 0 {
					config["optionSpecs"] = object{"reasoningLevel": object{"values": efforts}}
				}
				if len(props) > 0 || len(efforts) > 0 {
					f = append(f, frag([]string{"config", "modelConfigRules", "providerModelRules", s}, object{"providerId": providerID, "modelId": m.ID, "config": config}))
				}
			}
			return f, nil
		}
		catalog := object{}
		for _, m := range models {
			row := object{"name": m.Name, "modalities": object{"input": clientInput(m, false), "output": []string{"text"}}}
			if m.Context > 0 {
				row["limit"] = object{"context": m.Context}
			}
			if efforts := selectableEfforts(m); len(efforts) > 0 {
				reasoning := object{"enabled": true, "variants": efforts}
				if m.DefaultReasoningEffort != nil && hasEffort(efforts, *m.DefaultReasoningEffort) {
					reasoning["defaultVariant"] = *m.DefaultReasoningEffort
				}
				row["reasoning"] = reasoning
			}
			catalog[m.ID] = row
		}
		return providerFragment("provider", object{"name": "TaiRouter", "kind": "openai", "enabled": true, "source": "custom", "options": object{"apiKey": key, "baseURL": base, "apiKeyRequired": true}, "models": catalog}), nil
	case "commandcode":
		root := "provider"
		if _, ok := doc[root]; !ok {
			if _, ok = doc["providers"]; ok {
				root = "providers"
			}
		}
		catalog := object{}
		for _, m := range models {
			row := object{}
			if m.Context > 0 {
				row["contextWindow"] = m.Context
			}
			var efforts []string
			for _, e := range m.Efforts {
				if hasEffort([]string{"low", "medium", "high", "xhigh", "max"}, e) {
					efforts = append(efforts, e)
				}
			}
			if len(efforts) > 0 {
				row["reasoningEfforts"] = efforts
			}
			catalog[m.ID] = row
		}
		return providerFragment(root, object{"name": "TaiRouter", "api": "openai-completions", "baseURL": base, "apiKey": false, "models": catalog}), nil
	case "droid":
		var f []Fragment
		for _, m := range models {
			s, e := selector("model", m.ID)
			if e != nil {
				return nil, e
			}
			label := "TaiRouter: " + m.Name
			if strings.ContainsAny(label, ",]") {
				return nil, errors.New("Droid display name cannot be addressed")
			}
			s = strings.TrimSuffix(s, "]") + ",displayName=" + label + "]"
			f = append(f, frag([]string{"customModels", s}, object{"model": m.ID, "displayName": label, "baseUrl": base, "provider": "generic-chat-completion-api", "noImageSupport": !m.Vision}))
		}
		return f, nil
	case "raycast":
		rows := []any{}
		for _, m := range models {
			tools := true
			if m.SupportsTools != nil {
				tools = *m.SupportsTools
			}
			row := object{"id": m.ID, "name": m.Name, "abilities": object{"temperature": object{"supported": len(m.Efforts) == 0}, "vision": object{"supported": m.Vision}, "system_message": object{"supported": true}, "tools": object{"supported": tools}, "reasoning_effort": object{"supported": len(m.Efforts) > 0}}}
			if m.Context > 0 {
				row["context"] = m.Context
			}
			rows = append(rows, row)
		}
		return []Fragment{frag([]string{"providers", "[id=tairouter]"}, object{"id": providerID, "name": "TaiRouter", "base_url": base, "models": rows})}, nil
	case "codex":
		return []Fragment{frag([]string{"model_providers", providerID}, object{"name": "TaiRouter", "base_url": base, "wire_api": "responses", "experimental_bearer_token": key}), frag([]string{"model_provider"}, providerID)}, nil
	case "claude":
		return []Fragment{frag([]string{"env", "ANTHROPIC_BASE_URL"}, strings.TrimSuffix(base, "/v1")), frag([]string{"env", "ANTHROPIC_AUTH_TOKEN"}, key)}, nil
	case "grok":
		f := []Fragment{frag([]string{"model_providers", providerID}, object{"base_url": base, "api_backend": "responses", "api_key": key})}
		taken := map[string]bool{}
		if existing, ok := doc["model"].(map[string]any); ok {
			for alias := range existing {
				taken[alias] = true
			}
		}
		for _, m := range models {
			stem := "aip-" + regexp.MustCompile(`[^A-Za-z0-9_-]`).ReplaceAllString(m.ID, "-")
			alias := stem
			for n := 2; taken[alias]; n++ {
				alias = fmt.Sprintf("%s-%d", stem, n)
			}
			taken[alias] = true
			row := object{"model_provider": providerID, "model": m.ID, "name": m.Name}
			if m.Context > 0 {
				row["context_window"] = m.Context
			}
			var efforts []string
			for _, e := range m.Efforts {
				if e != "ultra" {
					efforts = append(efforts, e)
				}
			}
			chosen := ""
			if len(efforts) > 0 {
				chosen = efforts[0]
				if hasEffort(efforts, "high") {
					chosen = "high"
				}
				if hasEffort(efforts, "medium") {
					chosen = "medium"
				}
				if m.DefaultReasoningEffort != nil && hasEffort(efforts, *m.DefaultReasoningEffort) {
					chosen = *m.DefaultReasoningEffort
				}
			}
			var options []any
			for _, effort := range m.Efforts {
				if effort == "ultra" {
					continue
				}
				options = append(options, object{"id": effort, "value": effort, "label": effort, "description": effort, "default": effort == chosen})
			}
			if len(options) > 0 {
				row["supports_reasoning_effort"] = true
				row["reasoning_effort"] = chosen
				row["reasoning_efforts"] = options
			}
			f = append(f, frag([]string{"model", alias}, row))
		}
		return f, nil
	case "cline":
		return nil, errors.New("Cline must use the paired-file builder")
	case "claude-desktop":
		return nil, errors.New("Claude Desktop profile activation and native secret store require dedicated ownership handling; configuration refused")
	}
	return nil, fmt.Errorf("no builder for client %s", id)
}
