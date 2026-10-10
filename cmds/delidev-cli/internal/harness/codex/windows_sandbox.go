// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type WindowsWarningKind string

const (
	WorldWritablePaths              WindowsWarningKind = "world-writable-paths"
	WorldWritableScanFailed         WindowsWarningKind = "world-writable-scan-failed"
	WorldWritablePathsAndScanFailed WindowsWarningKind = "world-writable-paths-and-scan-failed"
)

// Only redacted classification and finite counts survive native decoding.
type WindowsWarning struct {
	Kind        WindowsWarningKind
	SampleCount uint32
	ExtraCount  uint64
}
type windowsSetupMode string

const (
	windowsElevated   windowsSetupMode = "elevated"
	windowsUnelevated windowsSetupMode = "unelevated"
)

func (c *Client) observeWindowsSandboxLocked(native nativewire.Event) (Event, error) {
	if native.Method == "windowsSandbox/setupCompleted" {
		var params *struct {
			Mode    *windowsSetupMode `json:"mode"`
			Success *bool             `json:"success"`
			Error   *string           `json:"error"`
		}
		if !windowsFields(native.Params, "mode", "success", "error") || domain.Decode(native.Params, &params) != nil || params == nil || params.Mode == nil || (*params.Mode != windowsElevated && *params.Mode != windowsUnelevated) || params.Success == nil || params.Error != nil && domain.Text(*params.Error, "native sandbox error", 4096, false) != nil {
			return Event{}, incompatible()
		}
		// There is no admitted product setup owner. Native completion cannot
		// establish readiness, privilege, recovery or successful cleanup.
		return c.metadata(WindowsSandboxDiscarded), nil
	}
	var params *struct {
		SamplePaths []*string `json:"samplePaths"`
		ExtraCount  *uint64   `json:"extraCount"`
		FailedScan  *bool     `json:"failedScan"`
	}
	if !windowsFields(native.Params, "samplePaths", "extraCount", "failedScan") || domain.Decode(native.Params, &params) != nil || params == nil || params.SamplePaths == nil || len(params.SamplePaths) > 1024 || params.ExtraCount == nil || params.FailedScan == nil {
		return Event{}, incompatible()
	}
	for _, path := range params.SamplePaths {
		if path == nil || domain.Text(*path, "native sandbox sample path", 4096, true) != nil {
			return Event{}, incompatible()
		}
	}
	paths := len(params.SamplePaths) > 0 || *params.ExtraCount > 0
	if !paths && !*params.FailedScan {
		return c.metadata(WindowsSandboxDiscarded), nil
	}
	kind := WorldWritablePaths
	if *params.FailedScan {
		kind = WorldWritableScanFailed
		if paths {
			kind = WorldWritablePathsAndScanFailed
		}
	}
	// A generic warning has no original request ID. Emit each redacted safety
	// classification once per original process; repeats grant no second effect.
	if c.windowsWarnings == nil {
		c.windowsWarnings = make(map[WindowsWarningKind]bool)
	}
	if c.windowsWarnings[kind] {
		return c.metadata(WindowsSandboxDiscarded), nil
	}
	c.windowsWarnings[kind] = true
	return Event{Kind: NoticeEvent, ThreadID: c.thread, Notice: domain.NativeWarning, Correlated: true, WindowsWarning: &WindowsWarning{Kind: kind, SampleCount: uint32(len(params.SamplePaths)), ExtraCount: *params.ExtraCount}}, nil
}

// Reject case aliases as well as unknown/duplicate fields before typed decoding.
func windowsFields(raw []byte, allowed ...string) bool {
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &fields) != nil || fields == nil {
		return false
	}
	for key := range fields {
		if !slices.Contains(allowed, key) {
			return false
		}
	}
	return true
}
