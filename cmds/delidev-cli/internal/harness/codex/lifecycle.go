// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Lifecycle processes never execute a thread or need plugin catalog downloads.
// Observe the normalized feature, rather than trusting the requested override.
func (c *Client) verifyLifecyclePlugins(ctx context.Context) (returned error) {
	if c.mode == ThreadProtocol {
		return nil
	}
	defer c.recordFailure(ctx, domain.CodexProfile, &returned)
	response, err := c.wire.Call(ctx, domain.NewID(), "experimentalFeature/list", struct {
		Limit int `json:"limit"`
	}{256})
	if err != nil {
		return err
	}
	if response.ErrorCode != nil {
		return incompatible()
	}
	return validateLifecyclePlugins(response.Result)
}

func validateLifecyclePlugins(raw json.RawMessage) error {
	var observed struct {
		Data []struct {
			Name           string  `json:"name"`
			Stage          string  `json:"stage"`
			DisplayName    *string `json:"displayName"`
			Description    *string `json:"description"`
			Announcement   *string `json:"announcement"`
			Enabled        *bool   `json:"enabled"`
			DefaultEnabled *bool   `json:"defaultEnabled"`
		} `json:"data"`
		Next *string `json:"nextCursor"`
	}
	if domain.Decode(raw, &observed) != nil || len(observed.Data) == 0 || len(observed.Data) > 256 || observed.Next != nil {
		return incompatible()
	}
	seen := make(map[string]bool, len(observed.Data))
	pluginsDisabled := false
	for _, feature := range observed.Data {
		if domain.Text(feature.Name, "native feature", 128, true) != nil || seen[feature.Name] {
			return incompatible()
		}
		seen[feature.Name] = true
		if feature.Name == "plugins" {
			if feature.Enabled == nil || *feature.Enabled {
				return incompatible()
			}
			pluginsDisabled = true
		}
	}
	if !pluginsDisabled {
		return incompatible()
	}
	return nil
}
