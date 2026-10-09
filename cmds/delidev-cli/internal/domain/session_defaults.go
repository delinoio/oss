// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"
)

const DefaultBranchPrefix = "delidev/"

// ValidateBranchPrefix validates the literal concatenation, without Git or shell
// interpolation. Keep this Git ref grammar in sync with the desktop validator.
func ValidateBranchPrefix(prefix string) error {
	invalid := func() error {
		return Fail(InvalidArgument, "Invalid branch prefix.", "Use up to 256 UTF-8 bytes forming a Git branch name with a suffix, or leave it empty to disable the instruction.")
	}
	if prefix == "" {
		return nil
	}
	if !utf8.ValidString(prefix) || len(prefix) > 256 {
		return invalid()
	}
	ref := prefix + "branch"
	if strings.HasPrefix(ref, "-") || strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, ".") || strings.Contains(ref, "..") || strings.Contains(ref, "@{") || strings.Contains(ref, "//") {
		return invalid()
	}
	for _, r := range ref {
		if r <= 32 || r == 127 || strings.ContainsRune("~^:?*[\\", r) {
			return invalid()
		}
	}
	for _, part := range strings.Split(ref, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return invalid()
		}
	}
	return nil
}
func (s Settings) EffectiveBranchPrefix() string {
	if s.BranchPrefix != nil {
		return *s.BranchPrefix
	}
	return DefaultBranchPrefix
}
func (p *Project) EffectiveBranchPrefix(global string) string {
	if p != nil && p.Settings != nil && p.Settings.BranchPrefix != nil {
		return *p.Settings.BranchPrefix
	}
	return global
}
func (p *Project) EffectivePlanMode(global bool) bool {
	if p != nil && p.Settings != nil {
		return p.Settings.PlanModeDefault.Resolve(global)
	}
	return global
}

// The optional declaration preserves legacy snapshot bytes and records only
// non-secret configuration provenance. It never grants Git/workspace authority.
type BranchPrefixSelection struct {
	Version          uint32 `json:"version"`
	Prefix           string `json:"prefix"`
	SettingsID       ID     `json:"settings_id,omitempty"`
	SettingsRevision uint64 `json:"settings_revision"`
	ProjectID        ID     `json:"project_id,omitempty"`
	ProjectRevision  uint64 `json:"project_revision"`
}

func (p BranchPrefixSelection) Validate() error {
	if p.Version != 1 || (p.SettingsID == "") != (p.SettingsRevision == 0) || (p.ProjectID == "") != (p.ProjectRevision == 0) || p.SettingsID != "" && p.SettingsID.Validate() != nil || p.ProjectID != "" && p.ProjectID.Validate() != nil {
		return Fail(RecoveryRequired, "Invalid retained branch prefix provenance.", "Preserve the original execution configuration.")
	}
	return ValidateBranchPrefix(p.Prefix)
}

// NativeInstructions retains template bytes and appends exactly one shared
// Execute instruction. Plan and read-only Sidechat retain their original profile.
func (c ExecutionConfiguration) NativeInstructions(mode SessionMode) (string, error) {
	instructions := c.Instructions
	if c.BranchPrefix != nil && c.BranchPrefix.Prefix != "" && mode == ExecuteMode && c.SidechatPolicy == "" {
		literal, _ := json.Marshal(c.BranchPrefix.Prefix)
		if instructions != "" {
			instructions += "\n\n"
		}
		instructions += "When creating a new Git branch, concatenate the literal prefix " + string(literal) + " with a suffix you choose. Do not insert a separator or rename existing branches. Preserve existing branches and workspace permissions."
	}
	if len(instructions) > MaxAppliedInstructions {
		return "", Fail(ResourceExhausted, "Combined instructions exceed the native adapter bound.", "Reduce the ordered templates before first execution; no contents were truncated.")
	}
	return instructions, nil
}

// Reject invalid Unicode escapes before encoding/json can replace them. This
// keeps native instructions identical to the explicit scalar prefix selection.
func validateBranchPrefixJSON(raw json.RawMessage) error {
	invalid := func() error {
		return Fail(InvalidArgument, "Invalid branch prefix encoding.", "Use a UTF-8 string, including empty to disable.")
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' || bytes.Equal(raw, []byte("null")) {
		return invalid()
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return invalid()
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return invalid()
		}
		code, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return invalid()
		}
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return invalid()
		}
		if code >= 0xd800 && code <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return invalid()
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return invalid()
			}
			i += 6
		}
	}
	return nil
}
func (s *Settings) UnmarshalJSON(raw []byte) error {
	type plain Settings
	var fields map[string]json.RawMessage
	if err := Decode(raw, &fields); err != nil {
		return err
	}
	if value, ok := fields["branch_prefix"]; ok {
		if err := validateBranchPrefixJSON(value); err != nil {
			return err
		}
	}
	if value, ok := fields["plan_mode_default"]; ok && !bytes.Equal(value, []byte("true")) && !bytes.Equal(value, []byte("false")) {
		return Fail(InvalidArgument, "Invalid Plan Mode default.", "Use an explicit boolean.")
	}
	value := plain(*s)
	if err := Decode(raw, &value); err != nil {
		return err
	}
	*s = Settings(value)
	return nil
}
func (p *BranchPrefixSelection) UnmarshalJSON(raw []byte) error {
	type plain BranchPrefixSelection
	var fields map[string]json.RawMessage
	if err := Decode(raw, &fields); err != nil {
		return err
	}
	if err := validateBranchPrefixJSON(fields["prefix"]); err != nil {
		return err
	}
	var value plain
	if err := Decode(raw, &value); err != nil {
		return err
	}
	*p = BranchPrefixSelection(value)
	return p.Validate()
}
