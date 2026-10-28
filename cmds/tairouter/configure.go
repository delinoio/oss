// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// PlanSettings reads only the resolved client's files. A prior record must bind
// to those exact files and still match its owned source before any replacement.
func PlanSettings(resolver Resolver, s State, id string, p Profile, key string, models []ClientModel) ([]Change, error) {
	switch id {
	case "codex":
		return PlanCodexNative(resolver, s, p, key, models)
	case "claude", "claude-desktop":
		return PlanClaudeNative(resolver, s, id, p, key, models)
	}
	models = compatibleClientModels(id, normalizedExportModels(models))
	if len(models) == 0 {
		return nil, errors.New("no compatible routed models are available for this client")
	}
	targets, e := resolvedTargets(resolver, s, id)
	if e != nil {
		return nil, e
	}
	expected, _ := json.Marshal(targets)
	checkTargets := func() error {
		current, e := resolvedTargets(resolver, s, id)
		if e != nil {
			return e
		}
		actual, _ := json.Marshal(current)
		if !bytes.Equal(actual, expected) {
			return errors.New("client profile selectors changed after preview")
		}
		return nil
	}
	var changes []Change
	for _, target := range targets {
		part, e := planSettingsTarget(resolver, s, id, target.Path, p, key, models, checkTargets)
		if e != nil {
			return nil, e
		}
		changes = append(changes, part...)
	}
	return changes, nil
}
func planSettingsTarget(resolver Resolver, s State, id, path string, p Profile, key string, models []ClientModel, checkTargets func() error) ([]Change, error) {
	spec, e := clientSpec(id)
	if e != nil {
		return nil, e
	}
	if previous, ok := s.Records[id]; ok && id != "aside" && id != "hermes" {
		if len(previous.Files) > 0 && previous.Files[0].Path != path {
			return nil, errors.New("client active path changed; restore recorded settings before switching")
		}
	}
	base := strings.TrimRight(p.Server, "/") + "/v1"
	remote := !spec.Loopback
	if spec.Loopback {
		base = fmt.Sprintf("http://127.0.0.1:%d/v1", p.Port)
		key = placeholder
	}
	paths := []string{path}
	if id == "claude-desktop" {
		paths = append(paths, filepath.Join(filepath.Dir(path), "_meta.json"))
	}
	if id == "cline" {
		paths = append(paths, filepath.Join(filepath.Dir(path), "models.json"))
	}
	changes := []Change{}
	for index, file := range paths {
		before, exists, e := readConfig(file)
		if e != nil {
			return nil, e
		}
		source := before
		for client, record := range s.Records {
			if client != id {
				for _, b := range record.Files {
					if b.Path == file {
						return nil, errors.New("another client owns this destination; disconnect it first")
					}
				}
			}
		}
		if record, ok := s.Records[id]; ok {
			matched := false
			for _, b := range record.Files {
				if b.Path == file {
					source, _, e = restoreOwned(id, b, before)
					if e != nil {
						return nil, e
					}
					matched = true
					break
				}
			}
			if !matched && id != "aside" && id != "hermes" {
				return nil, errors.New("client file has no matching ownership record")
			}
		}
		doc := object{}
		if len(bytes.TrimSpace(source)) > 0 {
			doc, e = parseDocument(source, spec.Format)
			if e != nil {
				return nil, e
			}
		}
		if e = clientPreflight(id, path, doc, base, models); e != nil {
			return nil, e
		}
		var fragments []Fragment
		if id == "claude-desktop" {
			if index == 0 {
				if len(source) > 0 {
					return nil, errors.New("unowned TaiRouter Desktop profile already exists")
				}
				fragments = []Fragment{frag([]string{"inferenceProvider"}, "gateway"), frag([]string{"inferenceCredentialKind"}, "static"), frag([]string{"inferenceGatewayBaseUrl"}, strings.TrimSuffix(base, "/v1")), frag([]string{"inferenceGatewayApiKey"}, key), frag([]string{"modelDiscoveryEnabled"}, true)}
			} else {
				if len(source) == 0 {
					source = []byte("{\"entries\":[]}\n")
				} else if _, ok := doc["entries"].([]any); !ok {
					return nil, errors.New("Claude Desktop metadata has no entries array")
				}
				fragments = []Fragment{frag([]string{"entries", "[id=tairouter]"}, object{"id": "tairouter", "name": "TaiRouter"})}
				source, e = jsonPatch(source, []string{"appliedId"}, "tairouter", true)
				if e != nil {
					return nil, e
				}
			}
		} else if id == "cline" {
			if len(source) == 0 {
				if index == 0 {
					source = []byte("{\"version\":1,\"modes\":{},\"providers\":{}}\n")
				} else {
					source = []byte("{\"version\":1,\"providers\":{}}\n")
				}
			} else if doc["version"] != float64(1) {
				return nil, errors.New("Cline schema version is not established")
			}
			connection := object{"baseUrl": base, "protocol": "openai-responses", "client": "openai"}
			if index == 0 {
				settings := object{"provider": providerID, "baseUrl": base, "protocol": "openai-responses", "client": "openai", "apiKey": key}
				fragments = providerFragment("providers", object{"settings": settings, "updatedAt": "1970-01-01T00:00:00.000Z", "tokenSource": "manual"})
				if _, owned := s.Records[id]; owned {
					preserveClineSelection(before, &fragments[0], models)
				}
			} else {
				catalog := object{}
				for _, m := range models {
					row := object{"name": m.Name}
					if len(m.InputModalities) > 0 || m.Vision {
						row["supportsVision"] = m.Vision
					}
					if len(m.InputModalities) > 0 {
						row["modalities"] = object{"input": clientInput(m, false), "output": []string{"text"}}
					}
					if m.Context > 0 {
						row["contextWindow"] = m.Context
					}
					catalog[m.ID] = row
				}
				connection["name"] = "TaiRouter"
				fragments = providerFragment("providers", object{"provider": connection, "models": catalog})
			}
		} else {
			builderPath := path
			if id == "zcode" && resolver.Env["ZCODE_PERSONAL_PROVIDER_CONFIG_FILE"] != "" {
				builderPath = "provider_config.json"
			}
			fragments, e = contributions(id, builderPath, base, key, models, doc)
			if e != nil {
				return nil, e
			}
		}
		if id == "dsh" && filepath.Base(path) == "cordis.patch.yml" {
			if _, ok := doc["$sequence"]; !ok {
				return nil, errors.New("DSH active profile patch must be a loader sequence")
			}
			fragments[0].Path = []string{"[id=llm-pi-ai]", "config", "providers", providerID}
		}
		if id == "codex" {
			source, e = tomlRootString(source, "model_provider", providerID)
			if e != nil {
				return nil, e
			}
			fragments = fragments[:len(fragments)-1]
		}
		after, e := Patch(source, spec.Format, fragments, false)
		if e != nil {
			return nil, e
		}
		fields := []string{}
		for _, f := range fragments {
			fields = append(fields, strings.Join(f.Path, "."))
		}
		if id == "claude-desktop" && index == 1 {
			fields = append(fields, "appliedId")
		}
		check := func() error {
			if e := checkTargets(); e != nil {
				return e
			}
			return clientPreflight(id, path, doc, base, models)
		}
		changes = append(changes, Change{Path: file, Before: before, After: after, Existed: exists, Fields: fields, RemoteKey: remote, Check: check, Locks: nativeLockPaths(id, file)})
	}
	return changes, nil
}
func clientPreflight(id, path string, doc object, base string, models []ClientModel) error {
	switch id {
	case "kilo":
		for _, name := range kiloCandidates {
			other := filepath.Join(filepath.Dir(path), name)
			if other == path {
				continue
			}
			b, ok, e := readConfig(other)
			if e != nil {
				return e
			}
			if ok {
				v, e := parseDocument(b, JSON5)
				if e != nil {
					return e
				}
				if p, ok := v["provider"].(map[string]any); ok && p[providerID] != nil {
					return errors.New("competing Kilo candidate owns an TaiRouter provider; preserve both files")
				}
			}
		}
	case "droid":
		for _, name := range []string{"config.json", "settings.local.json"} {
			b, ok, e := readConfig(filepath.Join(filepath.Dir(path), name))
			if e != nil {
				return e
			}
			if !ok {
				continue
			}
			v, e := parseDocument(b, JSON)
			if e != nil {
				return e
			}
			if name == "settings.local.json" && v["customModels"] != nil {
				return errors.New("Droid local settings override customModels")
			}
			if bytes.Contains(b, []byte(base)) || bytes.Contains(b, []byte("TaiRouter:")) {
				return errors.New("competing Droid legacy settings must be resolved first")
			}
			for _, m := range models {
				if bytes.Contains(b, []byte(m.ID)) {
					return errors.New("Droid legacy model collision")
				}
			}
		}
	case "workbuddy", "workbuddy-ai":
		if available, ok := doc["availableModels"].([]any); ok && len(available) > 0 {
			for _, m := range models {
				found := false
				for _, v := range available {
					if v == m.ID || v == "custom-local:"+m.ID {
						found = true
					}
				}
				if !found {
					return errors.New("WorkBuddy availableModels hides the proposed catalog")
				}
			}
		}
	case "qoder", "qoder-cn":
		if configs, ok := doc["modelConfigs"].(map[string]any); ok {
			if rows, ok := configs["customModels"].([]any); ok {
				for _, row := range rows {
					if r, ok := row.(map[string]any); ok {
						for _, m := range models {
							if r["model"] == m.ID || r["id"] == m.ID {
								return errors.New("Qoder legacy model collision; resolve it in Qoder first")
							}
						}
					}
				}
			}
		}
	case "codex":
		if owner, ok := doc["model_provider"].(string); ok && owner != "" && owner != "openai" {
			return errors.New("Codex has an existing selected provider; preserve its settings and restore it before configuring")
		}
	}
	return nil
}
