// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type windowsWarningClass uint8

const (
	windowsWritablePaths              windowsWarningClass = 1
	windowsScanFailed                 windowsWarningClass = 2
	windowsWritablePathsAndScanFailed windowsWarningClass = 3
)

type windowsWarningObservation struct {
	Class       windowsWarningClass
	SampleCount uint32
	ExtraCount  uint64
}

type windowsSetupMode string

const (
	windowsSetupElevated   windowsSetupMode = "elevated"
	windowsSetupUnelevated windowsSetupMode = "unelevated"
)

func (c *Client) observeWindowsLocked(native nativewire.Event) (Event, error) {
	switch native.Method {
	case "windows/worldWritableWarning":
		var params struct {
			SamplePaths []string `json:"samplePaths"`
			ExtraCount  *uint64  `json:"extraCount"`
			FailedScan  *bool    `json:"failedScan"`
		}
		if domain.Decode(native.Params, &params) != nil || params.SamplePaths == nil || len(params.SamplePaths) > 1000 || params.ExtraCount == nil || params.FailedScan == nil {
			return Event{}, incompatible()
		}
		for _, path := range params.SamplePaths {
			if domain.Text(path, "private Windows warning path", 4096, true) != nil {
				return Event{}, incompatible()
			}
		}
		var class windowsWarningClass
		if len(params.SamplePaths) > 0 || *params.ExtraCount > 0 {
			class = windowsWritablePaths
		}
		if *params.FailedScan {
			class |= windowsScanFailed
		}
		// This process-wide advisory has no native event identity. Publish at most
		// one generic notice per closed classification on the original connection;
		// raw paths are neither replay keys nor retained/public warning content.
		if class == 0 {
			return c.metadata(WindowsWarningDiscarded), nil
		}
		turn, known := c.execution.turns[c.execution.active]
		if c.problem != nil || c.execution.paused || !known || turn.Turn.Status.terminal() {
			return c.metadata(WindowsWarningDiscarded), nil
		}
		if c.windowsWarnings == nil {
			c.windowsWarnings = make(map[windowsWarningClass]bool)
		}
		if c.windowsWarnings[class] {
			return c.metadata(WindowsWarningReplayChecked), nil
		}
		c.windowsWarnings[class] = true
		return Event{Kind: NoticeEvent, ThreadID: c.thread, Notice: domain.NativeWarning, Correlated: true, WindowsWarning: &windowsWarningObservation{Class: class, SampleCount: uint32(len(params.SamplePaths)), ExtraCount: *params.ExtraCount}}, nil
	case "windowsSandbox/setupCompleted":
		var params struct {
			Mode    *windowsSetupMode `json:"mode"`
			Success *bool             `json:"success"`
			Error   json.RawMessage   `json:"error"`
		}
		if domain.Decode(native.Params, &params) != nil || params.Mode == nil || (*params.Mode != windowsSetupElevated && *params.Mode != windowsSetupUnelevated) || params.Success == nil || len(params.Error) == 0 {
			return Event{}, incompatible()
		}
		if !bytes.Equal(bytes.TrimSpace(params.Error), []byte("null")) {
			var diagnostic string
			if domain.Decode(params.Error, &diagnostic) != nil || domain.Text(diagnostic, "private Windows setup diagnostic", 4096, false) != nil {
				return Event{}, incompatible()
			}
		}
		// No dedicated setup operation owner exists. Neither success nor elevated
		// mode proves readiness, privilege, input acceptance or successful cleanup.
		// An eventual owner must bind its original operation and independently
		// verify readiness before consuming this private completion.
		return c.metadata(WindowsSetupDiscarded), nil
	default:
		return Event{}, incompatible()
	}
}
