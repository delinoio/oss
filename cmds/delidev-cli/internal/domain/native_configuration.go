// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"path"
	"strings"
)

const ClaudeConfigurationImportV1 WorkerCapability = "claude-configuration-import-v1"
const MaxNativeConfigurationEntries = 128
const MaxNativeConfigurationBytes = 384 << 10

type NativeConfigurationSource string

const (
	NativeConfigurationUser    NativeConfigurationSource = "user"
	NativeConfigurationProject NativeConfigurationSource = "project"
	NativeConfigurationLocal   NativeConfigurationSource = "project-local"
)

type NativeConfigurationEntryKind string

const (
	NativeConfigurationSetting     NativeConfigurationEntryKind = "setting"
	NativeConfigurationInstruction NativeConfigurationEntryKind = "instruction"
	NativeConfigurationSkill       NativeConfigurationEntryKind = "skill"
	NativeConfigurationPlugin      NativeConfigurationEntryKind = "plugin"
	NativeConfigurationHook        NativeConfigurationEntryKind = "hook"
	NativeConfigurationMCP         NativeConfigurationEntryKind = "mcp"
)

type NativeConfigurationActivation string

const NativeConfigurationDisabled NativeConfigurationActivation = "disabled"

type NativeConfigurationFile struct {
	Path string `json:"path"
"net/url"`
	Contents string `json:"contents"`
}
type NativeConfigurationEntry struct {
	ID     string                    `json:"id"`
	Source NativeConfigurationSource `json:"source"`
	Path   string                    `json:"path"
"net/url"`
	Name       string                        `json:"name"`
	Kind       NativeConfigurationEntryKind  `json:"kind"`
	Precedence uint32                        `json:"precedence"`
	Digest     string                        `json:"digest,omitempty"`
	Supported  bool                          `json:"supported"`
	Reason     string                        `json:"reason,omitempty"`
	Value      json.RawMessage               `json:"value,omitempty"`
	Files      []NativeConfigurationFile     `json:"files,omitempty"`
	Activation NativeConfigurationActivation `json:"activation"`
}
type NativeConfigurationSelection struct {
	MachineID        ID       `json:"machine_id"`
	ProjectID        ID       `json:"project_id,omitempty"`
	IncludeUser      bool     `json:"include_user"`
	TargetID         ID       `json:"target_id,omitempty"`
	ExpectedRevision uint64   `json:"expected_revision,string"`
	Name             string   `json:"name"`
	Entries          []string `json:"entries"`
}
type NativeConfigurationReadScope struct {
	MachineID          ID     `json:"machine_id"`
	ProjectID          ID     `json:"project_id,omitempty"`
	ProjectRevision    uint64 `json:"project_revision,string"`
	RepositoryID       ID     `json:"repository_id,omitempty"`
	RepositoryRevision uint64 `json:"repository_revision,string"`
	ProjectRoot        string `json:"project_root,omitempty"`
	IncludeUser        bool   `json:"include_user"`
	WorkerDeviceID     ID     `json:"worker_device_id"`
	WorkerInstanceID   ID     `json:"worker_instance_id"`
}
type NativeConfigurationSnapshot struct {
	Digest  string                     `json:"digest"`
	Entries []NativeConfigurationEntry `json:"entries"`
}
type NativeConfigurationPreview struct {
	Version   uint32                       `json:"version"`
	Selection NativeConfigurationSelection `json:"selection"`
	Scope     NativeConfigurationReadScope `json:"scope"`
	Snapshot  NativeConfigurationSnapshot  `json:"snapshot"`
	Token     string                       `json:"token"`
}
type ImportedNativeConfiguration struct {
	Version      uint32                     `json:"version"`
	Name         string                     `json:"name"`
	MachineID    ID                         `json:"machine_id"`
	ProjectID    ID                         `json:"project_id,omitempty"`
	SourceDigest string                     `json:"source_digest"`
	Entries      []NativeConfigurationEntry `json:"entries"`
}

