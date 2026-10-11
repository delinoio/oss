// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claudeImportReadSelection(scopes []domain.NativeConfigurationScope) domain.NativeConfigurationPreviewSelection {
	selection := nativeImportReadSelection(scopes)
	selection.Harness = domain.ClaudeCode
	selection.Target.Scope.Harness = domain.ClaudeCode
	return selection
}
func TestClaudeImportSelectedLayersInstructionsAndInertPackages(t *testing.T) {
	home, project := nativeImportDirectory(t), nativeImportDirectory(t)
	nativeImportWrite(t, filepath.Join(home, "settings.json"), `{"model":"home-model","permissions":{"defaultMode":"plan","allow":["Read"]},"env":{"PRIVATE":"excluded"},"hooks":{"Stop":[{"command":"review-only"}]}}`)
	nativeImportWrite(t, filepath.Join(home, "CLAUDE.md"), "Home instructions.\n")
	nativeImportWrite(t, filepath.Join(project, ".claude/settings.json"), `{"model":"project-model","effortLevel":"high","enabledPlugins":{"review@example":true}}`)
	local := `{"model":"local-model","permissions":{"defaultMode":"acceptEdits"},"mcpServers":{"private":{"headers":{"Authorization":"private-value"}}}}`
	nativeImportWrite(t, filepath.Join(project, ".claude/settings.local.json"), local)
	for file, text := range map[string]string{"CLAUDE.md": "Root instructions.\n", ".claude/CLAUDE.md": "Project instructions.\n", "CLAUDE.local.md": "Local instructions.\n", ".claude/rules/plain.md": "Selected rule.\n", ".claude/rules/scoped.md": "---\npaths: src/**\n---\nScoped rule.\n", ".claude/skills/review/SKILL.md": "Review skill.\n", ".claude/plugins/review/.claude-plugin/plugin.json": `{"name":"review","skills":"../unselected"}`} {
		nativeImportWrite(t, filepath.Join(project, file), text)
	}
	// Nonregular authentication/memory/unselected files prove these paths are not opened.
	for _, file := range []string{filepath.Join(home, ".credentials.json"), filepath.Join(home, "memory"), filepath.Join(project, ".claude/rules/unselected.md")} {
		if err := os.MkdirAll(file, 0700); err != nil {
			t.Fatal(err)
		}
	}
	selection := claudeImportReadSelection([]domain.NativeConfigurationScope{{Kind: domain.NativeConfigurationHome, Path: home}, {Kind: domain.NativeConfigurationProject, Path: project, Files: []string{".claude/rules/plain.md", ".claude/rules/scoped.md", ".claude/skills/review/SKILL.md", ".claude/plugins/review/.claude-plugin/plugin.json"}}})
	observed, err := inspectNativeConfiguration(context.Background(), selection)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(observed)
	for _, secret := range []string{"PRIVATE", "private-value", "Authorization"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("excluded contents escaped", secret)
		}
	}
	models, instructions := []string{}, []string{}
	categories := map[string]bool{}
	for _, entry := range observed.Entries {
		if entry.Name == "model" {
			models = append(models, string(entry.Value))
		}
		if entry.Kind == domain.NativeConfigurationInstruction {
			instructions = append(instructions, entry.Contents)
		}
		if entry.Kind == domain.NativeConfigurationInertExtension {
			categories[entry.Name] = true
			var pkg domain.ClaudeInertConfiguration
			if domain.Decode([]byte(entry.Contents), &pkg) != nil || pkg.ExecutionAuthority || pkg.Trust || pkg.CredentialAuthority {
				t.Fatal("extension gained authority")
			}
		}
	}
	if strings.Join(models, ",") != `"home-model","project-model","local-model"` || strings.Join(instructions, "") != "Home instructions.\nRoot instructions.\nProject instructions.\nLocal instructions.\nSelected rule.\n" {
		t.Fatal("precedence or bytes changed", models, instructions)
	}
	for _, category := range []string{"hooks", "plugins", "skills", "rules"} {
		if !categories[category] {
			t.Fatal("selected inert package lost", category)
		}
	}
	retained, _ := os.ReadFile(filepath.Join(project, ".claude/settings.local.json"))
	if string(retained) != local {
		t.Fatal("source changed")
	}
}
func TestClaudeImportNoImplicitReadsBoundsLinksAndSourceDrift(t *testing.T) {
	for _, scenario := range []string{"parent-link", "oversize", "malformed", "profile", "source-drift", "import-directive"} {
		t.Run(scenario, func(t *testing.T) {
			root := nativeImportDirectory(t)
			selection := claudeImportReadSelection([]domain.NativeConfigurationScope{{Kind: domain.NativeConfigurationHome, Path: root}})
			nativeImportWrite(t, filepath.Join(root, "settings.json"), `{"model":"reviewed"}`)
			switch scenario {
			case "parent-link":
				outside := nativeImportDirectory(t)
				if err := os.Symlink(outside, filepath.Join(root, "rules")); err != nil {
					t.Skip(err)
				}
				selection.Scopes[0].Files = []string{"rules/review.md"}
			case "oversize":
				nativeImportWrite(t, filepath.Join(root, "CLAUDE.md"), strings.Repeat("x", domain.MaxNativeConfigurationFileBytes+1))
			case "malformed":
				nativeImportWrite(t, filepath.Join(root, "settings.json"), `{"model":"first","model":"second"}`)
			case "profile":
				selection.Scopes[0].Profile = "unsupported"
			case "import-directive":
				nativeImportWrite(t, filepath.Join(root, "CLAUDE.md"), "Read @../outside.md\n")
			}
			observed, err := inspectNativeConfiguration(context.Background(), selection)
			if scenario == "import-directive" {
				if err != nil || len(observed.Entries) != 2 || observed.Entries[1].Kind != domain.NativeConfigurationInertExtension {
					t.Fatal("directive followed or attached", err)
				}
				return
			}
			if scenario != "source-drift" {
				if err == nil {
					t.Fatal("unsafe selection accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(observed)
			instance, device := domain.NewID(), domain.NewID()
			input := domain.NativeConfigurationJobInput{Version: 1, ServerID: domain.NewID(), Actor: domain.Principal{Type: domain.OwnerDevice}, WorkerDeviceID: device, InstanceID: instance, PreviewJobID: domain.NewID(), Preview: selection, Selection: &domain.NativeConfigurationApplySelection{Version: 1, PreviewDigest: domain.HarnessEvidenceDigest(raw), Entries: []string{observed.Entries[0].ID}}}
			document, _ := json.Marshal(input)
			job := domain.Job{Type: domain.NativeConfigurationApplyJob, State: domain.JobClaimed, MachineID: selection.MachineID, InstanceID: instance, AssignedDeviceID: device, Input: document}
			if _, err := executeNativeConfigurationImport(context.Background(), Config{}, job); err != nil {
				t.Fatal(err)
			}
			nativeImportWrite(t, filepath.Join(root, "CLAUDE.md"), "New source after review.\n")
			if _, err := executeNativeConfigurationImport(context.Background(), Config{}, job); domain.SafeError(err).Code != domain.Conflict {
				t.Fatal("missing source drift accepted", err)
			}
		})
	}
}
