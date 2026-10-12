// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func (s *sessionAPI) verifyNativeSkills(ctx context.Context) error {
	p := s.apiProfile
	if p == nil || len(p.Skills) == 0 {
		return nil
	}
	s.skillInventoryRead = true
	defer func() { s.skillInventoryRead = false }()
	raw, _, err := s.request(ctx, http.MethodGet, "/skill", nil, http.StatusOK)
	if err != nil {
		return err
	}
	var inventory []json.RawMessage
	if domain.Decode(raw, &inventory) != nil || len(inventory) != len(p.Skills)+1 {
		return sessionProblem()
	}
	seen := map[string]bool{}
	for _, raw := range inventory {
		fields, err := shape(raw, []string{"name", "location", "content"}, []string{"description"})
		if err != nil {
			return err
		}
		name, valid := boundedString(fields["name"], 128, true)
		if !valid || seen[name] {
			return sessionProblem()
		}
		seen[name] = true
		if name == "customize-opencode" {
			if !scalar(fields["location"], "<built-in>") {
				return sessionProblem()
			}
			continue // Its native tool policy is denied independently.
		}
		found := false
		for _, selection := range p.Skills {
			if name == selection.Name {
				found = true
				if !scalar(fields["location"], selection.Path) || !scalar(fields["description"], selection.Description) || !scalar(fields["content"], selection.Content) {
					return sessionProblem()
				}
			}
		}
		if !found {
			return sessionProblem()
		}
	}
	raw, _, err = s.request(ctx, http.MethodGet, "/command", nil, http.StatusOK)
	if err != nil {
		return err
	}
	var commands []json.RawMessage
	if domain.Decode(raw, &commands) != nil || len(commands) != len(p.Skills)+3 {
		return sessionProblem()
	}
	seen = map[string]bool{}
	for _, raw := range commands {
		fields, err := shape(raw, []string{"name", "template", "hints"}, []string{"description", "agent", "model", "source", "subtask"})
		if err != nil {
			return err
		}
		name, valid := boundedString(fields["name"], 128, true)
		if !valid || seen[name] {
			return sessionProblem()
		}
		seen[name] = true
		if slices.Contains([]string{"init", "review", "customize-opencode"}, name) {
			continue
		}
		found := false
		for _, selection := range p.Skills {
			if name != selection.Name {
				continue
			}
			found = true
			expected := selection.Content + "\n\nBase directory for this skill: " + filepath.Dir(selection.Path) + "\nRelative paths in this skill (e.g., scripts/, references/) are relative to this base directory."
			if !scalar(fields["source"], "skill") || !scalar(fields["template"], expected) || !scalar(fields["description"], selection.Description) || string(fields["hints"]) != "[]" || len(fields["agent"]) != 0 || len(fields["model"]) != 0 || len(fields["subtask"]) != 0 {
				return sessionProblem()
			}
		}
		if !found {
			return sessionProblem()
		}
	}
	if s.logger != nil {
		s.logger.InfoContext(ctx, "opencode_native_skill_inventory_verified", "owner_id", s.owner, "selected_packages", len(p.Skills))
	}
	return nil
}

