// SPDX-License-Identifier: Apache-2.0
package updates

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"time"
)

type State string

const (
	Observed  State = "OBSERVED"
	Waiting   State = "WAITING_FOR_IDLE"
	Running   State = "RUNNING"
	Ready     State = "READY_TO_INSTALL"
	Succeeded State = "SUCCEEDED"
	Failed    State = "FAILED"
	Uncertain State = "UNCERTAIN"
	Canceled  State = "CANCELED"
)

type Operation struct {
	ServerID        domain.ID        `json:"server_id"`
	Actor           domain.Principal `json:"actor"`
	State           State            `json:"state"`
	Component       Component        `json:"component"`
	Target          Target           `json:"target"`
	CurrentVersion  string           `json:"current_version"`
	Version         string           `json:"version"`
	Manifest        []byte           `json:"manifest"`
	ManifestSHA256  string           `json:"manifest_sha256"`
	MachineID       domain.ID        `json:"machine_id,omitempty"`
	MachineRevision uint64           `json:"machine_revision,omitempty,string"`
	DeviceID        domain.ID        `json:"device_id,omitempty"`
	ClaimedInstance domain.ID        `json:"claimed_instance,omitempty"`
	ClaimRequestID  domain.ID        `json:"claim_request_id,omitempty"`
	ClaimedRevision uint64           `json:"claimed_revision,omitempty,string"`
	ProblemCode     domain.Code      `json:"problem_code,omitempty"`
	FinishedAt      *time.Time       `json:"finished_at,omitempty"`
}

func (c *Client) VerifyManifest(raw []byte, current string, now time.Time) (Verified, error) {
	return c.verifier.Verify(raw, current, now)
}