func NativeConfigurationDigest(v any) string {
	raw, _ := json.Marshal(v)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func NativeConfigurationEntryDigest(e NativeConfigurationEntry) string {
	e.Digest = ""
	return NativeConfigurationDigest(e)
}
func NativeConfigurationEntryID(e NativeConfigurationEntry) string {
	return NativeConfigurationDigest([]any{e.Source, e.Path, e.Name, e.Kind})
}
func nativeConfigurationDigestValid(v string) bool {
	b, err := hex.DecodeString(v)
	return err == nil && len(b) == sha256.Size && hex.EncodeToString(b) == v
}
func NativeConfigurationSafePath(v string) bool {
	return v != "" && !path.IsAbs(v) && path.Clean(v) == v && v != ".." && !strings.HasPrefix(v, "../") && !strings.ContainsAny(v, "\\\x00")
}
func NativeConfigurationSettingAllowed(name string) bool {
	return name == "model" || name == "effortLevel" || name == "permissions"
}
func NativeConfigurationSettingSupported(name string, raw json.RawMessage) bool {
	if !NativeConfigurationSettingAllowed(name) || !NativeConfigurationJSONSafe(raw) {
		return false
	}
	if name != "permissions" {
		var v string
		return json.Unmarshal(raw, &v) == nil && Text(v, "native scalar setting", 256, true) == nil
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return false
	}
	for key, value := range values {
		switch key {
		case "allow", "deny", "ask", "additionalDirectories":
			var items []string
			if json.Unmarshal(value, &items) != nil || len(items) > 128 {
				return false
			}
			for _, item := range items {
				if Text(item, "permission rule", 4096, true) != nil {
					return false
				}
			}
		case "defaultMode", "disableBypassPermissionsMode":
			var v string
			if json.Unmarshal(value, &v) != nil || Text(v, "permission mode", 128, true) != nil {
				return false
			}
		default:
			return false
		}
	}
	return true
}
func (s NativeConfigurationSelection) Validate() error {
	if s.MachineID.Validate() != nil || s.ProjectID != "" && s.ProjectID.Validate() != nil || !s.IncludeUser && s.ProjectID == "" || s.TargetID != "" && s.TargetID.Validate() != nil || s.TargetID == "" && s.ExpectedRevision != 0 || len(s.Entries) > MaxNativeConfigurationEntries || Text(s.Name, "import name", 256, true) != nil {
		return Fail(InvalidArgument, "Invalid Claude configuration selection.", "Choose an original Runner, source and imported destination.")
	}
	seen := map[string]bool{}
	for _, id := range s.Entries {
		if !nativeConfigurationDigestValid(id) || seen[id] {
			return Fail(InvalidArgument, "Invalid Claude configuration entry selection.", "Select each exact supported entry once.")
		}
		seen[id] = true
	}
	return nil
}
func NativeConfigurationTextSafe(v string) bool {
	lower := strings.ToLower(v)
	for _, prefix := range []string{"bearer ", "sk-ant-", "sk-proj-", "access_token=", "password=", "api_key=", "api-key=", "apikey=", "client_secret="} {
		if strings.Contains(lower, prefix) {
			return false
		}
	}
	if parsed, e := url.Parse(v); e == nil && parsed.User != nil {
		return false
	}
	return true
}
func (e NativeConfigurationEntry) Validate() error {
	if e.Source != NativeConfigurationUser && e.Source != NativeConfigurationProject && e.Source != NativeConfigurationLocal || e.Precedence < 1 || e.Precedence > 3 || !NativeConfigurationSafePath(e.Path) || Text(e.Name, "entry name", 256, true) != nil || e.Activation != NativeConfigurationDisabled || e.ID != NativeConfigurationEntryID(e) {
		return Fail(InvalidArgument, "Invalid native configuration entry.", "Refresh the selected original sources.")
	}
	expected := map[NativeConfigurationSource]uint32{NativeConfigurationUser: 1, NativeConfigurationProject: 2, NativeConfigurationLocal: 3}
	if e.Precedence != expected[e.Source] {
		return Fail(InvalidArgument, "Invalid native source precedence.", "Refresh the original source.")
	}
	switch e.Kind {
	case NativeConfigurationSetting, NativeConfigurationInstruction, NativeConfigurationSkill, NativeConfigurationPlugin, NativeConfigurationHook, NativeConfigurationMCP:
	default:
		return Fail(Unsupported, "Unknown native configuration kind.", "Update the importer.")
	}
	if !e.Supported {
		if e.Digest != "" || len(e.Value) != 0 || len(e.Files) != 0 || Text(e.Reason, "unsupported reason", 128, true) != nil {
			return Fail(InvalidArgument, "Unsupported native configuration exposed content.", "Refresh the selected original sources.")
		}
		return nil
	}
	if e.Reason != "" || e.Digest != NativeConfigurationEntryDigest(e) || len(e.Files) > 128 {
		return Fail(InvalidArgument, "Native configuration content changed.", "Generate a fresh preview.")
	}
	switch e.Kind {
	case NativeConfigurationSetting:
		if !NativeConfigurationSettingSupported(e.Name, e.Value) || len(e.Value) == 0 || !json.Valid(e.Value) || len(e.Files) != 0 {
			return Fail(Unsupported, "Unsupported native setting.", "Keep unsupported settings in their original CLI configuration.")
		}
	case NativeConfigurationInstruction, NativeConfigurationSkill, NativeConfigurationPlugin, NativeConfigurationHook, NativeConfigurationMCP:
		if len(e.Value) != 0 || len(e.Files) == 0 {
			return Fail(InvalidArgument, "Missing immutable native package content.", "Refresh its original source.")
		}
	default:
		return Fail(Unsupported, "Unknown native configuration kind.", "Update the original importer.")
	}
	total := len(e.Value)
	seen := map[string]bool{}
	for _, f := range e.Files {
		if !NativeConfigurationTextSafe(f.Contents) || !NativeConfigurationSafePath(f.Path) || seen[f.Path] || Text(f.Contents, "imported contents", 64<<10, true) != nil {
			return Fail(InvalidArgument, "Invalid immutable native package.", "Use bounded regular UTF-8 files.")
		}
		if strings.HasSuffix(f.Path, ".json") && !NativeConfigurationJSONSafe([]byte(f.Contents)) {
			return Fail(Unsupported, "Credential-bearing native package.", "Keep credentials outside imported configuration.")
		}
		seen[f.Path] = true
		total += len(f.Contents)
	}
	if total > 256<<10 {
		return Fail(ResourceExhausted, "Native package exceeds its import bound.", "Select a bounded package.")
	}
	return nil
}
func (s NativeConfigurationSnapshot) Validate() error {
	if !nativeConfigurationDigestValid(s.Digest) || len(s.Entries) > MaxNativeConfigurationEntries {
		return Fail(InvalidArgument, "Invalid native configuration observation.", "Refresh the original sources.")
	}
	raw, _ := json.Marshal(s)
	if len(raw) > MaxNativeConfigurationBytes {
		return Fail(ResourceExhausted, "Native configuration exceeds its observation bound.", "Use a smaller explicit source scope.")
	}
	seen := map[string]bool{}
	for _, e := range s.Entries {
		if err := e.Validate(); err != nil {
			return err
		}
		if seen[e.ID] {
			return Fail(InvalidArgument, "Repeated native configuration entry.", "Refresh the original sources.")
		}
		seen[e.ID] = true
	}
	return nil
}
func (v ImportedNativeConfiguration) Validate() error {
	if v.Version != 1 || Text(v.Name, "import name", 256, true) != nil || v.MachineID.Validate() != nil || v.ProjectID != "" && v.ProjectID.Validate() != nil || len(v.Entries) == 0 {
		return Fail(InvalidArgument, "Invalid imported native configuration.", "Use the reviewed Claude import operation.")
	}
	return (NativeConfigurationSnapshot{Digest: v.SourceDigest, Entries: v.Entries}).Validate()
}

func NativeConfigurationJSONSafe(raw []byte) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	var safe func(any) bool
	safe = func(v any) bool {
		switch t := v.(type) {
		case map[string]any:
			for k, v := range t {
				lower := strings.ToLower(k)
				if lower == "env" || lower == "headers" || lower == "key" || strings.Contains(lower, "apikey") || strings.Contains(lower, "api_key") || strings.Contains(lower, "privatekey") || strings.Contains(lower, "private_key") || strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "credential") || strings.Contains(lower, "auth") {
					if m, ok := v.(map[string]any); !ok || len(m) != 0 {
						return false
					}
				}
				if !safe(v) {
					return false
				}
			}
		case []any:
			for _, v := range t {
				if !safe(v) {
					return false
				}
			}
		case string:
			if !NativeConfigurationTextSafe(t) {
				return false
			}
		}
		return true
	}
	return safe(value)
}
