// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
)

func TestNativeSkillPolicyAuthenticatesDirectorySetAndPolicyPosition(t *testing.T) {
	home := t.TempDir()
	selected := []skills.OpenCodeSelection{
		{NativeSelection: skills.NativeSelection{Name: "first", Path: filepath.Join(home, "opencode", "skills", "first", "SKILL.md")}},
		{NativeSelection: skills.NativeSelection{Name: "second", Path: filepath.Join(home, "opencode", "skills", "second", "SKILL.md")}, ExplicitOnly: true},
	}
	profile := &nativeAPIProfile{Skills: selected}
	primary, err := expectedPrimaryAgent(BuildAgent, home, home)
	if err != nil {
		t.Fatal(err)
	}
	base := primary["permission"].([]PermissionRule)
	directories := []PermissionRule{{"external_directory", filepath.Join(filepath.Dir(selected[1].Path), "*"), PermissionAllow}, {"external_directory", filepath.Join(filepath.Dir(selected[0].Path), "*"), PermissionAllow}}
	rules := append(append(slices.Clone(base[:5]), directories...), base[5:]...)
	rules = append(append(slices.Clone(rules[:len(rules)-1]), PermissionRule{"skill", "*", PermissionDeny}, PermissionRule{"skill", "first", PermissionAllow}), rules[len(rules)-1])
	for _, change := range []string{"original-encounter-order", "foreign-dir", "duplicate-dir", "changed-permission", "moved-rule", "explicit-only-allow"} {
		t.Run(change, func(t *testing.T) {
			actual := slices.Clone(rules)
			switch change {
			case "foreign-dir":
				actual[5].Pattern = filepath.Join(home, "foreign", "*")
			case "duplicate-dir":
				actual[5] = actual[6]
			case "changed-permission":
				actual[5].Action = PermissionAsk
			case "moved-rule":
				actual[4], actual[5] = actual[5], actual[4]
			case "explicit-only-allow":
				actual = append(actual, PermissionRule{"skill", "second", PermissionAllow})
			}
			primary["permission"] = actual
			agents := fixtureAgentInventory(home)
			agents[0] = primary
			raw, _ := json.Marshal(agents)
			if err := validatePrimaryAgentSkills(raw, BuildAgent, home, home, profile); (err == nil) != (change == "original-encounter-order") {
				t.Fatalf("native policy %s: %v", change, err)
			}
		})
	}
}
