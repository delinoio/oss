// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

const codexCatalogName = "tairouter-catalog.json"

// PlanCodexNative only reads settings. Disk.Apply owns preview confirmation,
// separate remote-key consent, protected backups, and the atomic journal.
// Wire field names follow opencodex 88aaa754566d119b6b8fc898f8fb47602387d82e;
// this is an independent implementation, without imported provider execution.
func PlanCodexNative(resolver Resolver, state State, profile Profile, key string, models []ClientModel) ([]Change, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("Codex requires a remote API key")
	}
	path, err := resolver.Resolve("codex")
	if err != nil {
		return nil, err
	}
	home := filepath.Dir(path)
	catalogPath := filepath.Join(home, codexCatalogName)
	cachePath := filepath.Join(home, "models_cache.json")
	paths := []string{path, catalogPath, cachePath}
	if record, ok := state.Records["codex"]; ok {
		for _, backup := range record.Files {
			if backup.Path != path && backup.Path != catalogPath && backup.Path != cachePath {
				return nil, errors.New("Codex home changed; restore recorded settings before switching")
			}
		}
	}
	var changes []Change
	sources := make([][]byte, len(paths))
	for i, file := range paths {
		before, existed, err := readConfig(file)
		if err != nil {
			return nil, err
		}
		source := before
		owned := false
		for client, record := range state.Records {
			for _, backup := range record.Files {
				if backup.Path != file {
					continue
				}
				if client != "codex" {
					return nil, errors.New("another client owns this Codex destination")
				}
				source, _, err = restoreOwned("codex", backup, before)
				if err != nil {
					return nil, err
				}
				owned = true
			}
		}
		if i == 1 && existed && !owned {
			return nil, errors.New("unowned TaiRouter Codex catalog already exists")
		}
		sources[i] = source
		changes = append(changes, Change{Path: file, Before: before, Existed: existed})
	}
	doc, err := parseDocument(sources[0], TOML)
	if err != nil {
		return nil, err
	}
	if value, ok := doc["model_provider"]; ok && value != "openai" && value != "" {
		return nil, errors.New("Codex selected provider is externally owned; restore it before configuring")
	}
	if mapAt(doc, "model_providers", "openai") != nil {
		return nil, errors.New("Codex openai provider table is externally owned; preserve it before configuring")
	}
	// A selected native profile can override the root route and catalog. Refuse
	// ambiguous routing rather than advertise a connection that Codex will ignore.
	if selected, ok := doc["profile"].(string); ok && selected != "" {
		active := mapAt(doc, "profiles", selected)
		for _, field := range []string{"model_provider", "model_catalog_json", "openai_base_url"} {
			if _, exists := active[field]; exists {
				return nil, errors.New("active Codex profile overrides routing or catalog; resolve its selection first")
			}
		}
	}
	// Never consume legacy opencodex state implicitly. A user-selected native
	// catalog is read-only input and remains at its original path for restoration.
	var native []any
	var dependencyCheck func() error
	if selected, ok := doc["model_catalog_json"]; ok {
		value, valid := selected.(string)
		if !valid || strings.TrimSpace(value) == "" {
			return nil, errors.New("invalid Codex catalog selection")
		}
		originalPath := value
		if strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
			originalPath = filepath.Join(resolver.Home, value[2:])
		} else if !filepath.IsAbs(value) {
			originalPath = filepath.Join(home, value)
		}
		originalPath = filepath.Clean(originalPath)
		if originalPath == catalogPath || originalPath == path || originalPath == cachePath {
			return nil, errors.New("Codex catalog selection conflicts with managed destinations")
		}
		data, exists, err := readNativeCatalog(originalPath)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, errors.New("selected Codex catalog is missing; restore it before configuring")
		}
		native, err = codexCatalogRows(data)
		if err != nil {
			return nil, err
		}
		dependencyCheck = func() error {
			current, exists, err := readNativeCatalog(originalPath)
			if err != nil {
				return err
			}
			if !exists || !bytes.Equal(current, data) {
				return ErrConflict
			}
			return nil
		}
	} else if len(bytes.TrimSpace(sources[2])) != 0 {
		native, err = codexCatalogRows(sources[2])
		if err != nil {
			return nil, err
		}
	}
	rows, err := codexMergeRows(native, models)
	if err != nil {
		return nil, err
	}
	catalog, err := json.MarshalIndent(object{"models": rows}, "", "  ")
	if err != nil {
		return nil, errors.New("cannot encode Codex catalog")
	}
	changes[1].After = append(catalog, '\n')
	changes[1].Fields = []string{"models"}
	// The native cache must be stale so Codex reloads capability metadata. Keep
	// unrelated cache keys and the original bytes in the protected recovery record.
	cacheSource := sources[2]
	if len(bytes.TrimSpace(cacheSource)) == 0 {
		cacheSource = []byte("{}\n")
	}
	changes[2].After, err = Patch(cacheSource, JSON, []Fragment{frag([]string{"models"}, rows), frag([]string{"fetched_at"}, "2000-01-01T00:00:00Z"), frag([]string{"client_version"}, "0.0.0")}, true)
	if err != nil {
		return nil, err
	}
	changes[2].Fields = []string{"models", "fetched_at", "client_version"}
	config, err := tomlRootString(sources[0], "model_catalog_json", catalogPath)
	if err != nil {
		return nil, err
	}
	// Override the built-in openai entry without changing its identifier. Existing
	// native history stays native; no SQLite, authentication, or history migration.
	provider := object{"name": "TaiRouter", "base_url": strings.TrimRight(profile.Server, "/") + "/v1", "wire_api": "responses", "experimental_bearer_token": key, "requires_openai_auth": false}
	for _, model := range models {
		if model.CodexCatalog["supports_websockets"] == true {
			provider["supports_websockets"] = true
		}
	}
	config, err = Patch(config, TOML, []Fragment{frag([]string{"model_providers", "openai"}, provider)}, false)
	if err != nil {
		return nil, err
	}
	changes[0].After = config
	changes[0].RemoteKey = true
	changes[0].Fields = []string{"model_catalog_json", "model_providers.openai"}
	for i := range changes {
		if len(changes[i].After) > configSizeLimit(changes[i].Path) {
			return nil, errors.New("Codex catalog or settings exceed the destination size limit")
		}
		changes[i].Check = func() error {
			current, err := resolver.Resolve("codex")
			if err != nil {
				return err
			}
			if current != path {
				return errors.New("Codex home changed after preview")
			}
			if dependencyCheck != nil {
				return dependencyCheck()
			}
			return nil
		}
	}
	return changes, nil
}

