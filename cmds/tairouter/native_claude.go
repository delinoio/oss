// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
)

// PlanClaudeNative plans gateway configuration without writing files or starting
// a client. The caller must obtain both settings and remote-key consent through
// Disk.Apply. First-party TLS interception requires a separate trust lifecycle;
// gateway configuration must never install a proxy or enroll a certificate.
func PlanClaudeNative(resolver Resolver, state State, client string, profile Profile, key string, models []ClientModel) ([]Change, error) {
	if client != "claude" && client != "claude-desktop" {
		return nil, errors.New("unsupported native Claude client")
	}
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("Claude gateway requires a profile key")
	}
	path, err := resolver.Resolve(client)
	if err != nil {
		return nil, err
	}
	paths := []string{path}
	if client == "claude" {
		paths = append(paths, filepath.Join(filepath.Dir(path), "cache", "gateway-models.json"))
	} else {
		paths = append(paths, filepath.Join(filepath.Dir(path), "_meta.json"))
	}
	if record, ok := state.Records[client]; ok {
		for _, backup := range record.Files {
			found := false
			for _, target := range paths {
				if backup.Path == target {
					found = true
				}
			}
			if !found {
				return nil, errors.New("Claude configuration path changed; restore the recorded files first")
			}
		}
	}
	check := func() error {
		current, e := resolver.Resolve(client)
		if e != nil {
			return e
		}
		if current != path {
			return ErrConflict
		}
		return nil
	}
	var changes []Change
	base := strings.TrimRight(profile.Server, "/")
	for index, target := range paths {
		before, exists, e := readConfig(target)
		if e != nil {
			return nil, e
		}
		source := before
		owned := false
		for owner, record := range state.Records {
			for _, backup := range record.Files {
				if backup.Path != target {
					continue
				}
				if owner != client {
					return nil, errors.New("another client owns this Claude destination")
				}
				source, _, e = restoreOwned(client, backup, before)
				if e != nil {
					return nil, e
				}
				owned = true
			}
		}
		doc := object{}
		if len(bytes.TrimSpace(source)) > 0 {
			doc, e = parseDocument(source, JSON)
			if e != nil {
				return nil, e
			}
		}
		var fragments []Fragment
		replace := false
		remote := index == 0
		if client == "claude" && index == 0 {
			// A pre-existing vendor key must not follow the new gateway origin.
			// Leave it untouched and require the user to resolve its provenance.
			if env := mapAt(doc, "env"); env != nil {
				if value, present := env["ANTHROPIC_API_KEY"]; present && value != "" && value != nil {
					return nil, errors.New("Claude has an existing API key; resolve its gateway authorization before configuring")
				}
			}
			fragments = []Fragment{frag([]string{"env", "ANTHROPIC_BASE_URL"}, base), frag([]string{"env", "ANTHROPIC_AUTH_TOKEN"}, key)}
			if _, present := mapAt(doc, "env")["CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"]; !present {
				fragments = append(fragments, frag([]string{"env", "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"}, "1"))
			}
		} else if client == "claude" {
			// This is a disposable native cache, but its prior bytes still belong to
			// the user. Capture and restore them with the same transaction as settings.
			// The schema and usable-ID rule follow the pinned upstream gateway-cache.ts.
			rows := []any{}
			seen := map[string]bool{}
			for _, model := range models {
				id := strings.ToLower(model.ID)
				if !strings.Contains(id, "claude") && !strings.Contains(id, "anthropic") {
					continue
				}
				if seen[model.ID] {
					return nil, errors.New("duplicate Claude gateway model ID")
				}
				seen[model.ID] = true
				row := object{"id": model.ID}
				if model.Name != "" {
					row["display_name"] = model.Name
				}
				if model.Description != "" {
					row["description"] = model.Description
				}
				rows = append(rows, row)
			}
			fragments = []Fragment{frag([]string{"baseUrl"}, base), frag([]string{"fetchedAt"}, time.Now().UnixMilli()), frag([]string{"models"}, rows)}
			replace = true
		} else if index == 0 {
			if !owned && exists {
				return nil, errors.New("unowned TaiRouter Desktop profile already exists")
			}
			if len(bytes.TrimSpace(source)) > 0 {
				return nil, errors.New("TaiRouter Desktop profile has a foreign restoration baseline")
			}
			fragments = []Fragment{frag([]string{"inferenceProvider"}, "gateway"), frag([]string{"inferenceCredentialKind"}, "static"), frag([]string{"inferenceGatewayBaseUrl"}, base), frag([]string{"inferenceGatewayApiKey"}, key), frag([]string{"modelDiscoveryEnabled"}, true)}
			catalog, static, err := claudeDesktopCatalog(models)
			if err != nil {
				return nil, err
			}
			if static {
				fragments[len(fragments)-1] = frag([]string{"modelDiscoveryEnabled"}, false)
				fragments = append(fragments, frag([]string{"inferenceModels"}, catalog))
			}
		} else {
			if len(bytes.TrimSpace(source)) == 0 {
				source = []byte("{\"entries\":[]}\n")
				doc = object{"entries": []any{}}
			}
			entries, ok := doc["entries"].([]any)
			if !ok {
				return nil, errors.New("Claude Desktop metadata has no entries array")
			}
			seen := map[string]bool{}
			for _, entry := range entries {
				row, ok := entry.(map[string]any)
				if !ok {
					return nil, errors.New("invalid Claude Desktop metadata entry")
				}
				id, ok := row["id"].(string)
				name, named := row["name"].(string)
				if !ok || id == "" || !named || name == "" || seen[id] {
					return nil, errors.New("invalid or duplicate Claude Desktop profile identity")
				}
				seen[id] = true
				if id == providerID {
					return nil, errors.New("unowned TaiRouter Desktop metadata entry already exists")
				}
			}
			fragments = []Fragment{frag([]string{"entries", "[id=tairouter]"}, object{"id": providerID, "name": "TaiRouter"})}
			source, e = Patch(source, JSON, []Fragment{frag([]string{"appliedId"}, providerID)}, true)
			if e != nil {
				return nil, e
			}
		}
		after, e := Patch(source, JSON, fragments, replace)
		if e != nil {
			return nil, e
		}
		if len(after) > MaxConfigBytes {
			return nil, errors.New("Claude configuration exceeds the file size limit")
		}
		fields := []string{}
		for _, fragment := range fragments {
			fields = append(fields, strings.Join(fragment.Path, "."))
		}
		if client == "claude-desktop" && index == 1 {
			fields = append(fields, "appliedId")
		}
		changes = append(changes, Change{Path: target, Before: before, After: after, Existed: exists, Fields: fields, RemoteKey: remote, Check: check})
	}
	return changes, nil
}

