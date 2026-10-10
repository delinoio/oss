// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func (c *Client) observeWindowsSandboxLocked(native nativewire.Event) (Event, error) {
	switch native.Method {
	case "windows/worldWritableWarning":
		warning, err := decodeWindowsWorldWritableWarning(native.Params)
		if err != nil {
			return Event{}, err
		}
		event := c.metadata(WindowsSandboxWarningDiscarded)
		event.WindowsSandboxWarning = &warning
		if warning.Classification == WindowsWorldWritableNone || c.lastWindowsSandboxWarning != nil && *c.lastWindowsSandboxWarning == warning {
			return event, nil
		}
		// A generic warning needs no retained paths. Suppress an identical
		// summary replay without inventing a setup, input or execution receipt.
		c.lastWindowsSandboxWarning = &warning
		event.Kind, event.Notice, event.Metadata = NoticeEvent, domain.NativeWarning, ""
		return event, nil
	case "windowsSandbox/setupCompleted":
		setup, err := decodeWindowsSandboxSetupCompleted(native.Params)
		if err != nil {
			return Event{}, err
		}
		// No setup operation owner exists. Discard original error text and
		// classify telemetry without changing privilege, readiness or settings.
		event := c.metadata(WindowsSandboxSetupDiscarded)
		event.WindowsSandboxSetup = &setup
		return event, nil
	default:
		return privateNative(native), nil
	}
}