// Compare complete original primary-agent policy. Skill directories add only
// native external-directory allowances; they do not grant edits or commands.
func validatePrimaryAgentSkills(raw []byte, selected PrimaryAgent, home, worktree string, profile *nativeAPIProfile) error {
	if len(profile.Skills) == 0 {
		return validatePrimaryAgent(raw, selected, home, worktree, profile.References...)
	}
	expected, err := expectedPrimaryAgent(selected, home, worktree, profile.References...)
	if err != nil {
		return err
	}
	rules := expected["permission"].([]PermissionRule)
	ordered := slices.Clone(profile.Skills)
	slices.SortFunc(ordered, func(a, b skills.OpenCodeSelection) int { return strings.Compare(a.Path, b.Path) })
	additional := []PermissionRule{}
	for _, selection := range ordered {
		additional = append(additional, PermissionRule{"external_directory", filepath.Join(filepath.Dir(selection.Path), "*"), PermissionAllow})
	}
	skillDirectories := slices.Clone(additional)
	rules = append(append(append([]PermissionRule{}, rules[:5]...), additional...), rules[5:]...)
	// The pinned native agent merges user permission rules before its final tool
	// output allowance. Native JSON key order is preserved from the exact config.
	additional = []PermissionRule{{"skill", "*", PermissionDeny}}
	slices.SortFunc(ordered, func(a, b skills.OpenCodeSelection) int { return strings.Compare(a.Name, b.Name) })
	for _, selection := range ordered {
		if !selection.ExplicitOnly {
			additional = append(additional, PermissionRule{"skill", selection.Name, PermissionAllow})
		}
	}
	rules = append(append(append([]PermissionRule{}, rules[:len(rules)-1]...), additional...), rules[len(rules)-1:]...)
	expected["permission"] = rules
	var agents []json.RawMessage
	if domain.Decode(raw, &agents) != nil || len(agents) != 7 {
		return sessionProblem()
	}
	seen := map[string]bool{}
	for _, agent := range agents {
		fields, err := shape(agent, []string{"name", "mode", "native", "permission", "options"}, []string{"description", "hidden", "prompt", "temperature"})
		if err != nil {
			return err
		}
		name, valid := boundedString(fields["name"], 128, true)
		if !valid || seen[name] || !slices.Contains([]string{"build", "plan", "general", "explore", "compaction", "title", "summary"}, name) || string(fields["native"]) != "true" {
			return sessionProblem()
		}
		seen[name] = true
		if name == string(selected) {
			// The native scanner retains Glob encounter order in a Set. These
			// disjoint selected-directory rules all allow the same permission;
			// authenticate the exact set within its original policy position.
			var actual []PermissionRule
			if domain.Decode(fields["permission"], &actual) != nil || len(actual) != len(rules) {
				return sessionProblem()
			}
			remaining := slices.Clone(skillDirectories)
			for _, rule := range actual[5 : 5+len(skillDirectories)] {
				index := slices.Index(remaining, rule)
				if index < 0 {
					return sessionProblem()
				}
				remaining = slices.Delete(remaining, index, index+1)
			}
			copy(rules[5:5+len(skillDirectories)], actual[5:5+len(skillDirectories)])
			if err := exactPrivateJSON(agent, expected); err != nil {
				return err
			}
		}
	}
	return nil
}

// Recheck the actual HTTP execution owner's complete policy and registry before
// later mutations. Local byte proofs alone cannot authenticate native caches.
func (s *sessionAPI) verifyNativeSkillContext(ctx context.Context) error {
	if s.apiProfile == nil || len(s.apiProfile.Skills) == 0 || s.runtimeRoot == "" {
		return nil
	}
	s.skillInventoryRead = true
	defer func() { s.skillInventoryRead = false }()
	for _, step := range []struct {
		path     string
		validate func([]byte) error
	}{
		{"/config", s.apiProfile.validateConfig},
		{"/agent", func(raw []byte) error {
			return validatePrimaryAgentSkills(raw, s.apiProfile.Settings.Agent, s.runtimeHome, s.runtimeRoot, s.apiProfile)
		}},
	} {
		raw, _, err := s.request(ctx, http.MethodGet, step.path, nil, http.StatusOK)
		if err != nil {
			return err
		}
		if err = step.validate(raw); err != nil {
			return err
		}
	}
	return s.verifyNativeSkills(ctx)
}

// These operation identities belong to the pinned legacy HTTP execution service.
// Alternative native services cannot reuse this selected-package proof.
func nativeSkillOperations() []operationProfile {
	return []operationProfile{
		{"/skill", "get", "app.skills", "200", "application/json"},
		{"/command", "get", "command.list", "200", "application/json"},
		{"/session/{sessionID}/command", "post", "session.command", "200", "application/json"},
	}
}
