// SPDX-License-Identifier: Apache-2.0
package tailscale

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// NewReadyManagerForTest returns an in-memory Manager that reports an enabled,
// Ready access state for the given server and origin. It starts no native
// process, ingress listener or persisted intent, so server-package tests can
// exercise the approval service without an installed Tailscale. Production code
// must obtain a Manager from Open.
func NewReadyManagerForTest(server domain.ID, origin string) *Manager {
	return &Manager{server: server, state: Ready, value: accessIntent{Version: 1, ServerID: server, Enabled: true, Origin: origin}}
}