func codexCatalogRows(data []byte) ([]any, error) {
	parser := scanner{b: data}
	if _, err := parser.value(); err != nil {
		return nil, errors.New("ambiguous native Codex catalog")
	}
	var value any
	if json.Unmarshal(data, &value) != nil {
		return nil, errors.New("invalid native Codex catalog")
	}
	if wrapper, ok := value.(map[string]any); ok {
		value = wrapper["models"]
	}
	rows, ok := value.([]any)
	if !ok {
		return nil, errors.New("Codex catalog must contain a models array")
	}
	seen := map[string]bool{}
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("invalid Codex catalog row")
		}
		slug, ok := row["slug"].(string)
		if !ok || strings.TrimSpace(slug) == "" || seen[slug] {
			return nil, errors.New("invalid or duplicate Codex catalog slug")
		}
		seen[slug] = true
	}
	return rows, nil
}

func codexMergeRows(native []any, models []ClientModel) ([]any, error) {
	rows := append([]any{}, native...)
	positions := map[string]int{}
	for i, raw := range rows {
		positions[raw.(map[string]any)["slug"].(string)] = i
	}
	seen := map[string]bool{}
	for _, model := range models {
		if strings.TrimSpace(model.ID) == "" || seen[model.ID] || strings.ContainsAny(model.ID, "\x00\r\n") {
			return nil, errors.New("invalid or duplicate Codex model identifier")
		}
		seen[model.ID] = true
		row, err := codexModelRow(model)
		if err != nil {
			return nil, err
		}
		if index, exists := positions[model.ID]; exists {
			rows[index] = row
		} else {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func codexModelRow(model ClientModel) (object, error) {
	if model.CodexCatalog != nil {
		// The authenticated service owns exact provider/account capabilities.
		// Copy the row without synthesizing effort, tier, or native entitlement.
		data, err := json.Marshal(model.CodexCatalog)
		if err != nil || len(data) > MaxNativeCatalogBytes {
			return nil, errors.New("invalid service Codex catalog row")
		}
		var row object
		if json.Unmarshal(data, &row) != nil || row["slug"] != model.ID {
			return nil, errors.New("service Codex catalog slug does not match the authorized model")
		}
		return row, nil
	}
	name := model.Name
	if name == "" {
		name = model.ID
	}
	context := model.Context
	if context <= 0 {
		context = 128000
	}
	modalities := []string{"text"}
	if model.Vision {
		modalities = append(modalities, "image")
	}
	levels := []any{}
	seen := map[string]bool{}
	for _, effort := range model.Efforts {
		switch effort {
		case "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
		default:
			return nil, errors.New("unsupported Codex reasoning effort")
		}
		if seen[effort] {
			continue
		}
		seen[effort] = true
		levels = append(levels, object{"effort": effort, "description": effort})
	}
	row := object{"slug": model.ID, "display_name": name, "description": "Routed via TaiRouter", "visibility": "list", "supported_in_api": true, "priority": 0, "shell_type": "unified_exec", "base_instructions": "You are a helpful coding assistant.", "supported_reasoning_levels": levels, "supports_reasoning_summaries": false, "default_reasoning_summary": "none", "support_verbosity": false, "supports_parallel_tool_calls": false, "supports_image_detail_original": false, "experimental_supported_tools": []any{}, "input_modalities": modalities, "context_window": context, "max_context_window": context, "effective_context_window_percent": 95, "comp_hash": nil, "apply_patch_tool_type": "freeform", "truncation_policy": object{"mode": "tokens", "limit": 10000}, "node_repl_disabled": true, "node_repl_auto_review_required": false, "include_plugin_usage_instructions": false, "include_apps_usage_instructions": false}
	if len(model.Efforts) > 0 {
		row["default_reasoning_level"] = model.Efforts[0]
		if seen["medium"] {
			row["default_reasoning_level"] = "medium"
		}
	}
	if model.Output > 0 {
		row["max_output_tokens"] = model.Output
	}
	return row, nil
}

// restoreCodexNative restores independent source spans after the generic fast
// path fails. Cache timestamps are native derived fields; model rows are not.
func restoreCodexNative(backup Backup, current []byte) ([]byte, bool, error) {
	switch filepath.Base(backup.Path) {
	case "config.toml":
		after, err := parseDocument(backup.After, TOML)
		if err != nil {
			return nil, false, ErrConflict
		}
		catalog, ok := after["model_catalog_json"].(string)
		if !ok {
			return nil, false, ErrConflict
		}
		intermediate, err := tomlRootString(backup.Before, "model_catalog_json", catalog)
		if err != nil {
			return nil, false, ErrConflict
		}
		withoutProvider, err := restoreBytes(Backup{Before: intermediate, After: backup.After}, current)
		if err != nil {
			return nil, false, err
		}
		result, err := restoreBytes(Backup{Before: backup.Before, After: intermediate}, withoutProvider)
		if err != nil {
			return nil, false, err
		}
		// A newly created file may now contain user settings. Keep those settings.
		remainder, err := parseDocument(result, TOML)
		if err != nil {
			return nil, false, err
		}
		return result, !backup.Existed && len(remainder) == 0, nil
	case "models_cache.json":
		expected, err := parseDocument(backup.After, JSON)
		if err != nil {
			return nil, false, ErrConflict
		}
		actual, err := parseDocument(current, JSON)
		if err != nil || !semanticEqual(expected["models"], actual["models"]) {
			return nil, false, ErrConflict
		}
		result := current
		for _, field := range []string{"models", "fetched_at", "client_version"} {
			path := []string{field}
			var original *jsonSpan
			if len(bytes.TrimSpace(backup.Before)) > 0 {
				original, err = jsonNodeAt(backup.Before, path)
				if err != nil {
					return nil, false, err
				}
			}
			if original != nil {
				var value any
				if json.Unmarshal(backup.Before[original.start:original.end], &value) != nil {
					return nil, false, ErrConflict
				}
				result, err = jsonPatch(result, path, value, true)
			} else {
				node, nodeErr := jsonNodeAt(result, path)
				if nodeErr != nil {
					return nil, false, nodeErr
				}
				if node != nil {
					result, err = removeIntroducedJSON(result, backup.Before, path)
				}
			}
			if err != nil {
				return nil, false, err
			}
		}
		remainder, err := parseDocument(result, JSON)
		if err != nil {
			return nil, false, err
		}
		return result, !backup.Existed && len(remainder) == 0, nil
	default:
		return nil, false, ErrConflict
	}
}
