// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func nativeImportDirectory(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
func nativeImportWrite(t *testing.T, path, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
}
func nativeImportReadSelection(scopes []domain.NativeConfigurationScope) domain.NativeConfigurationPreviewSelection {
	return domain.NativeConfigurationPreviewSelection{Version: 1, Harness: domain.Codex, MachineID: domain.NewID(), MachineRevision: 1, Scopes: scopes, Target: domain.NativeConfigurationTarget{Kind: domain.NativeConfigurationServerDefaults, ID: domain.NewID(), Revision: 1, Scope: domain.HarnessDefaultScope{Harness: domain.Codex, Source: domain.HarnessSubscriptionSource, SubscriptionService: domain.SubscriptionChatGPT}}}
}
func TestNativeImportReadsOnlySelectedLayersAndPreservesOverride(t *testing.T) {
	home := nativeImportDirectory(t)
	project := nativeImportDirectory(t)
	nativeImportWrite(t, filepath.Join(home, "config.toml"), "model = 'home-model'\napi_key = 'sk-fixture-secret'\nunknown_private_key = 'hidden value'\n[mcp_servers.private]\ncommand = 'secret command'\n[profiles.reviewed]\nmodel_reasoning_effort = 'high'\n[profiles.unselected]\nmodel = 'never-selected'\n")
	nativeImportWrite(t, filepath.Join(home, "AGENTS.md"), "Home exact text.\n")
	nativeImportWrite(t, filepath.Join(project, ".codex", "config.toml"), "model = 'project-model'\nsandbox_mode = 'danger-full-access'\n")
	// Even a nonregular masked default must not be opened after an override wins.
	if err := os.Mkdir(filepath.Join(project, "AGENTS.md"), 0700); err != nil {
		t.Fatal(err)
	}
	nativeImportWrite(t, filepath.Join(project, "AGENTS.override.md"), "Original override.\nSecond line.\n")
	// Native credentials and generated memory are not candidates, even inside a
	// selected home. Unsupported profile paths never become source reads either.
	if err := os.Mkdir(filepath.Join(home, "auth.json"), 0700); err != nil {
		t.Fatal(err)
	}
	nativeImportWrite(t, filepath.Join(home, "memories", "AGENTS.md"), "sk-generated-secret")
	selection := nativeImportReadSelection([]domain.NativeConfigurationScope{{Kind: domain.NativeConfigurationHome, Path: home, Profile: "reviewed"}, {Kind: domain.NativeConfigurationProject, Path: project}})
	observation, err := inspectNativeConfiguration(context.Background(), selection)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(observation)
	for _, excluded := range []string{"sk-fixture-secret", "hidden value", "unknown_private_key", "secret command", "never-selected", "sk-generated-secret", "Never import the masked"} {
		if bytes.Contains(raw, []byte(excluded)) {
			t.Fatal("unselected or excluded contents escaped", excluded)
		}
	}
	values := []string{}
	instructions := []string{}
	for _, entry := range observation.Entries {
		if entry.Name == "effort" && entry.Profile != "reviewed" {
			t.Fatal("selected profile provenance missing", entry)
		}
		if entry.Name == "model" && entry.Profile != "" {
			t.Fatal("base value falsely labeled as selected profile", entry)
		}
		if entry.Name == "model" {
			values = append(values, string(entry.Value))
		}
		if entry.Kind == domain.NativeConfigurationInstruction {
			instructions = append(instructions, entry.Contents)
		}
	}
	if strings.Join(values, ",") != `"project-model"` || len(instructions) != 2 || instructions[0] != "Home exact text.\n" || instructions[1] != "Original override.\nSecond line.\n" {
		t.Fatal("selected layer order or original text changed", values, instructions)
	}
	for _, source := range observation.Sources {
		if strings.Contains(source.File, "auth") || strings.Contains(source.File, "memories") {
			t.Fatal("unselected read")
		}
	}
	retained, err := os.ReadFile(filepath.Join(project, "AGENTS.override.md"))
	if err != nil || string(retained) != instructions[1] {
		t.Fatal("host source changed", err)
	}
}
func TestNativeImportRejectsSourceDriftAndOriginalWorkerReplacement(t *testing.T) {
	root := nativeImportDirectory(t)
	nativeImportWrite(t, filepath.Join(root, "config.toml"), "model = 'reviewed'\n")
	selection := nativeImportReadSelection([]domain.NativeConfigurationScope{{Kind: domain.NativeConfigurationHome, Path: root}})
	observed, err := inspectNativeConfiguration(context.Background(), selection)
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
	nativeImportWrite(t, filepath.Join(root, "AGENTS.override.md"), "New override added after review.\n")
	if _, err := executeNativeConfigurationImport(context.Background(), Config{}, job); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("new override did not invalidate all source proof", err)
	}
	job.InstanceID = domain.NewID()
	if _, err := executeNativeConfigurationImport(context.Background(), Config{}, job); err == nil {
		t.Fatal("replacement process reread original scope")
	}
}
func TestNativeImportRejectsLinksBoundsAndSecretInstructionFallback(t *testing.T) {
	for _, scenario := range []string{"link", "oversize", "malformed", "secret-override", "outside-project-chain"} {
		t.Run(scenario, func(t *testing.T) {
			root := nativeImportDirectory(t)
			selection := nativeImportReadSelection([]domain.NativeConfigurationScope{{Kind: domain.NativeConfigurationHome, Path: root}})
			switch scenario {
			case "link":
				outside := nativeImportDirectory(t)
				nativeImportWrite(t, filepath.Join(outside, "auth.json"), "model = 'never-read'")
				if err := os.Symlink(filepath.Join(outside, "auth.json"), filepath.Join(root, "config.toml")); err != nil {
					t.Skip("symlink unavailable", err)
				}
			case "oversize":
				nativeImportWrite(t, filepath.Join(root, "AGENTS.md"), strings.Repeat("x", domain.MaxNativeConfigurationFileBytes+1))
			case "malformed":
				nativeImportWrite(t, filepath.Join(root, "config.toml"), "model = 'first'\nmodel = 'second'\n")
			case "secret-override":
				nativeImportWrite(t, filepath.Join(root, "AGENTS.override.md"), "api_key = sk-fixture-secret")
				nativeImportWrite(t, filepath.Join(root, "AGENTS.md"), "Masked safe fallback.")
			case "outside-project-chain":
				selection.Scopes = []domain.NativeConfigurationScope{{Kind: domain.NativeConfigurationProject, Path: root}, {Kind: domain.NativeConfigurationProject, Path: nativeImportDirectory(t)}}
			}
			result, err := inspectNativeConfiguration(context.Background(), selection)
			if scenario == "secret-override" {
				if err != nil || len(result.Entries) != 1 || result.Entries[0].Kind != domain.NativeConfigurationExcluded {
					t.Fatal("secret override used safe fallback or leaked content", err, result)
				}
			} else if err == nil {
				t.Fatal("unsafe source accepted")
			}
		})
	}
}

