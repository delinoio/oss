// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type remoteControlStatus string

const (
	remoteControlDisabled   remoteControlStatus = "disabled"
	remoteControlConnecting remoteControlStatus = "connecting"
	remoteControlConnected  remoteControlStatus = "connected"
	remoteControlErrored    remoteControlStatus = "errored"
)

type remoteControlPolicy string

const (
	remoteControlActiveRefused      remoteControlPolicy = "remote-state-refused"
	remoteControlEnvironmentRefused remoteControlPolicy = "remote-environment-refused"
)

// Retain only closed classifications. Native identities are discarded before
// the refusal can reach logs, product errors or retained execution state.
type remoteControlPolicyViolation struct {
	status         remoteControlStatus
	classification remoteControlPolicy
	problem        *domain.Error
}

func (v *remoteControlPolicyViolation) Error() string { return v.problem.Error() }
func (v *remoteControlPolicyViolation) Unwrap() error { return v.problem }

// Source: openai/codex a06545b311fe01e51ce855c7aa5d8da21e9e7aaf,
// app-server-protocol/schema/json/v2/RemoteControlStatusChangedNotification.json.
func decodeRemoteControl(raw json.RawMessage) (remoteControlStatus, bool, error) {
	const maxEnvelope = 32 * 1024
	var fields map[string]json.RawMessage
	if domain.DecodeBounded(raw, &fields, maxEnvelope) != nil || fields == nil {
		return "", false, incompatible()
	}
	for name := range fields {
		switch name {
		case "status", "serverName", "installationId", "environmentId":
		default:
			// Reject case aliases before encoding/json's case-insensitive projection.
			return "", false, incompatible()
		}
	}
	var wire *struct {
		Status         *remoteControlStatus `json:"status"`
		ServerName     *string              `json:"serverName"`
		InstallationID *string              `json:"installationId"`
		EnvironmentID  *string              `json:"environmentId"`
	}
	if domain.DecodeBounded(raw, &wire, maxEnvelope) != nil || wire == nil || wire.Status == nil || wire.ServerName == nil || wire.InstallationID == nil {
		return "", false, incompatible()
	}
	for _, value := range []*string{wire.ServerName, wire.InstallationID, wire.EnvironmentID} {
		if value != nil && domain.Text(*value, "native remote identity", 1024, false) != nil {
			return "", false, incompatible()
		}
	}
	switch *wire.Status {
	case remoteControlDisabled, remoteControlConnecting, remoteControlConnected, remoteControlErrored:
	default:
		return "", false, incompatible()
	}
	return *wire.Status, wire.EnvironmentID != nil, nil
}

func (c *Client) observeRemoteControlLocked(native nativewire.Event) (Event, error) {
	status, environment, err := decodeRemoteControl(native.Params)
	if err != nil {
		return Event{}, err
	}
	if status == remoteControlDisabled && !environment {
		event := c.metadata(RemoteControlDisabled)
		event.remoteControlStatus = status
		return event, nil
	}
	classification := remoteControlActiveRefused
	if status == remoteControlDisabled {
		classification = remoteControlEnvironmentRefused
	}
	if c.problem == nil {
		c.problem = domain.Fail(domain.RecoveryRequired, "Native remote control violates the private execution policy.", "Retain the original execution and reconcile its independent cleanup; do not pair, reconnect or replay input.")
	}
	c.execution.paused = true
	refusal := &remoteControlPolicyViolation{status: status, classification: classification, problem: c.problem}
	if c.remoteControlRefusal == nil {
		c.remoteControlRefusal = refusal
	}
	// A valid remote state is recognized, but cannot adopt an identity, mutate
	// native control or replace the original input, settings or turn outcome.
	return Event{}, refusal
}
