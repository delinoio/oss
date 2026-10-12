// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const NativeConfigurationImportV1 WorkerCapability = "native-configuration-import-v1"
const NativeConfigurationPreviewJob JobType = "native-configuration-preview"
const NativeConfigurationApplyJob JobType = "native-configuration-apply"
const MaxNativeConfigurationSelectionBytes = 64 << 10
const MaxNativeConfigurationObservationBytes = 2 << 20
const MaxNativeConfigurationSourceBytes = 512 << 10
const MaxNativeConfigurationFileBytes = 128 << 10

type NativeConfigurationScopeKind string

const (
	NativeConfigurationHome    NativeConfigurationScopeKind = "home"
	NativeConfigurationProject NativeConfigurationScopeKind = "project"
)

type NativeConfigurationScope struct {
	Kind NativeConfigurationScopeKind `json:"kind"`
	Path string                       `json:"path"`
	// Only an explicitly named profile is inspected; profile selectors in native
	// files never expand the selected read set.
	Profile  string                                `json:"profile,omitempty"`
	Packages []NativeConfigurationPackageSelection `json:"packages,omitempty"`
}
type NativeConfigurationTargetKind string

const (
	NativeConfigurationServerDefaults  NativeConfigurationTargetKind = "server-defaults"
	NativeConfigurationProjectDefaults NativeConfigurationTargetKind = "project"
	NativeConfigurationAgentWorker     NativeConfigurationTargetKind = "agent-worker"
)

type NativeConfigurationTarget struct {
	Kind     NativeConfigurationTargetKind `json:"kind"`
	ID       ID                            `json:"id"`
	Revision uint64                        `json:"revision,string"`
	Scope    HarnessDefaultScope           `json:"scope"`
	// Agent model selection changes exactly this route, never every source route.
	RouteIndex *uint32 `json:"route_index,omitempty"`
}

func (t NativeConfigurationTarget) Validate(h Harness) error {
	if t.ID.Validate() != nil || t.Revision == 0 || t.Scope.Validate() != nil || t.Scope.Source == HarnessAnySource || t.Scope.Source == HarnessAPISource && (t.Scope.ProviderID == "" || t.Scope.APIProtocol == "") || t.Scope.Source == HarnessSubscriptionSource && t.Scope.SubscriptionService == "" || t.Scope.Harness != h || !slices.Contains([]NativeConfigurationTargetKind{NativeConfigurationServerDefaults, NativeConfigurationProjectDefaults, NativeConfigurationAgentWorker}, t.Kind) || t.Kind != NativeConfigurationAgentWorker && t.RouteIndex != nil || t.Kind == NativeConfigurationAgentWorker && t.RouteIndex == nil {
		return NativeConfigurationInvalid()
	}
	return nil
}
func (t NativeConfigurationTarget) EntityKind() Kind {
	switch t.Kind {
	case NativeConfigurationServerDefaults:
		return SettingsKind
	case NativeConfigurationProjectDefaults:
		return ProjectKind
	default:
		return AgentKind
	}
}

type NativeConfigurationPreviewSelection struct {
	Version         uint32                     `json:"version"`
	Harness         Harness                    `json:"harness"`
	MachineID       ID                         `json:"machine_id"`
	MachineRevision uint64                     `json:"machine_revision,string"`
	Scopes          []NativeConfigurationScope `json:"scopes"`
	Target          NativeConfigurationTarget  `json:"target"`
}

func (s NativeConfigurationPreviewSelection) Validate() error {
	if s.Version != 1 || s.Harness != Codex || s.MachineID.Validate() != nil || s.MachineRevision == 0 || len(s.Scopes) == 0 || len(s.Scopes) > 16 || s.Target.Validate(s.Harness) != nil {
		return NativeConfigurationInvalid()
	}
	seen := map[string]bool{}
	projectSeen := false
	for _, scope := range s.Scopes {
		if !slices.Contains([]NativeConfigurationScopeKind{NativeConfigurationHome, NativeConfigurationProject}, scope.Kind) || Text(scope.Path, "selected source scope", 4096, true) != nil || strings.ContainsAny(scope.Path, "\x00\r\n") || scope.Profile != "" && !nativeConfigurationSelector.MatchString(scope.Profile) || seen[scope.Path] || scope.Kind == NativeConfigurationHome && (projectSeen || len(seen) != 0) {
			return NativeConfigurationInvalid()
		}
		seen[scope.Path] = true
		projectSeen = projectSeen || scope.Kind == NativeConfigurationProject
		if len(scope.Packages) > 16 {
			return NativeConfigurationInvalid()
		}
		selected := map[string]bool{}
		for _, pkg := range scope.Packages {
			if pkg.Validate() != nil {
				return NativeConfigurationInvalid()
			}
			for _, file := range pkg.Files {
				if selected[file] {
					return NativeConfigurationInvalid()
				}
				selected[file] = true
			}
		}
	}
	raw, _ := json.Marshal(s)
	if len(raw) > MaxNativeConfigurationSelectionBytes {
		return NativeConfigurationInvalid()
	}
	return nil
}

