// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type remoteControlStatus string

const (
	remoteDisabled   remoteControlStatus = "disabled"
	remoteConnecting remoteControlStatus = "connecting"
	remoteConnected  remoteControlStatus = "connected"
	remoteErrored    remoteControlStatus = "errored"
)

// Identities remain private comparison metadata, never remote adoption authority.
type remoteControlObservation struct {
	status         remoteControlStatus
	serverName     string
	installationID string
	environmentID  *string
}
type remoteControlPolicyViolation struct{ status remoteControlStatus }

func (*remoteControlPolicyViolation) Error() string {
	return "Native remote control violates the private execution policy."
}
func remoteControlRecovery() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "Native remote control violates the private execution policy.", "Retain the original input and runtime; reconcile independent cleanup before any explicit recovery.")
}
func decodeRemoteControlStatus(raw json.RawMessage) (remoteControlObservation, error) {
	var wire struct {
		Status         remoteControlStatus `json:"status"`
		ServerName     *string             `json:"serverName"`
		InstallationID *string             `json:"installationId"`
		EnvironmentID  *string             `json:"environmentId"`
	}
	if domain.DecodeBounded(raw, &wire, nativewire.MaxFrame) != nil || wire.ServerName == nil || wire.InstallationID == nil || domain.Text(*wire.ServerName, "native remote server", 1024, false) != nil || domain.Text(*wire.InstallationID, "native remote installation", 1024, false) != nil || wire.EnvironmentID != nil && domain.Text(*wire.EnvironmentID, "native remote environment", 1024, false) != nil {
		return remoteControlObservation{}, incompatible()
	}
	switch wire.Status {
	case remoteDisabled, remoteConnecting, remoteConnected, remoteErrored:
	default:
		return remoteControlObservation{}, incompatible()
	}
	return remoteControlObservation{status: wire.Status, serverName: *wire.ServerName, installationID: *wire.InstallationID, environmentID: wire.EnvironmentID}, nil
}