var claudeDesktopID = regexp.MustCompile(`^claude-[a-z0-9-]+$`)

// The remote snapshot owns aliases and capabilities. Never derive an alias from
// a display label or infer family/1M support from an unrelated provider model.
func claudeDesktopCatalog(models []ClientModel) ([]any, bool, error) {
	// A non-nil empty snapshot is authoritative. A nil slice selects discovery
	// for callers that did not request a static snapshot.
	static := models != nil && len(models) == 0
	for _, model := range models {
		static = static || model.Desktop != nil
	}
	if !static {
		return nil, false, nil
	}
	rows := make([]any, 0, len(models))
	seen := map[string]bool{}
	defaults := map[string]bool{}
	for _, model := range models {
		row := model.Desktop
		if row == nil || !claudeDesktopID.MatchString(row.Name) || seen[row.Name] {
			return nil, false, errors.New("invalid, duplicate or incomplete Claude Desktop snapshot")
		}
		seen[row.Name] = true
		switch row.AnthropicFamilyTier {
		case "opus", "sonnet", "haiku", "fable":
		default:
			return nil, false, errors.New("invalid Claude Desktop model family")
		}
		if strings.ContainsAny(row.LabelOverride, "[]") || len(utf16.Encode([]rune(row.LabelOverride))) > 80 {
			return nil, false, errors.New("invalid Claude Desktop model label")
		}
		if row.Prefer1M != nil && *row.Prefer1M && (row.Supports1M == nil || !*row.Supports1M) {
			return nil, false, errors.New("Claude Desktop 1M preference lacks capability")
		}
		if row.IsFamilyDefault != nil && *row.IsFamilyDefault {
			if defaults[row.AnthropicFamilyTier] {
				return nil, false, errors.New("duplicate Claude Desktop family default")
			}
			defaults[row.AnthropicFamilyTier] = true
		}
		value := object{"name": row.Name, "labelOverride": row.LabelOverride, "anthropicFamilyTier": row.AnthropicFamilyTier}
		if row.IsFamilyDefault != nil {
			value["isFamilyDefault"] = *row.IsFamilyDefault
		}
		if row.Supports1M != nil {
			value["supports1m"] = *row.Supports1M
		}
		if row.Prefer1M != nil {
			value["prefer1m"] = *row.Prefer1M
		}
		rows = append(rows, value)
	}
	return rows, true, nil
}

