// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func inspectClaudeConfiguration(ctx context.Context, selection domain.NativeConfigurationPreviewSelection) (domain.NativeConfigurationObservation, error) {
	result := domain.NativeConfigurationObservation{Version: 1, Sources: []domain.NativeConfigurationSource{}, Entries: []domain.NativeConfigurationEntry{}}
	if selection.Validate() != nil || selection.Harness != domain.ClaudeCode {
		return result, domain.NativeConfigurationInvalid()
	}
	total := 0
	lastProject := ""
	for index, scope := range selection.Scopes {
		if ctx.Err() != nil {
			return result, domain.Fail(domain.Canceled, "Native configuration inspection was canceled.", "Create a new preview if needed.")
		}
		if !filepath.IsAbs(scope.Path) || filepath.Clean(scope.Path) != scope.Path {
			return result, domain.NativeConfigurationInvalid()
		}
		canonical, err := filepath.EvalSymlinks(scope.Path)
		if err != nil || canonical != scope.Path {
			return result, domain.NativeConfigurationInvalid()
		}
		if scope.Kind == domain.NativeConfigurationProject {
			if lastProject != "" {
				relative, err := filepath.Rel(lastProject, scope.Path)
				if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
					return result, domain.NativeConfigurationInvalid()
				}
			}
			lastProject = scope.Path
		}
		root, err := os.OpenRoot(scope.Path)
		if err != nil {
			return result, nativeConfigurationReadFailure()
		}
		// Every named file is read exactly once. No @import, manifest path,
		// plugin cache, rules directory or skill can add another source.
		for _, file := range domain.NativeConfigurationSourceFiles(selection.Harness, scope) {
			raw, present, err := readClaudeConfigurationFile(root, file)
			if err != nil {
				root.Close()
				return result, err
			}
			total += len(raw)
			if total > domain.MaxNativeConfigurationSourceBytes {
				root.Close()
				return result, nativeConfigurationReadFailure()
			}
			digest := domain.HarnessEvidenceDigest(raw)
			result.Sources = append(result.Sources, domain.NativeConfigurationSource{ScopeIndex: uint32(index), File: file, Present: present, Digest: digest})
			if !present || len(strings.TrimSpace(string(raw))) == 0 {
				continue
			}
			if err := appendClaudeConfigurationFile(&result, uint32(index), scope.Kind, file, digest, raw); err != nil {
				root.Close()
				return result, err
			}
		}
		root.Close()
	}
	if err := result.Validate(selection); err != nil {
		return result, err
	}
	return result, nil
}
func readClaudeConfigurationFile(root *os.Root, file string) ([]byte, bool, error) {
	// os.Root contains traversal, while per-component lstat excludes in-root
	// symlinks too. A plugin/rule selector never grants linked-file authority.
	parts := strings.Split(file, "/")
	for n := 1; n < len(parts); n++ {
		info, err := root.Lstat(strings.Join(parts[:n], "/"))
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, false, nativeConfigurationReadFailure()
		}
	}
	return readNativeConfigurationFile(root, file)
}
func appendClaudeConfigurationFile(result *domain.NativeConfigurationObservation, index uint32, kind domain.NativeConfigurationScopeKind, file, digest string, raw []byte) error {
	entry := domain.NativeConfigurationEntry{ScopeIndex: index, File: file, SourceDigest: digest, Kind: domain.NativeConfigurationUnsupported, Name: "unsupported"}
	if !utf8.Valid(raw) {
		appendNativeConfigurationEntry(result, entry)
		return nil
	}
	settings := file == "settings.json" || file == ".claude/settings.json" || file == ".claude/settings.local.json"
	if settings {
		var table map[string]json.RawMessage
		if domain.Decode(raw, &table) != nil || table == nil {
			return nativeConfigurationReadFailure()
		}
		keys := make([]string, 0, len(table))
		for key := range table {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			selected := entry
			switch key {
			case "model", "effortLevel":
				name := key
				if key == "effortLevel" {
					name = "effort"
				}
				if _, err := domain.NativeConfigurationSettingForHarness(domain.ClaudeCode, name, table[key]); err == nil {
					selected.Kind = domain.NativeConfigurationSetting
					selected.Name = name
					selected.Value = table[key]
				} else if domain.ClaudeConfigurationSensitive(table[key]) {
					selected.Kind = domain.NativeConfigurationExcluded
					selected.Name = "excluded"
				}
			case "permissions":
				var permissions map[string]json.RawMessage
				if domain.Decode(table[key], &permissions) != nil || permissions == nil {
					break
				}
				if mode, ok := permissions["defaultMode"]; ok {
					if _, err := domain.NativeConfigurationSettingForHarness(domain.ClaudeCode, "claude_permission", mode); err == nil {
						selected.Kind = domain.NativeConfigurationSetting
						selected.Name = "claude_permission"
						selected.Value = mode
					}
				}
				if selected.Kind != domain.NativeConfigurationSetting && domain.ClaudeConfigurationSensitive(table[key]) {
					selected.Kind = domain.NativeConfigurationExcluded
					selected.Name = "excluded"
				}
				// Tool rules, extra directories and managed restrictions are
				// not translated into a native mode or granted runtime trust.
				appendNativeConfigurationEntry(result, selected)
				if len(permissions) > 1 && selected.Kind == domain.NativeConfigurationSetting {
					extra := entry
					if domain.ClaudeConfigurationSensitive(table[key]) {
						extra.Kind = domain.NativeConfigurationExcluded
						extra.Name = "excluded"
					}
					appendNativeConfigurationEntry(result, extra)
				}
				continue
			case "hooks", "enabledPlugins", "mcpServers":
				category := map[string]string{"hooks": "hooks", "enabledPlugins": "plugins", "mcpServers": "mcp_servers"}[key]
				selected = claudeInertEntry(selected, category, table[key])
			default:
				if domain.NativeConfigurationSensitiveKey(key) || key == "env" || domain.ClaudeConfigurationSensitive(table[key]) {
					selected.Kind = domain.NativeConfigurationExcluded
					selected.Name = "excluded"
				}
			}
			appendNativeConfigurationEntry(result, selected)
		}
		return nil
	}
	if file == ".mcp.json" {
		var servers map[string]json.RawMessage
		if domain.Decode(raw, &servers) != nil || servers == nil {
			return nativeConfigurationReadFailure()
		}
		entry = claudeInertEntry(entry, "mcp_servers", raw)
	} else if domain.NativeConfigurationInstructionFile(domain.ClaudeCode, kind, file) {
		if domain.ClaudeConfigurationSensitive(raw) {
			entry.Kind = domain.NativeConfigurationExcluded
			entry.Name = "excluded"
		} else if domain.ClaudeConfigurationPlainInstruction(string(raw)) {
			entry.Kind = domain.NativeConfigurationInstruction
			entry.Name = "instructions"
			entry.Contents = string(raw)
		} else {
			entry = claudeInertEntry(entry, "rules", raw)
		}
	} else if strings.HasPrefix(strings.TrimPrefix(file, ".claude/"), "skills/") {
		entry = claudeInertEntry(entry, "skills", raw)
	} else if strings.HasPrefix(strings.TrimPrefix(file, ".claude/"), "plugins/") {
		entry = claudeInertEntry(entry, "plugins", raw)
	}
	appendNativeConfigurationEntry(result, entry)
	return nil
}
func claudeInertEntry(entry domain.NativeConfigurationEntry, category string, raw []byte) domain.NativeConfigurationEntry {
	if domain.ClaudeConfigurationSensitive(raw) {
		entry.Kind = domain.NativeConfigurationExcluded
		entry.Name = "excluded"
		return entry
	}
	contents := domain.ClaudeInertConfigurationContents(category, string(raw))
	if domain.Text(contents, "inert Claude envelope", domain.MaxNativeConfigurationFileBytes, true) != nil {
		return entry
	}
	entry.Kind = domain.NativeConfigurationInertExtension
	entry.Name = category
	entry.Contents = contents
	return entry
}
