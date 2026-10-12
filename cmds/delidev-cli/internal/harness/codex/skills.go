// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
	"path/filepath"
	"slices"
	"strings"
)

func (c *Client) selectedSkillInputs(ctx context.Context, input domain.SessionInput) ([]nativeTextInput, error) {
	if len(input.Skills) == 0 && len(c.managedSkills) == 0 {
		return nil, nil
	}
	if c.skillsRoot == "" || c.home == "" {
		return nil, incompatible()
	}
	selected, e := (skills.Manager{Root: c.skillsRoot}).CopyToRuntime(ctx, input.Skills, c.home)
	if e != nil {
		return nil, e
	}
	if len(c.managedSkills) > 0 {
		if e := c.verifyManagedSkillDiscovery(ctx); e != nil {
			return nil, e
		}
	}
	if e := skills.VerifyManagedRuntime(ctx, c.home, c.managedSkills); e != nil {
		return nil, e
	}
	all := append([]skills.NativeSelection{}, selected...)
	for _, managed := range c.managedSkills {
		matched := false
		for _, explicit := range selected {
			if strings.EqualFold(explicit.Name, managed.Name) {
				if explicit.ContentRevision != managed.ContentRevision {
					return nil, incompatible()
				}
				matched = true
			}
		}
		if !matched {
			all = append(all, managed)
		}
	}
	names := map[string]string{}
	roots := []string{}
	for _, s := range all {
		key := strings.ToLower(s.Name)
		if prior, exists := names[key]; exists && !nativePathEqual(prior, s.Path) {
			return nil, incompatible()
		}
		names[key] = s.Path
		if !slices.Contains(roots, filepath.Dir(s.Path)) {
			roots = append(roots, filepath.Dir(s.Path))
		}
	}
	result, e := c.wire.Call(ctx, domain.NewID(), "skills/extraRoots/set", struct {
		ExtraRoots []string `json:"extraRoots"`
	}{roots})
	if e != nil {
		return nil, e
	}
	var empty struct{}
	if domain.Decode(result.Result, &empty) != nil {
		return nil, incompatible()
	}
	listed, e := c.wire.Call(ctx, domain.NewID(), "skills/list", struct {
		Cwds        []string `json:"cwds"`
		ForceReload bool     `json:"forceReload"`
	}{[]string{c.execution.settings.Cwd}, true})
	if e != nil {
		return nil, e
	}
	var response struct {
		Data []struct {
			Cwd    string `json:"cwd"`
			Skills []struct {
				Name             string          `json:"name"`
				Description      string          `json:"description"`
				ShortDescription *string         `json:"shortDescription,omitempty"`
				Interface        json.RawMessage `json:"interface,omitempty"`
				Dependencies     json.RawMessage `json:"dependencies,omitempty"`
				Path             string          `json:"path"`
				Scope            string          `json:"scope"`
				Enabled          bool            `json:"enabled"`
				PluginID         *string         `json:"pluginId"`
			} `json:"skills"`
			Errors []json.RawMessage `json:"errors"`
		} `json:"data"`
	}
	if domain.Decode(listed.Result, &response) != nil || len(response.Data) != 1 || !nativePathEqual(response.Data[0].Cwd, c.execution.settings.Cwd) || len(response.Data[0].Errors) != 0 || len(response.Data[0].Skills) > 256 {
		return nil, incompatible()
	}
	if len(c.managedSkills) > 0 {
		for _, entry := range response.Data[0].Skills {
			if !entry.Enabled {
				continue
			}
			owned := slices.ContainsFunc(all, func(selection skills.NativeSelection) bool {
				return selection.Name == entry.Name && nativePathEqual(selection.Path, entry.Path)
			})
			if !owned {
				return nil, incompatible()
			}
		}
	}
	out := []nativeTextInput{}
	for i, selection := range all {
		matches := 0
		for _, entry := range response.Data[0].Skills {
			// A foreign enabled package with an equal native name can shadow or
			// compete with the selected package, even when the exact path exists.
			if entry.Enabled && strings.EqualFold(entry.Name, selection.Name) && (!nativePathEqual(entry.Path, selection.Path) || entry.Name != selection.Name) {
				return nil, incompatible()
			}
			if nativePathEqual(entry.Path, selection.Path) && entry.Name == selection.Name && entry.Enabled {
				matches++
			}
		}
		if matches != 1 {
			return nil, incompatible()
		}
		if i < len(selected) {
			out = append(out, nativeTextInput{Type: "skill", Name: selection.Name, Path: selection.Path})
		}
	}
	// Managed packages remain automatic discoveries; only explicit selections
	// produce structured input. Native invocation policy is read from package
	// resources, and literal prompt tokens are never converted into selections.
	if e := skills.VerifyManagedRuntime(ctx, c.home, c.managedSkills); e != nil {
		return nil, e
	}
	// Recheck immutable snapshots after native discovery, before turn/start.
	if _, e = (skills.Manager{Root: c.skillsRoot}).Resolve(ctx, input.Skills); e != nil {
		return nil, e
	}
	return out, nil
}