// The loopback merge behavior below is adapted from opencodex's
// src/claude/intercept/settings-no-proxy.ts at revision
// 88aaa754566d119b6b8fc898f8fb47602387d82e, under the following notice:
//
// MIT License
// Copyright (c) 2026 opencodex contributors
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies
// of the Software, and to permit persons to whom the Software is furnished to do
// so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

// claudeLoopbackBypass adds only missing native loopback spellings. The settings
// transaction retains the original value; restoration never guesses ownership
// from a matching proxy URL or CA filename.
func claudeLoopbackBypass(value any) (string, error) {
	if value == nil {
		value = ""
	}
	current, ok := value.(string)
	if !ok {
		return "", errors.New("Claude NO_PROXY must be a string or null")
	}
	present := map[string]bool{}
	for _, entry := range strings.FieldsFunc(current, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == '\r' }) {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "*" {
			return current, nil
		}
		if entry == "[::1]" {
			entry = "::1"
		}
		present[entry] = true
	}
	missing := []string{}
	for _, entry := range []string{"127.0.0.1", "localhost", "::1", "0.0.0.0"} {
		if !present[entry] {
			missing = append(missing, entry)
		}
	}
	if len(missing) == 0 {
		return current, nil
	}
	prefix := strings.TrimRight(current, ", \t\n\r")
	if prefix != "" {
		prefix += ","
	}
	return prefix + strings.Join(missing, ","), nil
}

