// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

const CodexConfigurationImportV1 WorkerCapability = "codex-configuration-import-v1"
const InspectCodexConfigurationJob JobType = "inspect-codex-configuration"
const MaxNativeConfigurationBytes = 384 << 10
const MaxNativeConfigurationScopes = 16

type NativeConfigurationScopeKind string

const (
	NativeConfigurationHome    NativeConfigurationScopeKind = "home"
	NativeConfigurationProject NativeConfigurationScopeKind = "project"
)

type NativeConfigurationScope struct {
	Kind NativeConfigurationScopeKind `json:"kind"`
	Path string                       `json:"path"`
}
type NativeConfigurationRead struct {
	Scopes         []NativeConfigurationScope `json:"scopes"`
	ExpectedDigest string                     `json:"expected_digest,omitempty"`
}

func (r NativeConfigurationRead) Validate() error {
	if len(r.Scopes) == 0 || len(r.Scopes) > MaxNativeConfigurationScopes || r.ExpectedDigest != "" && !NativeConfigurationDigest(r.ExpectedDigest) {
		return NativeConfigurationInvalid()
	}
	seen := map[string]bool{}
	previous := ""
	for i, s := range r.Scopes {
		normalized, valid := nativeConfigurationScopePath(s.Path)
		if Text(s.Path, "selected native scope", 4096, true) != nil || !valid || seen[normalized] {
			return NativeConfigurationInvalid()
		}
		seen[normalized] = true
		switch s.Kind {
		case NativeConfigurationHome:
			if i != 0 {
				return NativeConfigurationInvalid()
			}
		case NativeConfigurationProject:
			if previous != "" && !strings.HasPrefix(normalized, strings.TrimSuffix(previous, "/")+"/") {
				return NativeConfigurationInvalid()
			}
			previous = normalized
		default:
			return NativeConfigurationInvalid()
		}
	}
	return nil
}

type NativeConfigurationEntryKind string

const (
	NativeConfigurationSetting     NativeConfigurationEntryKind = "setting"
	NativeConfigurationInstruction NativeConfigurationEntryKind = "instruction"
	NativeConfigurationUnsupported NativeConfigurationEntryKind = "unsupported"
)

type NativeConfigurationEntry struct {
	Key    string                       `json:"key"`
	Scope  int                          `json:"scope"`
	Source string                       `json:"source"`
	Name   string                       `json:"name"`
	Kind   NativeConfigurationEntryKind `json:"kind"`
	Value  string                       `json:"value,omitempty"`
}
type NativeConfigurationFile struct {
	Scope   int    `json:"scope"`
	Name    string `json:"name"`
	Present bool   `json:"present"`
	Usable  bool   `json:"usable,omitempty"`
	Digest  string `json:"digest,omitempty"`
}
type NativeConfigurationSnapshot struct {
	Version uint32                     `json:"version"`
	Scopes  []NativeConfigurationScope `json:"scopes"`
	Files   []NativeConfigurationFile  `json:"files"`
	Entries []NativeConfigurationEntry `json:"entries"`
	Digest  string                     `json:"digest"`
}