type NativeConfigurationApplySelection struct {
	Version            uint32   `json:"version"`
	PreviewDigest      string   `json:"preview_digest"`
	Entries            []string `json:"entries"`
	AttachInstructions bool     `json:"attach_instructions"`
}

func (s NativeConfigurationApplySelection) Validate() error {
	if s.AttachInstructions || s.Version != 1 || !nativeExecutableDigest.MatchString(s.PreviewDigest) || len(s.Entries) == 0 || len(s.Entries) > 128 {
		return NativeConfigurationInvalid()
	}
	seen := map[string]bool{}
	for _, id := range s.Entries {
		if !nativeExecutableDigest.MatchString(id) || seen[id] {
			return NativeConfigurationInvalid()
		}
		seen[id] = true
	}
	return nil
}

type NativeConfigurationJobInput struct {
	Version        uint32                              `json:"version"`
	ServerID       ID                                  `json:"server_id"`
	Actor          Principal                           `json:"actor"`
	WorkerDeviceID ID                                  `json:"worker_device_id"`
	InstanceID     ID                                  `json:"instance_id"`
	PreviewJobID   ID                                  `json:"preview_job_id,omitempty"`
	Preview        NativeConfigurationPreviewSelection `json:"preview"`
	Selection      *NativeConfigurationApplySelection  `json:"selection,omitempty"`
}

func (i NativeConfigurationJobInput) Validate() error {
	if i.Version != 1 || i.ServerID.Validate() != nil || i.WorkerDeviceID.Validate() != nil || i.InstanceID.Validate() != nil || i.Preview.Validate() != nil || i.Actor.Type != OwnerDevice && i.Actor.Type != ClientDevice || i.Actor.Type == ClientDevice && i.Actor.DeviceID.Validate() != nil {
		return NativeConfigurationInvalid()
	}
	if i.Selection != nil {
		if i.PreviewJobID.Validate() != nil || i.Selection.Validate() != nil {
			return NativeConfigurationInvalid()
		}
	} else if i.PreviewJobID != "" {
		return NativeConfigurationInvalid()
	}
	return nil
}

type NativeConfigurationEntryKind string

const (
	NativeConfigurationSetting        NativeConfigurationEntryKind = "setting"
	NativeConfigurationInstruction    NativeConfigurationEntryKind = "instruction"
	NativeConfigurationInertExtension NativeConfigurationEntryKind = "inert-extension"
	NativeConfigurationUnsupported    NativeConfigurationEntryKind = "unsupported"
	NativeConfigurationExcluded       NativeConfigurationEntryKind = "excluded"
)

type NativeConfigurationSource struct {
	Shadowed   bool   `json:"shadowed,omitempty"`
	ScopeIndex uint32 `json:"scope_index"`
	File       string `json:"file"`
	Present    bool   `json:"present"`
	Digest     string `json:"digest"`
}
type NativeConfigurationEntry struct {
	Profile    string                       `json:"profile,omitempty"`
	ID         string                       `json:"id"`
	ScopeIndex uint32                       `json:"scope_index"`
	File       string                       `json:"file"`
	Kind       NativeConfigurationEntryKind `json:"kind"`
	// Names are closed setting/category names, never unknown native key text.
	Name         string                           `json:"name"`
	Value        json.RawMessage                  `json:"value,omitempty"`
	Contents     string                           `json:"contents,omitempty"`
	SourceDigest string                           `json:"source_digest"`
	Files        []NativeConfigurationPackageFile `json:"files,omitempty"`
	Reason       NativeConfigurationReason        `json:"reason,omitempty"`
}
type NativeConfigurationObservation struct {
	Version uint32                      `json:"version"`
	Sources []NativeConfigurationSource `json:"sources"`
	Roots   []string                    `json:"roots"`
	Entries []NativeConfigurationEntry  `json:"entries"`
}