func nativeSkillDigest(selected []nativeTextInput) [32]byte {
	if len(selected) == 0 {
		return [32]byte{}
	}
	b, _ := json.Marshal(selected)
	return sha256.Sum256(b)
}

// SkillInputProofs are private native-history evidence retained before cleanup.
func (c *Client) SkillInputProofs(ctx context.Context) ([]HistoricalInput, error) {
	if e := c.acquireControl(ctx); e != nil {
		return nil, e
	}
	defer func() { <-c.control }()
	if c.execution == nil {
		return nil, incompatible()
	}
	out := []HistoricalInput{}
	for id, attempt := range c.execution.inputs {
		out = append(out, HistoricalInput{ID: id, PromptDigest: attempt.Digest, SkillDigest: attempt.SkillDigest})
	}
	return out, nil
}

// Native skill parts are authenticated separately from text and image digests.
// Remove only structured parts, retaining the exact text/image order and bytes.
func nativeInputSkills(parts []json.RawMessage) ([]json.RawMessage, []nativeTextInput, error) {
	plain := []json.RawMessage{}
	selected := []nativeTextInput{}
	if len(parts) > domain.MaxInputImages+17 {
		return nil, nil, incompatible()
	}
	for _, raw := range parts {
		var kind struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &kind) != nil {
			return nil, nil, incompatible()
		}
		if kind.Type != "skill" {
			plain = append(plain, raw)
			continue
		}
		var part nativeTextInput
		if domain.Decode(raw, &part) != nil || part.Name == "" || part.Path == "" || part.Text != "" || len(selected) >= 16 {
			return nil, nil, incompatible()
		}
		selected = append(selected, part)
	}
	return plain, selected, nil
}

// Skip ordinary host/project discovery only for an explicit managed generation.
// Old Agents keep their historical behavior; selected packages still use the
// native engine's own discovery and per-package invocation policy.
func (c *Client) verifyManagedSkillDiscovery(ctx context.Context) error {
	response, err := c.wire.Call(ctx, domain.NewID(), "config/read", struct {
		Cwd           string `json:"cwd"`
		IncludeLayers bool   `json:"includeLayers"`
	}{Cwd: c.execution.settings.Cwd})
	if err != nil {
		return err
	}
	var result struct {
		Config  map[string]json.RawMessage `json:"config"`
		Origins json.RawMessage            `json:"origins"`
		Layers  json.RawMessage            `json:"layers"`
	}
	if domain.Decode(response.Result, &result) != nil || result.Config == nil {
		return incompatible()
	}
	var features map[string]json.RawMessage
	if domain.Decode(result.Config["features"], &features) != nil || strings.TrimSpace(string(features["skip_host_skill_discovery"])) != "true" {
		return incompatible()
	}
	return nil
}
