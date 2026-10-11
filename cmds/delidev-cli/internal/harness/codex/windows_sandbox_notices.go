// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

const (
	maxWindowsWarningPaths                      = 1024
	maxWindowsSandboxText                       = 4096
	WindowsSandboxWarningDiscarded MetadataKind = "windows-sandbox-warning-discarded"
	WindowsSandboxSetupDiscarded   MetadataKind = "windows-sandbox-setup-discarded"
)

type windowsWarningClass uint8

const (
	windowsNoWarning windowsWarningClass = iota
	windowsWorldWritable
	windowsScanFailed
	windowsWorldWritableScanFailed
)

func (c windowsWarningClass) String() string {
	switch c {
	case windowsWorldWritable:
		return "world-writable"
	case windowsScanFailed:
		return "scan-failed"
	case windowsWorldWritableScanFailed:
		return "world-writable-scan-failed"
	default:
		return "none"
	}
}

// Only finite counts and a closed classification survive private decoding.
// This file is OS-neutral so non-Windows hosts enforce the same wire boundary.
type windowsWarningObservation struct {
	class   windowsWarningClass
	samples uint16
	extra   uint64
}

type windowsSandboxMode string

const (
	windowsSandboxElevated   windowsSandboxMode = "elevated"
	windowsSandboxUnelevated windowsSandboxMode = "unelevated"
)

func windowsSandboxObject(raw json.RawMessage, required []string, optional string) error {
	if len(raw) > nativewire.MaxFrame {
		return incompatible()
	}
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &fields) != nil || fields == nil {
		return incompatible()
	}
	for _, key := range required {
		value, exists := fields[key]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return incompatible()
		}
	}
	for key := range fields {
		known := optional != "" && key == optional
		for _, requiredKey := range required {
			known = known || key == requiredKey
		}
		if !known {
			return incompatible()
		}
	}
	return nil
}

func decodeWindowsWarning(raw json.RawMessage) (windowsWarningObservation, error) {
	if windowsSandboxObject(raw, []string{"samplePaths", "extraCount", "failedScan"}, "") != nil {
		return windowsWarningObservation{}, incompatible()
	}
	var params struct {
		Paths  []*string `json:"samplePaths"`
		Extra  uint64    `json:"extraCount"`
		Failed bool      `json:"failedScan"`
	}
	if domain.Decode(raw, &params) != nil || len(params.Paths) > maxWindowsWarningPaths {
		return windowsWarningObservation{}, incompatible()
	}
	for _, path := range params.Paths {
		if path == nil || domain.Text(*path, "native Windows path", maxWindowsSandboxText, false) != nil {
			return windowsWarningObservation{}, incompatible()
		}
	}
	observed := windowsWarningObservation{samples: uint16(len(params.Paths)), extra: params.Extra}
	if observed.samples > 0 || observed.extra > 0 {
		observed.class = windowsWorldWritable
	}
	if params.Failed {
		observed.class += windowsScanFailed
	}
	return observed, nil
}

func validateWindowsSetup(raw json.RawMessage) error {
	if windowsSandboxObject(raw, []string{"mode", "success"}, "error") != nil {
		return incompatible()
	}
	var params struct {
		Mode    windowsSandboxMode `json:"mode"`
		Success bool               `json:"success"`
		Error   *string            `json:"error"`
	}
	if domain.Decode(raw, &params) != nil || (params.Mode != windowsSandboxElevated && params.Mode != windowsSandboxUnelevated) {
		return incompatible()
	}
	if params.Error != nil && domain.Text(*params.Error, "native Windows setup error", maxWindowsSandboxText, false) != nil {
		return incompatible()
	}
	return nil
}

func (c *Client) observeWindowsSandboxLocked(native nativewire.Event) (Event, error) {
	if native.Kind != nativewire.Notification || c.execution == nil || c.execution.thread.ID != c.thread {
		return privateNative(native), nil
	}
	if native.Method == "windowsSandbox/setupCompleted" {
		if validateWindowsSetup(native.Params) != nil {
			return Event{}, incompatible()
		}
		// No setup request owner exists in this adapter. Completion cannot prove
		// readiness, privilege, permission, recovery or native/process cleanup.
		return c.metadata(WindowsSandboxSetupDiscarded), nil
	}
	observed, err := decodeWindowsWarning(native.Params)
	if err != nil {
		return Event{}, err
	}
	event := c.metadata(WindowsSandboxWarningDiscarded)
	event.windowsWarning = &observed
	turn, known := c.execution.turns[c.execution.active]
	if observed.class == windowsNoWarning || !known || turn.Turn.Status.terminal() || c.execution.paused || c.problem != nil {
		return event, nil
	}
	// Process-local classes have only three possible warning values. A replay
	// cannot flood the transcript or make a terminal/failed context current.
	bit := uint8(1 << observed.class)
	if c.execution.windowsWarnings&bit != 0 {
		return event, nil
	}
	c.execution.windowsWarnings |= bit
	event.Kind, event.Notice, event.Metadata = NoticeEvent, domain.NativeWarning, ""
	event.TurnID = c.execution.active
	return event, nil
}