func TestNativeImportDistinctBlankOverrideAndIndependentProjectBudget(t *testing.T) {
	home, project := nativeImportDirectory(t), nativeImportDirectory(t)
	nativeImportWrite(t, filepath.Join(home, "AGENTS.override.md"), " \n")
	nativeImportWrite(t, filepath.Join(home, "AGENTS.md"), strings.Repeat("h", 40<<10))
	nativeImportWrite(t, filepath.Join(project, "AGENTS.override.md"), " \n")
	nativeImportWrite(t, filepath.Join(project, "AGENTS.md"), "Must remain masked")
	selection := nativeImportReadSelection([]domain.NativeConfigurationScope{{Kind: domain.NativeConfigurationHome, Path: home}, {Kind: domain.NativeConfigurationProject, Path: project}})
	value, err := inspectNativeConfiguration(context.Background(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Entries) != 1 || len(value.Entries[0].Contents) != 40<<10 || !value.Sources[5].Shadowed {
		t.Fatal("home budget or blank project precedence changed")
	}
	nativeImportWrite(t, filepath.Join(project, "AGENTS.override.md"), strings.Repeat("p", 33<<10))
	if _, err := inspectNativeConfiguration(context.Background(), selection); err == nil {
		t.Fatal("project instruction budget was truncated or exceeded")
	}
}
func TestNativeImportWholeFileSecretsAndExplicitCompleteDisabledPackage(t *testing.T) {
	root := nativeImportDirectory(t)
	nativeImportWrite(t, filepath.Join(root, "config.toml"), "model='must-not-escape'\n\"api_key\"='ghp_fixture'\n")
	nativeImportWrite(t, filepath.Join(root, "skill", "SKILL.md"), "# Fixture skill\n")
	nativeImportWrite(t, filepath.Join(root, "skill", "run.sh"), "#!/bin/sh\nprintf fixture\n")
	if err := os.Chmod(filepath.Join(root, "skill", "run.sh"), 0700); err != nil {
		t.Fatal(err)
	}
	selection := nativeImportReadSelection([]domain.NativeConfigurationScope{{Kind: domain.NativeConfigurationHome, Path: root, Packages: []domain.NativeConfigurationPackageSelection{{Kind: domain.NativePackageSkill, Files: []string{"skill/SKILL.md", "skill/run.sh"}}}}})
	value, err := inspectNativeConfiguration(context.Background(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Entries) != 2 || value.Entries[0].Kind != domain.NativeConfigurationExcluded || len(value.Entries[0].Value) > 0 || len(value.Entries[1].Files) != 2 || !value.Entries[1].Files[1].Executable {
		t.Fatal("whole-file exclusion or complete mode manifest failed")
	}
	raw, _ := json.Marshal(value)
	if bytes.Contains(raw, []byte("must-not-escape")) || bytes.Contains(raw, []byte("ghp_fixture")) {
		t.Fatal("secret source escaped")
	}
	nativeImportWrite(t, filepath.Join(root, "skill", "run.sh"), "TOKEN = 'private'\n")
	value, err = inspectNativeConfiguration(context.Background(), selection)
	if err != nil || value.Entries[1].Kind != domain.NativeConfigurationExcluded || len(value.Entries[1].Files) > 0 {
		t.Fatal("secret-bearing package not rejected as a whole", err)
	}
}

func TestNativeImportExactExtensionDefinitionExcludesUnsupportedValues(t *testing.T) {
	root := nativeImportDirectory(t)
	nativeImportWrite(t, filepath.Join(root, "config.toml"), "unknown_private='must-stay-hidden'\n[mcp_servers.fixture]\ncommand = 'tool'\nargs = ['--safe']\n[other]\nvalue='also-hidden'\n")
	observation, err := inspectNativeConfiguration(context.Background(), nativeImportReadSelection([]domain.NativeConfigurationScope{{Kind: domain.NativeConfigurationHome, Path: root}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range observation.Entries {
		if entry.Kind != domain.NativeConfigurationInertExtension {
			continue
		}
		if len(entry.Files) != 1 || entry.Files[0].Contents != "[mcp_servers.fixture]\ncommand = 'tool'\nargs = ['--safe']\n" {
			t.Fatal("extension raw definition lost or unsupported values copied", entry.Files)
		}
	}
	raw, _ := json.Marshal(observation)
	if bytes.Contains(raw, []byte("must-stay-hidden")) || bytes.Contains(raw, []byte("also-hidden")) {
		t.Fatal("unsupported values reached package preview")
	}
}
