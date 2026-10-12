// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
)

// Owned locations pin the complete original package, including executable modes.
// Only the name/digest/policy tuple enters the location-independent settings hash.
type checkpointSkill struct {
	Name            string `json:"name"`
	Location        string `json:"location"`
	ContentRevision string `json:"content_revision"`
	ExplicitOnly    bool   `json:"explicit_only"`
}
type checkpointSkillSettings struct {
	Name            string `json:"name"`
	ContentRevision string `json:"content_revision"`
	ExplicitOnly    bool   `json:"explicit_only"`
}

func retainedCheckpointSkills(selected []skills.OpenCodeSelection) []checkpointSkill {
	var out []checkpointSkill
	for _, s := range selected {
		out = append(out, checkpointSkill{s.Name, s.Path, s.ContentRevision, s.ExplicitOnly})
	}
	slices.SortFunc(out, func(a, b checkpointSkill) int { return strings.Compare(a.Name, b.Name) })
	return out
}
func checkpointSkillFingerprint(selected []skills.OpenCodeSelection) []checkpointSkillSettings {
	var out []checkpointSkillSettings
	for _, s := range retainedCheckpointSkills(selected) {
		out = append(out, checkpointSkillSettings{s.Name, s.ContentRevision, s.ExplicitOnly})
	}
	return out
}
func validCheckpointSkills(c nativeCheckpoint) bool {
	if len(c.Skills) > domain.MaxSelectedSkills {
		return false
	}
	seen := map[string]bool{}
	for i, s := range c.Skills {
		if !checkpointDigest(s.ContentRevision) || !checkpointPath(s.Location) || s.Name == "" || seen[strings.ToLower(s.Name)] || i > 0 && c.Skills[i-1].Name >= s.Name || s.Location != filepath.Join(c.RuntimeHome, "opencode", "skills", s.Name, "SKILL.md") {
			return false
		}
		seen[strings.ToLower(s.Name)] = true
		relative := filepath.ToSlash(filepath.Join("opencode", "skills", s.Name, "SKILL.md"))
		if !slices.ContainsFunc(c.Files, func(f checkpointFile) bool { return f.Path == relative && !f.Directory }) {
			return false
		}
	}
	for _, h := range checkpointHistories(c) {
		if h.SkillCommand != nil && !slices.ContainsFunc(c.Skills, func(s checkpointSkill) bool {
			return s.Name == h.SkillCommand.Name && s.ContentRevision == h.SkillCommand.ContentRevision
		}) {
			return false
		}
	}
	return true
}
func inspectCheckpointSkills(ctx context.Context, c nativeCheckpoint) error {
	var selected []skills.OpenCodeSelection
	for _, s := range c.Skills {
		selection, err := skills.OpenCodePackage(ctx, skills.NativeSelection{Name: s.Name, Path: s.Location, ContentRevision: s.ContentRevision})
		if err != nil || selection.ExplicitOnly != s.ExplicitOnly {
			return sessionUncertain()
		}
		selected = append(selected, selection)
	}
	// A historical checkpoint without this profile keeps its original closure.
	if len(c.Skills) == 0 {
		return nil
	}
	return skills.VerifyOpenCodeRuntime(ctx, c.RuntimeHome, selected)
}

// CheckpointSkillSources returns authenticated owned copies, never current
// catalog state. Callers still need their separate assignment/cleanup authority.
func CheckpointSkillSources(ctx context.Context, home string, raw []byte, ref CheckpointReference) ([]skills.NativeSelection, error) {
	c, err := decodeCheckpoint(raw, ref, home)
	if err != nil || InspectCheckpoint(ctx, home, raw, ref) != nil {
		return nil, sessionUncertain()
	}
	out := []skills.NativeSelection{}
	for _, s := range c.Skills {
		out = append(out, skills.NativeSelection{Name: s.Name, Path: s.Location, ContentRevision: s.ContentRevision})
	}
	return out, nil
}