// PlanClaudeCompatibility requires a live, trusted, separately consented runtime.
// Credentials and model catalogs remain in the runtime; native account secrets
// are never inserted into gateway settings by this planner.
func PlanClaudeCompatibility(resolver Resolver, state State, profile Profile, key string, models []ClientModel, ready NativeRuntimeReady) ([]Change, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	if err := ready.Validate(); err != nil {
		return nil, err
	}
	if ready.Profile != profile.Name {
		return nil, errors.New("native runtime belongs to another profile")
	}
	if resolver.OS != "darwin" && resolver.OS != "windows" {
		return nil, errors.New("Claude compatibility requires macOS or Windows")
	}
	if _, err := NewNativeCompatibilityPolicy(ready.Mode, resolver.OS, ready.Build); err != nil {
		return nil, err
	}
	client := "claude"
	picker := false
	switch ready.Mode {
	case NativeCompatibilityClaudeFirstParty:
	case NativeCompatibilityClaudePicker:
		client = "claude-desktop"
		picker = true
	default:
		return nil, errors.New("runtime mode is not a Claude compatibility mode")
	}
	path, err := resolver.Resolve(client)
	if err != nil {
		return nil, err
	}
	if picker {
		path = filepath.Join(filepath.Dir(path), "tairouter-picker.json")
	}
	paths := []string{path}
	cacheDir := ""
	var observedCaches []string
	if picker {
		paths = append(paths, filepath.Join(filepath.Dir(path), "_meta.json"))
	} else {
		cacheDir = filepath.Join(filepath.Dir(path), "cache", "model-catalog")
		observedCaches, err = claudeServedCatalogPaths(cacheDir)
		if err != nil {
			return nil, err
		}
		paths = append(paths, observedCaches...)
		// Retain removed cache entries across refresh so their original restoration
		// baseline is not discarded merely because invalidation already succeeded.
		for _, b := range state.Records[client].Files {
			if filepath.Dir(b.Path) == cacheDir && strings.HasSuffix(filepath.Base(b.Path), "-cc.json") {
				found := false
				for _, p := range paths {
					found = found || p == b.Path
				}
				if !found {
					paths = append(paths, b.Path)
				}
			}
		}
	}
	for _, backup := range state.Records[client].Files {
		found := false
		for _, p := range paths {
			found = found || p == backup.Path
		}
		if !found {
			return nil, errors.New("restore the previous Claude mode before switching compatibility settings")
		}
	}
	var libraryCheck func() error
	if !picker {
		libraryCheck, err = claudeFirstPartyLibraryCheck(resolver)
		if err != nil {
			return nil, err
		}
	}
	check := func() error {
		if err := ready.Validate(); err != nil {
			return err
		}
		current, err := resolver.Resolve(client)
		if err != nil {
			return err
		}
		if picker {
			current = filepath.Join(filepath.Dir(current), "tairouter-picker.json")
		}
		if current != path {
			return ErrConflict
		}
		if cacheDir != "" {
			currentCaches, err := claudeServedCatalogPaths(cacheDir)
			if err != nil {
				return err
			}
			// Earlier removals in this same transaction are expected. A new
			// catalog would escape the preview, so refuse it. Per-file checks
			// still detect edits or disappearance of pending destinations.
			for _, currentCache := range currentCaches {
				found := false
				for _, observed := range observedCaches {
					found = found || currentCache == observed
				}
				if !found {
					return ErrConflict
				}
			}
		}
		if libraryCheck != nil {
			return libraryCheck()
		}
		return nil
	}
	changes := []Change{}
	for index, target := range paths {
		before, exists, err := readConfig(target)
		if err != nil {
			return nil, err
		}
		source := before
		owned := false
		for owner, record := range state.Records {
			for _, backup := range record.Files {
				if backup.Path != target {
					continue
				}
				if owner != client {
					return nil, errors.New("another client owns this compatibility destination")
				}
				source, _, err = restoreOwned(client, backup, before)
				if err != nil {
					return nil, err
				}
				owned = true
			}
		}
		if !picker && index > 0 {
			changes = append(changes, Change{Path: target, Before: before, Existed: exists, Remove: true, Fields: []string{"served Claude Code model catalog"}, Check: check})
			continue
		}
		doc := object{}
		if len(bytes.TrimSpace(source)) > 0 {
			doc, err = parseDocument(source, JSON)
			if err != nil {
				return nil, err
			}
		}
		fragments := []Fragment{}
		if !picker {
			env := mapAt(doc, "env")
			if value := env["ANTHROPIC_BASE_URL"]; value != nil && value != "" {
				return nil, errors.New("restore Claude gateway settings before enabling first-party mode")
			}
			bypass, err := claudeLoopbackBypass(env["NO_PROXY"])
			if err != nil {
				return nil, err
			}
			fragments = []Fragment{frag([]string{"env", "HTTPS_PROXY"}, ready.ProxyURL), frag([]string{"env", "NODE_EXTRA_CA_CERTS"}, ready.CAPath)}
			source, err = Patch(source, JSON, []Fragment{frag([]string{"env", "NO_PROXY"}, bypass)}, true)
			if err != nil {
				return nil, err
			}
		} else if index == 0 {
			if exists && !owned || len(bytes.TrimSpace(source)) > 0 {
				return nil, errors.New("unowned Claude picker profile already exists")
			}
			fragments = []Fragment{frag([]string{"egressProxyUrl"}, ready.ProxyURL)}
		} else {
			if len(bytes.TrimSpace(source)) == 0 {
				source = []byte("{\"entries\":[]}\n")
				doc = object{"entries": []any{}}
			}
			entries, ok := doc["entries"].([]any)
			if !ok {
				return nil, errors.New("Claude Desktop metadata has no entries array")
			}
			seen := map[string]bool{}
			for _, entry := range entries {
				row, ok := entry.(map[string]any)
				if !ok {
					return nil, errors.New("invalid Claude Desktop entry")
				}
				id, ok := row["id"].(string)
				_, named := row["name"].(string)
				if !ok || !named || id == "" || seen[id] || id == "tairouter-picker" {
					return nil, errors.New("invalid or foreign Claude picker metadata")
				}
				seen[id] = true
			}
			fragments = []Fragment{frag([]string{"entries", "[id=tairouter-picker]"}, object{"id": "tairouter-picker", "name": "TaiRouter Picker"})}
			source, err = Patch(source, JSON, []Fragment{frag([]string{"appliedId"}, "tairouter-picker")}, true)
			if err != nil {
				return nil, err
			}
		}
		after, err := Patch(source, JSON, fragments, false)
		if err != nil {
			return nil, err
		}
		if len(after) > MaxConfigBytes {
			return nil, errors.New("Claude compatibility configuration exceeds the size limit")
		}
		fields := []string{}
		for _, f := range fragments {
			fields = append(fields, strings.Join(f.Path, "."))
		}
		if !picker {
			fields = append(fields, "env.NO_PROXY")
		} else if index == 1 {
			fields = append(fields, "appliedId")
		}
		changes = append(changes, Change{Path: target, Before: before, After: after, Existed: exists, Fields: fields, Check: check})
	}
	return changes, nil
}