var nativeConfigurationSelector = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$`)

func NativeConfigurationInvalid() *Error {
	return Fail(InvalidArgument, "Invalid native configuration import.", "Select explicit scopes, a current destination, and only compatible reviewed entries.")
}
func NativeConfigurationChanged() *Error {
	return Fail(Conflict, "The reviewed native configuration or destination changed.", "Create a new preview; no configuration was imported.")
}

var nativeConfigurationSecretAssignment = regexp.MustCompile(`(?i)(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|client[_ -]?secret|[a-z0-9_]*(?:password|authorization|credential|token|secret)[a-z0-9_]*)["' ]*\s*[:=]\s*[^\s]+`)

func NativeConfigurationSecret(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{"sk-", "ghp_", "gho_", "github_pat_", "xoxb-", "xoxp-", "akia", "bearer ", "-----begin encrypted private key", "-----begin private key", "-----begin rsa private key", "-----begin ec private key", "-----begin openssh private key"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return nativeConfigurationSecretAssignment.MatchString(text) || regexp.MustCompile(`(?i)https?://[^/\s:@]+:[^/\s@]+@`).MatchString(text)
}
func NativeConfigurationSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, marker := range []string{"auth", "api_key", "apikey", "api-key", "token", "secret", "password", "credential", "memory", "memories", "managed", "requirements"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func NativeConfigurationFiles(kind NativeConfigurationScopeKind) []string {
	if kind == NativeConfigurationHome {
		return []string{"config.toml", "AGENTS.override.md", "AGENTS.md"}
	}
	return []string{".codex/config.toml", "AGENTS.override.md", "AGENTS.md"}
}
func (o NativeConfigurationObservation) Validate(s NativeConfigurationPreviewSelection) error {
	raw, err := json.Marshal(o)
	if err != nil || len(raw) > MaxNativeConfigurationObservationBytes || o.Version != 1 || len(o.Roots) != len(s.Scopes) || len(o.Sources) != NativeConfigurationSourceCount(s) || o.Entries == nil || len(o.Entries) > 128 {
		return NativeConfigurationInvalid()
	}
	sources := map[string]NativeConfigurationSource{}
	for index, digest := range o.Roots {
		if !nativeExecutableDigest.MatchString(digest) {
			return NativeConfigurationInvalid()
		}
		_ = index
	}
	sourceIndex := 0
	for scope, selection := range s.Scopes {
		for n, file := range NativeConfigurationScopeFiles(selection) {
			source := o.Sources[sourceIndex]
			sourceIndex++
			if source.ScopeIndex != uint32(scope) || source.File != file || !nativeExecutableDigest.MatchString(source.Digest) || !source.Present && source.Digest != HarnessEvidenceDigest(nil) {
				return NativeConfigurationInvalid()
			}
			if source.Shadowed && (n != 2 || source.Present || !o.Sources[sourceIndex-2].Present || selection.Kind == NativeConfigurationHome && o.Sources[sourceIndex-2].Digest == HarnessEvidenceDigest(nil)) {
				return NativeConfigurationInvalid()
			}
			sources[string(rune(scope))+file] = source
		}
	}
	seen := map[string]bool{}
	lastScope := uint32(0)
	for _, entry := range o.Entries {
		if int(entry.ScopeIndex) >= len(s.Scopes) || entry.ScopeIndex < lastScope || !nativeExecutableDigest.MatchString(entry.ID) || seen[entry.ID] {
			return NativeConfigurationInvalid()
		}
		seen[entry.ID] = true
		lastScope = entry.ScopeIndex
		source, ok := sources[string(rune(entry.ScopeIndex))+entry.File]
		if entry.Profile != "" && entry.Profile != s.Scopes[entry.ScopeIndex].Profile {
			return NativeConfigurationInvalid()
		}
		if !ok || !source.Present || source.Digest != entry.SourceDigest {
			return NativeConfigurationInvalid()
		}
		switch entry.Kind {
		case NativeConfigurationSetting:
			if _, err := NativeConfigurationSettingValue(entry.Name, entry.Value); err != nil || entry.Contents != "" {
				return NativeConfigurationInvalid()
			}
		case NativeConfigurationInstruction:
			if entry.Profile != "" || entry.Name != "instructions" || (entry.File != "AGENTS.md" && entry.File != "AGENTS.override.md" && !NativeConfigurationSelectedFile(s.Scopes[entry.ScopeIndex], entry.File, NativePackageInstruction)) || len(entry.Value) != 0 || Text(entry.Contents, "imported instructions", MaxNativeConfigurationFileBytes, true) != nil || NativeConfigurationSecret(entry.Contents) || HarnessEvidenceDigest([]byte(entry.Contents)) != entry.SourceDigest {
				return NativeConfigurationInvalid()
			}
		case NativeConfigurationInertExtension:
			if !slices.Contains([]string{"hooks", "plugins", "mcp_servers", "skill", "hook", "plugin", "mcp"}, entry.Name) || len(entry.Value) != 0 || len(entry.Files) == 0 || NativeConfigurationSecret(entry.Contents) {
				return NativeConfigurationInvalid()
			}
		case NativeConfigurationUnsupported, NativeConfigurationExcluded:
			if entry.Name != "unsupported" && entry.Name != "excluded" || len(entry.Value) != 0 || entry.Contents != "" {
				return NativeConfigurationInvalid()
			}
		default:
			return NativeConfigurationInvalid()
		}
		if entry.Kind == NativeConfigurationInertExtension || entry.Kind == NativeConfigurationInstruction {
			for _, file := range entry.Files {
				source, ok := sources[string(rune(entry.ScopeIndex))+file.Path]
				if !ok || !source.Present || source.Digest != file.Digest && !(entry.Kind == NativeConfigurationInertExtension && file.Path == entry.File && (entry.File == "config.toml" || entry.File == ".codex/config.toml")) {
					return NativeConfigurationInvalid()
				}
			}
			if ValidateNativePackageFiles(entry.Files) != nil {
				return NativeConfigurationInvalid()
			}
		} else if len(entry.Files) != 0 {
			return NativeConfigurationInvalid()
		}
	}
	return nil
}
func NativeConfigurationSettingValue(name string, raw []byte) (HarnessSettings, error) {
	settings := HarnessSettings{Version: 1}
	var value string
	if Decode(raw, &value) != nil || NativeConfigurationSecret(value) {
		return settings, NativeConfigurationInvalid()
	}
	switch name {
	case "model":
		if !nativeConfigurationSelector.MatchString(value) {
			return settings, NativeConfigurationInvalid()
		}
		settings.Model = OverrideHarnessSetting(value)
	case "effort":
		if !slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra", "persistent"}, value) {
			return settings, NativeConfigurationInvalid()
		}
		settings.Effort = OverrideHarnessSetting(value)
	case "permission":
		if !slices.Contains([]PermissionMode{PermissionReadOnly, PermissionWorkspaceWrite, PermissionFullAccess}, PermissionMode(value)) {
			return settings, NativeConfigurationInvalid()
		}
		settings.Permission = OverrideHarnessSetting(PermissionMode(value))
	case "approval_policy":
		if !slices.Contains([]string{"never", "on-request", "on-failure", "untrusted"}, value) {
			return settings, NativeConfigurationInvalid()
		}
		settings.ApprovalPolicy = OverrideHarnessSetting(value)
	case "service_tier":
		if !slices.Contains([]string{"", "default", "priority", "fast", "flex"}, value) {
			return settings, NativeConfigurationInvalid()
		}
		settings.ServiceTier = OverrideHarnessSetting(value)
	default:
		return settings, NativeConfigurationInvalid()
	}
	return settings, settings.Validate()
}
func MergeNativeConfigurationSettings(target *HarnessSettings, selected HarnessSettings) {
	target.Version = 1
	if selected.Model != nil {
		target.Model = selected.Model
	}
	if selected.Effort != nil {
		target.Effort = selected.Effort
	}
	if selected.Permission != nil {
		target.Permission = selected.Permission
	}
	if selected.ApprovalPolicy != nil {
		target.ApprovalPolicy = selected.ApprovalPolicy
	}
	if selected.ServiceTier != nil {
		target.ServiceTier = selected.ServiceTier
	}
}

func (t *Template) UnmarshalJSON(raw []byte) error {
	type plain Template
	var decoded plain
	if err := Decode(raw, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := Decode(raw, &fields); err != nil {
		return err
	}
	if value, present := fields["native_import"]; present && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return NativeConfigurationInvalid()
	}
	*t = Template(decoded)
	return t.Validate()
}

type NativeImportedPackage struct {
	Version      uint32                       `json:"version"`
	Harness      Harness                      `json:"harness"`
	Kind         NativeConfigurationEntryKind `json:"kind"`
	SourceDigest string                       `json:"source_digest"`
	// Selected packages retain native order independently of display names.
	Order   uint32                           `json:"order"`
	Enabled bool                             `json:"enabled"`
	Files   []NativeConfigurationPackageFile `json:"files"`
}

func (p *NativeImportedPackage) Validate(contents string) error {
	if p == nil {
		return nil
	}
	if p.Kind == NativeConfigurationInstruction && HarnessEvidenceDigest([]byte(contents)) != p.SourceDigest {
		return NativeConfigurationInvalid()
	}
	if p.Enabled || ValidateNativePackageFiles(p.Files) != nil || p.Version != 1 || !p.Harness.Valid() || !nativeExecutableDigest.MatchString(p.SourceDigest) || p.Order >= 128 || !slices.Contains([]NativeConfigurationEntryKind{NativeConfigurationInstruction, NativeConfigurationInertExtension}, p.Kind) || NativeConfigurationSecret(contents) {
		return NativeConfigurationInvalid()
	}
	return nil
}

type NativeConfigurationPackageKind string

const (
	NativePackageInstruction NativeConfigurationPackageKind = "instruction"
	NativePackageSkill       NativeConfigurationPackageKind = "skill"
	NativePackageHook        NativeConfigurationPackageKind = "hook"
	NativePackagePlugin      NativeConfigurationPackageKind = "plugin"
	NativePackageMCP         NativeConfigurationPackageKind = "mcp"
)

type NativeConfigurationReason string

const (
	NativeReasonUnsupported NativeConfigurationReason = "unsupported-setting"
	NativeReasonSecret      NativeConfigurationReason = "secret-bearing-source"
	NativeReasonExcluded    NativeConfigurationReason = "excluded-layer"
)

type NativeConfigurationPackageSelection struct {
	Kind  NativeConfigurationPackageKind `json:"kind"`
	Files []string                       `json:"files"`
}

func (p NativeConfigurationPackageSelection) Validate() error {
	if !slices.Contains([]NativeConfigurationPackageKind{NativePackageInstruction, NativePackageSkill, NativePackageHook, NativePackagePlugin, NativePackageMCP}, p.Kind) || len(p.Files) == 0 || len(p.Files) > 64 {
		return NativeConfigurationInvalid()
	}
	for _, file := range p.Files {
		if !NativeConfigurationSafePath(file) || strings.HasPrefix(path.Base(file), ".env") || strings.HasPrefix(file, ".git/") || NativeConfigurationSensitiveKey(file) || slices.Contains([]string{"config.toml", ".codex/config.toml", "AGENTS.md", "AGENTS.override.md"}, file) {
			return NativeConfigurationInvalid()
		}
	}
	return nil
}
func NativeConfigurationSafePath(file string) bool {
	return Text(file, "selected package resource", 4096, true) == nil && !path.IsAbs(file) && path.Clean(file) == file && file != "." && file != ".." && !strings.HasPrefix(file, "../") && !strings.ContainsAny(file, "\\\x00\r\n")
}
func NativeConfigurationScopeFiles(scope NativeConfigurationScope) []string {
	files := NativeConfigurationFiles(scope.Kind)
	for _, pkg := range scope.Packages {
		files = append(files, pkg.Files...)
	}
	return files
}
func NativeConfigurationSourceCount(s NativeConfigurationPreviewSelection) int {
	n := 0
	for _, scope := range s.Scopes {
		n += len(NativeConfigurationScopeFiles(scope))
	}
	return n
}
func NativeConfigurationSelectedFile(scope NativeConfigurationScope, file string, kind NativeConfigurationPackageKind) bool {
	for _, pkg := range scope.Packages {
		if pkg.Kind == kind && slices.Contains(pkg.Files, file) {
			return true
		}
	}
	return false
}

// Resources share the managed package manifest: exact path, bytes, digest and mode.
// Native imports are immutable and disabled; this metadata never grants trust.
type NativeConfigurationPackageFile struct {
	ManagedSkillResource
	Contents string `json:"contents"`
}

func ValidateNativePackageFiles(files []NativeConfigurationPackageFile) error {
	if len(files) == 0 || len(files) > 64 {
		return NativeConfigurationInvalid()
	}
	seen := map[string]bool{}
	total := 0
	for _, f := range files {
		if !NativeConfigurationSafePath(f.Path) || seen[f.Path] || !utf8.ValidString(f.Contents) || NativeConfigurationSecret(f.Contents) || f.Bytes != len(f.Contents) || f.Digest != HarnessEvidenceDigest([]byte(f.Contents)) {
			return NativeConfigurationInvalid()
		}
		seen[f.Path] = true
		total += f.Bytes
	}
	if total > MaxNativeConfigurationSourceBytes {
		return NativeConfigurationInvalid()
	}
	return nil
}

type NativeConfigurationPlan struct {
	Version      uint32                            `json:"version"`
	PreviewJobID ID                                `json:"preview_job_id"`
	Selection    NativeConfigurationApplySelection `json:"selection"`
	Token        string                            `json:"token"`
}
