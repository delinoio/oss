// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type gatewayOAuthStatus string

const (
	gatewayNotReady  gatewayOAuthStatus = "notReady"
	gatewayStarted   gatewayOAuthStatus = "started"
	gatewaySucceeded gatewayOAuthStatus = "succeeded"
	gatewayFailed    gatewayOAuthStatus = "failed"
)

type gatewayOAuthPolicy string

const gatewayPassiveDiscarded gatewayOAuthPolicy = "passive-discarded"

func (c *Client) observeGatewayOAuthLocked(native nativewire.Event) (Event, error) {
	var params struct {
		ProviderID string             `json:"providerId"`
		Status     gatewayOAuthStatus `json:"status"`
		AuthURL    json.RawMessage    `json:"authUrl"`
		Error      json.RawMessage    `json:"error"`
	}
	limit := c.nativeFrameLimit()
	if domain.DecodeBounded(native.Params, &params, limit) != nil || domain.Text(params.ProviderID, "native gateway provider", 1024, true) != nil {
		return Event{}, incompatible()
	}
	switch params.Status {
	case gatewayNotReady, gatewayStarted, gatewaySucceeded, gatewayFailed:
	default:
		return Event{}, incompatible()
	}
	// Raw optional fields retain omission versus null only during private validation.
	// No gateway operation exists here. Arrival cannot adopt provider authority or
	// open a URL; a future operation needs its own original admission and receipt.
	for _, raw := range []json.RawMessage{params.AuthURL, params.Error} {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var value string
		if domain.DecodeBounded(raw, &value, limit) != nil || domain.Text(value, "native gateway metadata", limit, false) != nil {
			return Event{}, incompatible()
		}
	}
	if c.logger != nil {
		c.logger.Info("Codex native gateway OAuth status observed", "owner_id", c.ownerID, "status", params.Status, "policy", gatewayPassiveDiscarded)
	}
	return c.metadata(GatewayOAuthStatusDiscarded), nil
}
