// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
)

func nativeSkillToolFixture(t *testing.T) (*inputObserver, *NativeToolPart) {
	t.Helper()
	source := t.TempDir()
	content := "Original native skill instructions.\n"
	markdown := []byte("---\nname: selected\ndescription: Fixture\n---\n" + content)
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), markdown, 0600); err != nil {
		t.Fatal(err)
	}
	file := sha256.Sum256(markdown)
	record, _ := json.Marshal([]any{"SKILL.md", hex.EncodeToString(file[:]), false})
	revision := sha256.Sum256(record)
	selected, err := skills.CopyOpenCodeRuntime(context.Background(), []skills.NativeSelection{{Name: "selected", Path: filepath.Join(source, "SKILL.md"), ContentRevision: hex.EncodeToString(revision[:])}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Dir(selected[0].Path)
	title := "Loaded skill: selected"
	output := "<skill_content name=\"selected\">\n# Skill: selected\n\nOriginal native skill instructions.\n\nBase directory for this skill: " + directory + "\nRelative paths in this skill (e.g., scripts/, reference/) are relative to this base directory.\nNote: file list is sampled.\n\n<skill_files>\n\n</skill_files>\n</skill_content>"
	metadata, _ := json.Marshal(map[string]string{"name": "selected", "dir": directory})
	return &inputObserver{skillPackages: selected}, &NativeToolPart{Name: "skill", State: ToolCompleted, Input: json.RawMessage(`{"name":"selected"}`), Metadata: metadata, Title: &title, Output: &output}
}
func TestNativeSkillToolMatchesCompleteOwnedPackageAndPolicy(t *testing.T) {
	for _, mutation := range []string{"original", "foreign-name", "foreign-dir", "changed-output", "explicit-only", "changed-resource"} {
		t.Run(mutation, func(t *testing.T) {
			o, tool := nativeSkillToolFixture(t)
			switch mutation {
			case "foreign-name":
				tool.Input = json.RawMessage(`{"name":"unselected"}`)
			case "foreign-dir":
				tool.Metadata = json.RawMessage(`{"name":"selected","dir":"/foreign"}`)
			case "changed-output":
				changed := *tool.Output + "foreign"
				tool.Output = &changed
			case "explicit-only":
				o.skillPackages[0].ExplicitOnly = true
			case "changed-resource":
				if err := os.WriteFile(filepath.Join(filepath.Dir(o.skillPackages[0].Path), "foreign"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := o.verifySkillTool(tool)
			if (err == nil) != (mutation == "original") {
				t.Fatalf("owned native tool %s: %v", mutation, err)
			}
		})
	}
}
func TestNativeSkillPendingCannotGrantPackageAuthority(t *testing.T) {
	o := &inputObserver{}
	pending := &NativeToolPart{Name: "skill", State: ToolPending, Input: json.RawMessage(`{}`)}
	if err := o.verifySkillTool(pending); err != nil {
		t.Fatal("native empty pending envelope was rejected", err)
	}
	pending.State = ToolRunning
	pending.Input = json.RawMessage(`{"name":"foreign"}`)
	if o.verifySkillTool(pending) == nil {
		t.Fatal("running foreign package acquired authority")
	}
}

func TestForkSkillToolAuthenticatesCurrentCopyAtRetainedDirectory(t *testing.T) {
	observer, tool := nativeSkillToolFixture(t)
	currentDirectory := filepath.Dir(observer.skillPackages[0].Path)
	retiredDirectory := filepath.Join(t.TempDir(), "retired", "opencode", "skills", "selected")
	output := strings.Replace(*tool.Output, currentDirectory, retiredDirectory, 1)
	tool.Output = &output
	tool.Metadata, _ = json.Marshal(map[string]string{"name": "selected", "dir": retiredDirectory})
	if observer.verifySkillTool(tool) == nil {
		t.Fatal("ordinary observation accepted a historical directory")
	}
	if err := observer.verifySkillToolAtDirectory(tool, retiredDirectory); err != nil {
		t.Fatal("owned current copy could not authenticate retained output", err)
	}
	if err := os.WriteFile(filepath.Join(currentDirectory, "foreign"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if observer.verifySkillToolAtDirectory(tool, retiredDirectory) == nil {
		t.Fatal("changed current package authenticated historical output")
	}
}
