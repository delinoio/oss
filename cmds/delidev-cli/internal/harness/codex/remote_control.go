// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type RemoteControlStatus string

const (
	RemoteControlStatusDisabled   RemoteControlStatus = "disabled"
	RemoteControlStatusConnecting RemoteControlStatus = "connecting"
	RemoteControlStatusConnected  RemoteControlStatus = "connected"
	RemoteControlStatusErrored    RemoteControlStatus = "errored"
)

type RemoteControlPolicy string

const (
	RemoteControlPassive              RemoteControlPolicy = "disabled-only"
	RemoteControlStateForbidden       RemoteControlPolicy = "remote-state-forbidden"
	RemoteControlEnvironmentForbidden RemoteControlPolicy = "environment-forbidden"
)

// RemoteControlObservation retains no remote server, installation or environment
// identity. It describes a policy observation, never a remote execution owner.
type RemoteControlObservation struct {
	Status             RemoteControlStatus
	EnvironmentPresent bool
	Policy             RemoteControlPolicy
}

func (v RemoteControlObservation) Valid() bool {
	switch v.Status {
	case RemoteControlStatusDisabled, RemoteControlStatusConnecting, RemoteControlStatusConnected, RemoteControlStatusErrored:
	default:
		return false
	}
	if v.EnvironmentPresent {
		return v.Policy == RemoteControlEnvironmentForbidden
	}
	if v.Status == RemoteControlStatusDisabled {
		return v.Policy == RemoteControlPassive
	}
	return v.Policy == RemoteControlStateForbidden
}

// RemoteControlPolicyViolation distinguishes a valid forbidden state from an
// incompatible protocol shape. Existing original-execution recovery still owns
// the failure, and no native remediation call is authorized by this observation.
type RemoteControlPolicyViolation struct{ Observation RemoteControlObservation }

func (v *RemoteControlPolicyViolation) Error() string { return v.Unwrap().Error() }
func (v *RemoteControlPolicyViolation) Unwrap() error {
	return domain.Fail(domain.Unsupported, "Native remote control violates the private runtime policy.", "Preserve original input and reconcile the original process; do not pair, reconnect or replay input.")
}

func decodeRemoteControlStatus(raw []byte) (RemoteControlObservation, error) {
	var wire struct {
		Status         *RemoteControlStatus `json:"status"`
		ServerName     *string              `json:"serverName"`
		InstallationID *string              `json:"installationId"`
		EnvironmentID  json.RawMessage      `json:"environmentId"`
	}
	invalid := func() (RemoteControlObservation, error) {
		return RemoteControlObservation{}, domain.Fail(domain.Unsupported, "The native remote-control notification does not match its closed protocol shape.", "Preserve original input and cleanup ownership.")
	}
	if domain.DecodeWithLimit(raw, &wire, nativewire.MaxFrame) != nil || wire.Status == nil || wire.ServerName == nil || wire.InstallationID == nil {
		return invalid()
	}
	// The schema requires strings but permits empty identity values. Keep them
	// private and bounded without inferring a usable remote identity.
	if domain.Text(*wire.ServerName, "private remote server name", 4096, false) != nil || domain.Text(*wire.InstallationID, "private remote installation identity", 4096, false) != nil {
		return invalid()
	}
	present := false
	if len(wire.EnvironmentID) != 0 {
		var environment *string
		if domain.DecodeWithLimit(wire.EnvironmentID, &environment, nativewire.MaxFrame) != nil || environment != nil && domain.Text(*environment, "private remote environment identity", 4096, false) != nil {
			return invalid()
		}
		present = environment != nil
	}
	v := RemoteControlObservation{Status: *wire.Status, EnvironmentPresent: present, Policy: RemoteControlStateForbidden}
	if present {
		v.Policy = RemoteControlEnvironmentForbidden
	} else if v.Status == RemoteControlStatusDisabled {
		v.Policy = RemoteControlPassive
	}
	if !v.Valid() {
		return invalid()
	}
	return v, nil
}
