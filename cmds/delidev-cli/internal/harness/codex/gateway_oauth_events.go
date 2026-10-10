// SPDX-License-Identifier: Apache-2.0
package codex

import "github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"

func (c *Client) observeGatewayOAuthLocked(native nativewire.Event) (Event, error) {
	value, err := decodeGatewayOAuthNotification(native.Params)
	if err != nil {
		return Event{}, err
	}
	// This adapter owns no gateway operation. Discard provider, URL and error
	// strings without interpreting them or adopting account/configuration state.
	event := c.metadata(GatewayOAuthStatusDiscarded)
	event.GatewayOAuthStatus = value.status
	return event, nil
}
