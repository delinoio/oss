// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
)

func TestSkillCommandUsesOriginalSelectedBindingAndOrderedArguments(t *testing.T) {
	binding := domain.SkillBinding{ContentRevision: strings.Repeat("a", 64)}
	selected := skills.OpenCodeSelection{NativeSelection: skills.NativeSelection{Name: "selected", Path: "/private/opencode/skills/selected/SKILL.md", ContentRevision: binding.ContentRevision}, Content: "Use $1 then $2. Raw: $ARGUMENTS"}
	input := domain.SessionInput{Prompt: "$selected first \"second argument\" third", Skills: []domain.SkillBinding{binding}}
	command, err := PrepareSkillCommand(input, []skills.OpenCodeSelection{selected})
	if err != nil || command == nil || command.Arguments != "first \"second argument\" third" || !strings.Contains(command.Text, "Use first then second argument third. Raw: first \"second argument\" third") {
		t.Fatalf("native ordered arguments: %#v %v", command, err)
	}
	input.Skills = nil
	command, err = PrepareSkillCommand(input, []skills.OpenCodeSelection{selected})
	if err != nil || command != nil {
		t.Fatal("manually typed token acquired native invocation authority")
	}
}
func TestSkillCommandRejectsUnsupportedInputBeforeNativeSend(t *testing.T) {
	binding := domain.SkillBinding{ContentRevision: strings.Repeat("a", 64)}
	selected := skills.OpenCodeSelection{NativeSelection: skills.NativeSelection{Name: "selected", Path: "/private/opencode/skills/selected/SKILL.md", ContentRevision: binding.ContentRevision}, Content: "Instructions."}
	for _, input := range []domain.SessionInput{
		{Prompt: "$selected arguments", Skills: []domain.SkillBinding{binding, binding}},
		{Prompt: "$foreign arguments", Skills: []domain.SkillBinding{binding}},
		{Prompt: "$selected @foreign", Skills: []domain.SkillBinding{binding}},
	} {
		if _, err := PrepareSkillCommand(input, []skills.OpenCodeSelection{selected}); err == nil {
			t.Fatal("unsupported command became sendable")
		}
	}
	selected.Content = "Run !`echo fixture`"
	if _, err := PrepareSkillCommand(domain.SessionInput{Prompt: "$selected", Skills: []domain.SkillBinding{binding}}, []skills.OpenCodeSelection{selected}); err == nil {
		t.Fatal("pre-message shell substitution became sendable")
	}
}

func TestSkillCommandRejectsArgumentDerivedNativeExpansion(t *testing.T) {
	binding := domain.SkillBinding{ContentRevision: strings.Repeat("a", 64)}
	for _, scenario := range []struct{ content, arguments string }{
		{"Instructions.", "!`echo fixture`"},
		{"Use $ARGUMENTS", "!`echo fixture`"},
		{"Use $1$2", "! `echo fixture`"},
		{"Use $1", "@foreign"},
	} {
		selected := skills.OpenCodeSelection{NativeSelection: skills.NativeSelection{Name: "selected", Path: "/private/opencode/skills/selected/SKILL.md", ContentRevision: binding.ContentRevision}, Content: scenario.content}
		if command, err := PrepareSkillCommand(domain.SessionInput{Prompt: "$selected " + scenario.arguments, Skills: []domain.SkillBinding{binding}}, []skills.OpenCodeSelection{selected}); err == nil || command != nil {
			t.Fatal("argument-derived expansion acquired native send authority")
		}
	}
}

func TestSkillCommandPreservesNativeInnerQuotes(t *testing.T) {
	selection := skills.OpenCodeSelection{NativeSelection: skills.NativeSelection{Name: "selected", Path: "/private/opencode/skills/selected/SKILL.md"}, Content: "Use $1"}
	command, err := nativeSkillCommand(selection, "\"'literal'\"")
	if err != nil || !strings.HasPrefix(command.Text, "Use 'literal'") {
		t.Fatal("native inner quote semantics changed", command, err)
	}
}
