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

func (o *inputObserver) verifySkillTool(tool *NativeToolPart) error {
	if tool == nil || tool.Name != "skill" {
		return nil
	}
	var input struct {
		Name string `json:"name"`
	}
	if domain.Decode(tool.Input, &input) != nil || input.Name == "" {
		return observerProblem()
	}
	var selected *skills.OpenCodeSelection
	for i := range o.skillPackages {
		if o.skillPackages[i].Name == input.Name {
			if selected != nil {
				return observerProblem()
			}
			selected = &o.skillPackages[i]
		}
	}
	if selected == nil || selected.ExplicitOnly {
		return observerProblem()
	}
	resources, err := skills.OpenCodeResources(context.Background(), *selected)
	if err != nil {
		return observerProblem()
	}
	if tool.State != ToolCompleted {
		return nil
	}
	var metadata struct {
		Name      string `json:"name"`
		Directory string `json:"dir"`
	}
	if domain.Decode(tool.Metadata, &metadata) != nil || metadata.Name != selected.Name || metadata.Directory != filepath.Dir(selected.Path) || tool.Title == nil || *tool.Title != "Loaded skill: "+selected.Name || tool.Output == nil || len(tool.Attachments) != 0 {
		return observerProblem()
	}
	prefix := "<skill_content name=\"" + selected.Name + "\">\n# Skill: " + selected.Name + "\n\n" + strings.TrimSpace(selected.Content) + "\n\nBase directory for this skill: " + metadata.Directory + "\nRelative paths in this skill (e.g., scripts/, reference/) are relative to this base directory.\nNote: file list is sampled.\n\n<skill_files>\n"
	suffix := "\n</skill_files>\n</skill_content>"
	if !strings.HasPrefix(*tool.Output, prefix) || !strings.HasSuffix(*tool.Output, suffix) {
		return observerProblem()
	}
	list := strings.TrimSuffix(strings.TrimPrefix(*tool.Output, prefix), suffix)
	if list == "" {
		return nil
	}
	entries := strings.Split(list, "\n")
	if len(entries) > 10 {
		return observerProblem()
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry, "<file>") || !strings.HasSuffix(entry, "</file>") {
			return observerProblem()
		}
		path := strings.TrimSuffix(strings.TrimPrefix(entry, "<file>"), "</file>")
		if seen[path] || !slices.Contains(resources, path) {
			return observerProblem()
		}
		seen[path] = true
	}
	return nil
}
func checkpointInlineSkill(tool *NativeToolPart) bool {
	if tool == nil || tool.Name != "skill" || tool.State != ToolCompleted || tool.Output == nil || tool.Title == nil {
		return false
	}
	var input struct {
		Name string `json:"name"`
	}
	var metadata struct {
		Name      string `json:"name"`
		Directory string `json:"dir"`
	}
	return domain.Decode(tool.Input, &input) == nil && domain.Decode(tool.Metadata, &metadata) == nil && input.Name == metadata.Name && input.Name != "" && checkpointPath(metadata.Directory) && *tool.Title == "Loaded skill: "+input.Name
}
