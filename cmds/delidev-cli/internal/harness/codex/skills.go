// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
	"path/filepath"
)

func (c *Client) selectedSkillInputs(ctx context.Context, input domain.SessionInput) ([]nativeTextInput, error) {
	if len(input.Skills) == 0 {
		return nil, nil
	}
	if c.skillsRoot == "" || c.home == "" {
		return nil, incompatible()
	}
	selected, e := (skills.Manager{Root: c.skillsRoot}).CopyToRuntime(ctx, input.Skills, c.home)
	if e != nil {
		return nil, e
	}
	roots := []string{}
	for _, s := range selected {
		roots = append(roots, filepath.Dir(s.Path))
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
	out := []nativeTextInput{}
	for _, selection := range selected {
		matches := 0
		for _, entry := range response.Data[0].Skills {
			if nativePathEqual(entry.Path, selection.Path) && entry.Name == selection.Name && entry.Enabled {
				matches++
			}
		}
		if matches != 1 {
			return nil, incompatible()
		}
		out = append(out, nativeTextInput{Type: "skill", Name: selection.Name, Path: selection.Path})
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