func claudeServedCatalogPaths(dir string) ([]string, error) {
	if err := safeParents(dir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, errors.New("cannot inspect Claude served catalog cache")
	}
	paths := []string{}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "-cc.json") {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	return paths, nil
}

// First-party mode keeps the standard Desktop library. Do not silently replace
// a gateway or another proxy selected by the user in that separate application.
func claudeFirstPartyLibraryCheck(resolver Resolver) (func() error, error) {
	path, err := resolver.Resolve("claude-desktop")
	if err != nil {
		return nil, err
	}
	meta := filepath.Join(filepath.Dir(path), "_meta.json")
	before, exists, err := readConfig(meta)
	if err != nil {
		return nil, err
	}
	checks := []func() error{compareFile(meta, before, exists)}
	if exists {
		doc, err := parseDocument(before, JSON)
		if err != nil {
			return nil, err
		}
		if selected, ok := doc["appliedId"].(string); ok && selected != "" {
			if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`).MatchString(selected) {
				return nil, errors.New("invalid selected Claude Desktop profile")
			}
			selectedPath := filepath.Join(filepath.Dir(path), selected+".json")
			contents, present, err := readConfig(selectedPath)
			if err != nil {
				return nil, err
			}
			if !present {
				return nil, errors.New("selected Claude Desktop profile is missing")
			}
			selectedDoc, err := parseDocument(contents, JSON)
			if err != nil {
				return nil, err
			}
			if selectedDoc["inferenceProvider"] != nil || selectedDoc["egressProxyUrl"] != nil {
				return nil, errors.New("restore the selected Desktop gateway or proxy before first-party mode")
			}
			checks = append(checks, compareFile(selectedPath, contents, present))
		} else if doc["appliedId"] != nil {
			return nil, errors.New("invalid selected Claude Desktop profile")
		}
	}
	return func() error {
		for _, check := range checks {
			if err := check(); err != nil {
				return err
			}
		}
		return nil
	}, nil
}