func NativeConfigurationDigest(s string) bool {
	raw, e := hex.DecodeString(s)
	return e == nil && len(raw) == sha256.Size && strings.ToLower(s) == s
}
func (s NativeConfigurationSnapshot) SourceDigest() string {
	raw, _ := json.Marshal(struct {
		Scopes []NativeConfigurationScope
		Files  []NativeConfigurationFile
	}{s.Scopes, s.Files})
	d := sha256.Sum256(raw)
	return hex.EncodeToString(d[:])
}
func (s NativeConfigurationSnapshot) Validate() error {
	if s.Version != 1 || (NativeConfigurationRead{Scopes: s.Scopes}).Validate() != nil || s.Digest != s.SourceDigest() || len(s.Files) != 3*len(s.Scopes) || len(s.Entries) > 256 {
		return NativeConfigurationInvalid()
	}
	raw, _ := json.Marshal(s)
	if len(raw) > MaxNativeConfigurationBytes {
		return NativeConfigurationInvalid()
	}
	for index, file := range s.Files {
		scope := index / 3
		name := []string{"config.toml", "AGENTS.override.md", "AGENTS.md"}[index%3]
		if index%3 == 0 && s.Scopes[scope].Kind == NativeConfigurationProject {
			name = ".codex/config.toml"
		}
		if file.Scope != scope || file.Name != name || file.Present != NativeConfigurationDigest(file.Digest) || file.Usable && (!file.Present || index%3 == 0) || !file.Present && file.Digest != "" {
			return NativeConfigurationInvalid()
		}
	}
	previousScope := -1
	seen := map[string]bool{}
	for entryIndex, entry := range s.Entries {
		if Text(entry.Key, "entry identity", 256, true) != nil || seen[entry.Key] || entry.Scope < 0 || entry.Scope >= len(s.Scopes) || Text(entry.Name, "entry name", 128, true) != nil {
			return NativeConfigurationInvalid()
		}
		seen[entry.Key] = true
		if entry.Scope < previousScope {
			return NativeConfigurationInvalid()
		}
		previousScope = entry.Scope
		configName := s.Files[entry.Scope*3].Name
		if entry.Kind == NativeConfigurationInstruction {
			override := s.Files[entry.Scope*3+1]
			ordinary := s.Files[entry.Scope*3+2]
			source := ordinary
			if override.Usable {
				source = override
			}
			digest := sha256.Sum256([]byte(entry.Value))
			if !source.Usable || entry.Source != source.Name || entry.Name != "instructions" || hex.EncodeToString(digest[:]) != source.Digest || entry.Key != fmt.Sprintf("%d:%s", entry.Scope, entry.Source) {
				return NativeConfigurationInvalid()
			}
		} else if entry.Source != configName || !s.Files[entry.Scope*3].Present {
			return NativeConfigurationInvalid()
		}
		switch entry.Kind {
		case NativeConfigurationSetting:
			if !NativeConfigurationSettingSupported(entry.Name, entry.Value) || entry.Key != fmt.Sprintf("%d:%s:%s", entry.Scope, entry.Source, entry.Name) {
				return NativeConfigurationInvalid()
			}
		case NativeConfigurationInstruction:
			if Text(entry.Value, "instruction package", 128<<10, true) != nil {
				return NativeConfigurationInvalid()
			}
		case NativeConfigurationUnsupported:
			switch entry.Name {
			case "MCP settings (not imported)", "hooks (not imported)", "plugins (not imported)", "model or profile selection (unsupported)", "credential-bearing configuration (excluded)", "unsupported native configuration":
			default:
				return NativeConfigurationInvalid()
			}
			if entry.Value != "" || entry.Key != fmt.Sprintf("%d:%s:unsupported-%d", entry.Scope, entry.Source, entryIndex) {
				return NativeConfigurationInvalid()
			}
		default:
			return NativeConfigurationInvalid()
		}
	}
	return nil
}
func NativeConfigurationSettingSupported(name, value string) bool {
	switch name {
	case "model_reasoning_effort":
		return strings.Contains("|minimal|low|medium|high|xhigh|", "|"+value+"|") && value != ""
	case "service_tier":
		return value == "fast" || value == "flex"
	case "approval_policy":
		return value == "untrusted" || value == "on-failure" || value == "on-request" || value == "never"
	case "sandbox_mode":
		return value == "read-only" || value == "workspace-write" || value == "danger-full-access"
	}
	return false
}
func NativeConfigurationInvalid() *Error {
	return Fail(InvalidArgument, "The selected native configuration is invalid or unsupported.", "Select supported explicit scopes and compatible entries, then obtain a fresh preview.")
}

type NativeConfigurationImportSelection struct {
	PreviewJobID            ID       `json:"preview_job_id"`
	ExpectedPreviewRevision uint64   `json:"expected_preview_revision"`
	AgentID                 ID       `json:"agent_id"`
	ExpectedAgentRevision   uint64   `json:"expected_agent_revision"`
	Entries                 []string `json:"entries"`
}
type NativeInstructionSource struct {
	Digest string                       `json:"digest"`
	Scope  NativeConfigurationScopeKind `json:"scope"`
	Source string                       `json:"source"`
}
type NativeInstructionPackage struct {
	ID       ID       `json:"id"`
	Template Template `json:"template"`
}
type NativeConfigurationImportPlan struct {
	SourceJobID           ID                         `json:"source_job_id"`
	SourceRevision        uint64                     `json:"source_revision"`
	MachineID             ID                         `json:"machine_id"`
	DeviceID              ID                         `json:"device_id"`
	InstanceID            ID                         `json:"instance_id"`
	Read                  NativeConfigurationRead    `json:"read"`
	AgentID               ID                         `json:"agent_id"`
	ExpectedAgentRevision uint64                     `json:"expected_agent_revision"`
	Agent                 Agent                      `json:"agent"`
	Packages              []NativeInstructionPackage `json:"packages"`
	Selected              []string                   `json:"selected"`
}
type NativeConfigurationImportPreview struct {
	Plan  NativeConfigurationImportPlan `json:"plan"`
	Token string                        `json:"token"`
}

type NativeConfigurationJobInput struct {
	Actor      Principal               `json:"actor"`
	Read       NativeConfigurationRead `json:"read"`
	MachineID  ID                      `json:"machine_id"`
	DeviceID   ID                      `json:"device_id"`
	InstanceID ID                      `json:"instance_id"`
}

func (i NativeConfigurationJobInput) Validate() error {
	if i.MachineID.Validate() != nil || i.DeviceID.Validate() != nil || i.InstanceID.Validate() != nil {
		return NativeConfigurationInvalid()
	}
	return i.Read.Validate()
}

// A server can run on a different OS than the selected Worker. Check explicit
// POSIX and drive-absolute Windows shapes without applying the server's cwd.
func nativeConfigurationScopePath(value string) (string, bool) {
	if strings.HasPrefix(value, "/") {
		return value, path.Clean(value) == value
	}
	if len(value) < 3 || value[1] != ':' || (value[2] != '\\' && value[2] != '/') || !((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) {
		return "", false
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	clean := path.Clean(normalized)
	if len(normalized) == 3 {
		clean += "/"
	}
	return strings.ToLower(normalized), clean == normalized
}
