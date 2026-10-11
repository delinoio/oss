// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Fixed candidates retain absence proof. User scopes name the Claude config
// directory itself, rather than letting the Worker infer HOME or CLAUDE_CONFIG_DIR.
func NativeConfigurationSourceFiles(harness Harness, scope NativeConfigurationScope) []string {
	if harness == Codex {
		return NativeConfigurationFiles(scope.Kind)
	}
	var files []string
	if scope.Kind == NativeConfigurationHome {
		files = []string{"settings.json", "CLAUDE.md"}
	} else {
		files = []string{".claude/settings.json", ".claude/settings.local.json", "CLAUDE.md", ".claude/CLAUDE.md", "CLAUDE.local.md", ".mcp.json"}
	}
	return append(files, scope.Files...)
}

func ClaudeConfigurationFile(kind NativeConfigurationScopeKind, file string) bool {
	if Text(file, "selected Claude file", 1024, true) != nil || path.IsAbs(file) || path.Clean(file) != file || strings.ContainsAny(file, "\\\x00\r\n") {
		return false
	}
	parts := strings.Split(file, "/")
	for _, part := range parts {
		if part == ".." || part == "." || part == "" || part == ".env" || part == ".credentials.json" || NativeConfigurationSensitiveKey(part) {
			return false
		}
	}
	prefix := ""
	if kind == NativeConfigurationProject {
		prefix = ".claude/"
	}
	relative, ok := strings.CutPrefix(file, prefix)
	if !ok {
		return false
	}
	switch {
	case strings.HasPrefix(relative, "rules/"):
		return len(strings.Split(relative, "/")) >= 2 && strings.HasSuffix(relative, ".md")
	case strings.HasPrefix(relative, "skills/"), strings.HasPrefix(relative, "plugins/"):
		return len(strings.Split(relative, "/")) >= 3
	default:
		return false
	}
}
func NativeConfigurationInstructionFile(harness Harness, kind NativeConfigurationScopeKind, file string) bool {
	if harness == Codex {
		return file == "AGENTS.md" || file == "AGENTS.override.md"
	}
	prefix := "rules/"
	if kind == NativeConfigurationProject {
		prefix = ".claude/rules/"
	}
	return file == "CLAUDE.md" || kind == NativeConfigurationProject && (file == ".claude/CLAUDE.md" || file == "CLAUDE.local.md") || ClaudeConfigurationFile(kind, file) && strings.HasPrefix(file, prefix)
}

var claudeConfigurationImportDirective = regexp.MustCompile(`(?m)(?:^|\s)@(?:[./~]|[A-Za-z0-9_-]+[./])`)

// Path-scoped/frontmatter rules and native @imports cannot become unconditional
// Agent instructions. Keep the exact bytes as an inert package for review.
func ClaudeConfigurationPlainInstruction(contents string) bool {
	return !strings.HasPrefix(strings.TrimSpace(contents), "---") && !claudeConfigurationImportDirective.MatchString(contents)
}

func NativeConfigurationSettingForHarness(harness Harness, name string, raw []byte) (HarnessSettings, error) {
	if harness == Codex {
		return NativeConfigurationSettingValue(name, raw)
	}
	settings := HarnessSettings{Version: 1}
	var value string
	if harness != ClaudeCode || Decode(raw, &value) != nil || NativeConfigurationSecret(value) {
		return settings, NativeConfigurationInvalid()
	}
	switch name {
	case "model":
		return NativeConfigurationSettingValue(name, raw)
	case "effort":
		if !slices.Contains([]string{"low", "medium", "high", "xhigh", "max"}, value) {
			return settings, NativeConfigurationInvalid()
		}
		settings.Effort = OverrideHarnessSetting(value)
	case "claude_permission":
		permission := ClaudePermissionMode(value)
		if !permission.Valid() {
			return settings, NativeConfigurationInvalid()
		}
		settings.ClaudePermission = OverrideHarnessSetting(permission)
	default:
		return settings, NativeConfigurationInvalid()
	}
	return settings, settings.Validate()
}

type ClaudeInertConfiguration struct {
	Version             uint32 `json:"version"`
	Category            string `json:"category"`
	ExecutionAuthority  bool   `json:"execution_authority"`
	CredentialAuthority bool   `json:"credential_authority"`
	Trust               bool   `json:"trust"`
	Contents            string `json:"contents"`
	ContentsDigest      string `json:"contents_digest"`
}

func ClaudeInertConfigurationContents(category, contents string) string {
	raw, _ := json.Marshal(ClaudeInertConfiguration{Version: 1, Category: category, Contents: contents, ContentsDigest: HarnessEvidenceDigest([]byte(contents))})
	return string(raw)
}
func NativeConfigurationInertEntry(harness Harness, name, contents string) bool {
	if harness == Codex {
		return slices.Contains([]string{"hooks", "plugins", "mcp_servers"}, name) && contents == `{"execution_authority":false,"credential_authority":false,"trust":false}`
	}
	var value ClaudeInertConfiguration
	return harness == ClaudeCode && Decode([]byte(contents), &value) == nil && value.Category == name && NativeConfigurationInertPackage(harness, contents)
}
func NativeConfigurationInertPackage(harness Harness, contents string) bool {
	if harness == Codex {
		return contents == `{"execution_authority":false,"credential_authority":false,"trust":false}`
	}
	var value ClaudeInertConfiguration
	return harness == ClaudeCode && Decode([]byte(contents), &value) == nil && Text(contents, "inert Claude envelope", MaxNativeConfigurationFileBytes, true) == nil && value.Version == 1 && slices.Contains([]string{"hooks", "plugins", "mcp_servers", "skills", "rules"}, value.Category) && !value.ExecutionAuthority && !value.CredentialAuthority && !value.Trust && Text(value.Contents, "inert Claude package", MaxNativeConfigurationFileBytes, true) == nil && !ClaudeConfigurationSensitive([]byte(value.Contents)) && HarnessEvidenceDigest([]byte(value.Contents)) == value.ContentsDigest
}

// Apply the same credential exclusion to Worker observations and portable packages.
var claudeCredentialURL = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^/\s]+@`)

func ClaudeConfigurationSensitive(raw []byte) bool {
	if NativeConfigurationSecret(string(raw)) || claudeCredentialURL.Match(raw) {
		return true
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	var sensitive func(any) bool
	sensitive = func(value any) bool {
		switch value := value.(type) {
		case map[string]any:
			for key, nested := range value {
				if key == "env" || NativeConfigurationSensitiveKey(key) || sensitive(nested) {
					return true
				}
			}
		case []any:
			for _, nested := range value {
				if sensitive(nested) {
					return true
				}
			}
		case string:
			if parsed, err := url.Parse(value); err == nil && parsed.User != nil {
				return true
			}
			return NativeConfigurationSecret(value)
		}
		return false
	}
	return sensitive(value)
}
