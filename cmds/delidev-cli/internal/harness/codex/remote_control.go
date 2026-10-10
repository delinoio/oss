// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"errors"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type nativeRemoteControlStatus string

const (
	nativeRemoteDisabled   nativeRemoteControlStatus = "disabled"
	nativeRemoteConnecting nativeRemoteControlStatus = "connecting"
	nativeRemoteConnected  nativeRemoteControlStatus = "connected"
	nativeRemoteErrored    nativeRemoteControlStatus = "errored"

	validationRemoteControl       eventValidationStage = "remote-control-observation"
	validationRemoteControlPolicy eventValidationStage = "remote-control-policy"
)

// A valid native state can still violate the disabled-only execution policy.
// Retain only its closed status, never remote descriptors or identity strings.
type remoteControlPolicyViolation struct {
	Status nativeRemoteControlStatus
}

func (remoteControlPolicyViolation) Error() string {
	return "The original native process violated the disabled-only remote-control policy."
}

func remoteControlFailureStage(err error, fallback eventValidationStage) eventValidationStage {
	var policy remoteControlPolicyViolation
	if errors.As(err, &policy) {
		return validationRemoteControlPolicy
	}
	return fallback
}

func (c *Client) observeRemoteControlLocked(native nativewire.Event) (Event, error) {
	var params struct {
		EnvironmentID  *string                   `json:"environmentId"`
		InstallationID *string                   `json:"installationId"`
		ServerName     *string                   `json:"serverName"`
		Status         nativeRemoteControlStatus `json:"status"`
	}
	if domain.Decode(native.Params, &params) != nil || params.InstallationID == nil || params.ServerName == nil || domain.Text(*params.InstallationID, "native remote installation", 1024, false) != nil || domain.Text(*params.ServerName, "native remote server", 1024, false) != nil || params.EnvironmentID != nil && domain.Text(*params.EnvironmentID, "native remote environment", 1024, false) != nil {
		return Event{}, incompatible()
	}
	switch params.Status {
	case nativeRemoteDisabled, nativeRemoteConnecting, nativeRemoteConnected, nativeRemoteErrored:
	default:
		return Event{}, incompatible()
	}
	if params.Status != nativeRemoteDisabled || params.EnvironmentID != nil {
		if c.logger != nil {
			c.logger.Warn("Codex remote-control policy rejected", "owner_id", c.ownerID, "stage", validationRemoteControlPolicy, "status", params.Status)
		}
		// Next retains the ordinary execution uncertainty fence and its original
		// cleanup/recovery path. This observation cannot pair, disable, reconnect,
		// adopt a remote owner or grant another native input.
		return Event{}, remoteControlPolicyViolation{Status: params.Status}
	}
	return c.metadata(RemoteControlDisabled), nil
}
