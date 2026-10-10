// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type WindowsSandboxWarningClassification string

const (
	WindowsWorldWritablePaths        WindowsSandboxWarningClassification = "world-writable-paths"
	WindowsWorldWritableScanFailed   WindowsSandboxWarningClassification = "scan-failed"
	WindowsWorldWritablePathsAndScan WindowsSandboxWarningClassification = "paths-and-scan-failed"
	WindowsWorldWritableNone         WindowsSandboxWarningClassification = "no-warning"
)

// WindowsSandboxWarning contains only bounded counts and a closed classification.
// Neither the warning nor native scan results prove sandbox readiness or authority.
type WindowsSandboxWarning struct {
	Classification WindowsSandboxWarningClassification
	SampleCount    uint32
	ExtraCount     uint64
	FailedScan     bool
}

func (v WindowsSandboxWarning) classification() WindowsSandboxWarningClassification {
	paths := v.SampleCount != 0 || v.ExtraCount != 0
	switch {
	case paths && v.FailedScan:
		return WindowsWorldWritablePathsAndScan
	case paths:
		return WindowsWorldWritablePaths
	case v.FailedScan:
		return WindowsWorldWritableScanFailed
	default:
		return WindowsWorldWritableNone
	}
}

func (v WindowsSandboxWarning) Valid() bool {
	return v.SampleCount <= 1000 && v.Classification == v.classification()
}

type WindowsSandboxSetupMode string

const (
	WindowsSandboxElevated   WindowsSandboxSetupMode = "elevated"
	WindowsSandboxUnelevated WindowsSandboxSetupMode = "unelevated"
)

// WindowsSandboxSetupObservation is unsolicited telemetry, never a setup receipt.
type WindowsSandboxSetupObservation struct {
	Mode    WindowsSandboxSetupMode
	Success bool
}

func (v WindowsSandboxSetupObservation) Valid() bool {
	return v.Mode == WindowsSandboxElevated || v.Mode == WindowsSandboxUnelevated
}

func decodeWindowsWorldWritableWarning(raw []byte) (WindowsSandboxWarning, error) {
	var wire struct {
		SamplePaths []string `json:"samplePaths"`
		ExtraCount  *uint64  `json:"extraCount"`
		FailedScan  *bool    `json:"failedScan"`
	}
	if domain.DecodeWithLimit(raw, &wire, nativewire.MaxFrame) != nil || wire.SamplePaths == nil || len(wire.SamplePaths) > 1000 || wire.ExtraCount == nil || wire.FailedScan == nil {
		return WindowsSandboxWarning{}, invalidWindowsSandboxNotification()
	}
	for _, path := range wire.SamplePaths {
		if domain.Text(path, "private native Windows warning path", 4096, true) != nil {
			return WindowsSandboxWarning{}, invalidWindowsSandboxNotification()
		}
	}
	value := WindowsSandboxWarning{SampleCount: uint32(len(wire.SamplePaths)), ExtraCount: *wire.ExtraCount, FailedScan: *wire.FailedScan}
	value.Classification = value.classification()
	return value, nil
}

func decodeWindowsSandboxSetupCompleted(raw []byte) (WindowsSandboxSetupObservation, error) {
	var wire struct {
		Mode    *WindowsSandboxSetupMode `json:"mode"`
		Success *bool                    `json:"success"`
		Error   json.RawMessage          `json:"error"`
	}
	if domain.DecodeWithLimit(raw, &wire, nativewire.MaxFrame) != nil || wire.Mode == nil || wire.Success == nil {
		return WindowsSandboxSetupObservation{}, invalidWindowsSandboxNotification()
	}
	if len(wire.Error) != 0 {
		var privateError *string
		if domain.DecodeWithLimit(wire.Error, &privateError, nativewire.MaxFrame) != nil || privateError != nil && domain.Text(*privateError, "private native Windows setup error", nativewire.MaxFrame, false) != nil {
			return WindowsSandboxSetupObservation{}, invalidWindowsSandboxNotification()
		}
	}
	value := WindowsSandboxSetupObservation{Mode: *wire.Mode, Success: *wire.Success}
	if !value.Valid() {
		return WindowsSandboxSetupObservation{}, invalidWindowsSandboxNotification()
	}
	return value, nil
}

func invalidWindowsSandboxNotification() error {
	return domain.Fail(domain.Unsupported, "The native Windows sandbox notification does not match its closed protocol shape.", "Preserve original input and cleanup ownership; no privileged setup was authorized.")
}
