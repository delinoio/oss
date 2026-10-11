// SPDX-License-Identifier: Apache-2.0
package skills

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"go.yaml.in/yaml/v3"
)

// OpenCodeSelection is private complete-package evidence. Paths never cross the
// public catalog or execution publication boundary.
type OpenCodeSelection struct {
	NativeSelection
	Description  string `json:"description"`
	Content      string `json:"content"`
	ExplicitOnly bool   `json:"explicit_only"`
}

// OpenCodeSources resolves accepted immutable generations, independently of
// later catalog deletion or enablement. It grants no native invocation authority.
func (m Manager) OpenCodeSources(ctx context.Context, server, worker domain.ID, managed []domain.ManagedSkillPackage, explicit []domain.SkillBinding) ([]NativeSelection, error) {
	out := []NativeSelection{}
	for _, p := range managed {
		if server.Validate() != nil || p.Validate() != nil || p.WorkerDeviceID != worker {
			return nil, unavailable()
		}
		path := filepath.Join(managedRoot(m.Root, server, worker), "retained", string(p.ID), "SKILL.md")
		out = append(out, NativeSelection{Name: p.Name, Path: path, ContentRevision: p.ContentRevision})
	}
	selected, err := m.Resolve(ctx, explicit)
	if err != nil {
		return nil, err
	}
	for _, s := range selected {
		found := false
		for _, prior := range out {
			if strings.EqualFold(prior.Name, s.Name) {
				if prior.Name != s.Name || prior.ContentRevision != s.ContentRevision {
					return nil, unavailable()
				}
				found = true
			}
		}
		if !found {
			out = append(out, s)
		}
	}
	if len(out) > domain.MaxSelectedSkills {
		return nil, bound()
	}
	names := map[string]bool{}
	for _, s := range out {
		if names[strings.ToLower(s.Name)] {
			return nil, unavailable()
		}
		names[strings.ToLower(s.Name)] = true
		if _, err := OpenCodePackage(ctx, s); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Reject native command/config collisions and metadata that the two pinned
// native loaders cannot interpret identically. Keep package bytes unchanged.
func OpenCodePackage(ctx context.Context, source NativeSelection) (OpenCodeSelection, error) {
	result := OpenCodeSelection{NativeSelection: source}
	files, digest, err := packageFiles(ctx, filepath.Dir(source.Path))
	if err != nil || digest != source.ContentRevision || filepath.Base(source.Path) != "SKILL.md" {
		return result, unavailable()
	}
	text := strings.ReplaceAll(string(files["SKILL.md"].Bytes), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return result, unavailable()
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return result, unavailable()
	}
	var document yaml.Node
	if yaml.Unmarshal([]byte(text[4:4+end]), &document) != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return result, unavailable()
	}
	seen := map[string]bool{}
	fields := document.Content[0].Content
	for i := 0; i < len(fields); i += 2 {
		key, value := fields[i], fields[i+1]
		if key.Kind != yaml.ScalarNode || seen[key.Value] {
			return result, unavailable()
		}
		seen[key.Value] = true
		switch key.Value {
		case "name":
			if value.Tag != "!!str" || value.Value != source.Name {
				return result, unavailable()
			}
		case "description":
			if value.Tag != "!!str" {
				return result, unavailable()
			}
			result.Description = value.Value
		case "disable-model-invocation":
			if value.Tag != "!!bool" || value.Decode(&result.ExplicitOnly) != nil {
				return result, unavailable()
			}
		default:
			return result, domain.Fail(domain.Unsupported, "The selected package metadata is unsupported by OpenCode.", "Use a package with verified native name, description and invocation policy.")
		}
	}
	if !seen["name"] || !seen["description"] || domain.Text(source.Name, "native skill name", 128, true) != nil || strings.ContainsAny(source.Name, "/\\ \t\r\n$*?[]{}") || source.Name == "init" || source.Name == "review" || source.Name == "customize-opencode" || domain.Text(result.Description, "native skill description", 4096, true) != nil {
		return result, unavailable()
	}
	// Both inventories discover nested SKILL.md files. Reject extra declarations
	// before native traversal can overwrite a selected name.
	for path := range files {
		if filepath.Base(path) == "SKILL.md" && path != "SKILL.md" {
			return result, unavailable()
		}
	}
	body := text[4+end+4:]
	if strings.HasPrefix(body, "\n") {
		body = body[1:]
	}
	result.Content = body
	return result, nil
}

// The owned config-directory parent is understood by both native loaders. It
// avoids translating legacy skills.paths to the incompatible v2 skills array.
func CopyOpenCodeRuntime(ctx context.Context, sources []NativeSelection, home string) ([]OpenCodeSelection, error) {
	parent := filepath.Join(home, "opencode", "skills")
	if len(sources) == 0 {
		return nil, nil
	}
	if security.PrivateDir(parent) != nil {
		return nil, unavailable()
	}
	out := []OpenCodeSelection{}
	total := 0
	for _, source := range sources {
		selection, err := OpenCodePackage(ctx, source)
		if err != nil {
			return nil, err
		}
		files, digest, err := packageFiles(ctx, filepath.Dir(source.Path))
		if err != nil || digest != source.ContentRevision {
			return nil, unavailable()
		}
		for _, file := range files {
			total += len(file.Bytes)
		}
		if total > MaxTotalBytes {
			return nil, bound()
		}
		target := filepath.Join(parent, source.Name)
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			return nil, unavailable()
		}
		if err := writePackage(ctx, files, digest, target); err != nil {
			return nil, err
		}
		selection.Path = filepath.Join(target, "SKILL.md")
		if _, err := OpenCodePackage(ctx, selection.NativeSelection); err != nil {
			return nil, err
		}
		// Compare the original again after copying; native launch gets no mutable
		// source authority if a catalog or resource changes during the copy.
		if _, err := OpenCodePackage(ctx, source); err != nil {
			return nil, err
		}
		out = append(out, selection)
	}
	return out, VerifyOpenCodeRuntime(ctx, home, out)
}
func VerifyOpenCodeRuntime(ctx context.Context, home string, selected []OpenCodeSelection) error {
	parent := filepath.Join(home, "opencode", "skills")
	if len(selected) == 0 {
		return nil
	}
	entries, err := boundedDirectory(parent, domain.MaxSelectedSkills)
	if err != nil || len(entries) != len(selected) {
		return unavailable()
	}
	names := map[string]bool{}
	for _, selection := range selected {
		if selection.Path != filepath.Join(parent, selection.Name, "SKILL.md") || names[strings.ToLower(selection.Name)] {
			return unavailable()
		}
		actual, err := OpenCodePackage(ctx, selection.NativeSelection)
		if err != nil || actual.Description != selection.Description || actual.Content != selection.Content || actual.ExplicitOnly != selection.ExplicitOnly {
			return unavailable()
		}
		names[strings.ToLower(selection.Name)] = true
	}
	for _, entry := range entries {
		if !entry.IsDir() || !names[strings.ToLower(entry.Name())] {
			return unavailable()
		}
	}
	return nil
}
