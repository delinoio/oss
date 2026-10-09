// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type imageStartupRejectionClaim struct {
	Version            uint32    `json:"version"`
	JobID              domain.ID `json:"job_id"`
	ExecutionID        domain.ID `json:"execution_id"`
	InputID            domain.ID `json:"input_id"`
	RequestID          domain.ID `json:"request_id"`
	ServerID           domain.ID `json:"server_id"`
	DeviceID           domain.ID `json:"device_id"`
	InstanceID         domain.ID `json:"instance_id"`
	AssignmentRevision uint64    `json:"assignment_revision,string"`
	AssignmentDigest   string    `json:"assignment_digest"`
	InputDigest        [32]byte  `json:"input_digest"`
}

// Retain only positive original adapter evidence. This durable claim precedes
// classification, while native/workspace/credential cleanup remains independent.
func (a *executionStartupAttempt) captureImageRejection(result codex.TurnResult) error {
	if a == nil || !result.ProvesImageInputNotSent(a.input.TurnRequestID, a.input.InputID, a.input.Input.InputDigest()) {
		return nil
	}
	c := a.config.execution
	if c == nil || c.Assignment == nil || a.input.Version != 4 || a.input.Configuration.Harness != domain.Codex || len(a.input.Input.Attachments) == 0 || !a.readyReported || a.observation.Phase != domain.StartupInput || a.observation.InputDelivery != domain.StartupClaimed || domain.ID(c.Assignment.Id) != a.job || c.Assignment.Revision == 0 {
		return publicationUncertain()
	}
	var job domain.Job
	var input domain.ExecutionJobInput
	if domain.Decode(c.Assignment.DocumentJson, &job) != nil || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || !reflect.DeepEqual(input, a.input) || input.ExecutionID != a.input.ExecutionID || input.InputID != a.input.InputID || input.TurnRequestID != a.input.TurnRequestID || input.Input.InputDigest() != a.input.Input.InputDigest() || job.Type != domain.ExecuteSessionJob || job.State != domain.JobClaimed || job.InstanceID != c.Instance || job.MachineID != a.input.MachineID || job.AssignedDeviceID != c.Credential.DeviceID {
		return publicationUncertain()
	}
	for _, id := range []domain.ID{c.Credential.ServerID, c.Credential.DeviceID, c.Instance} {
		if id.Validate() != nil {
			return publicationUncertain()
		}
	}
	claim := a.imageRejectionIdentity()
	raw, err := json.Marshal(claim)
	if err != nil {
		return publicationUncertain()
	}
	directory := filepath.Join(a.config.Root, "jobs", string(a.job))
	if security.CheckPrivateDir(directory) != nil {
		return publicationUncertain()
	}
	path := filepath.Join(directory, "startup-image-rejection.json")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return publicationUncertain()
	}
	defer f.Close()
	if _, err = f.Write(raw); err != nil {
		return publicationUncertain()
	}
	if f.Sync() != nil || f.Close() != nil || security.SyncParent(path) != nil {
		return publicationUncertain()
	}
	a.imageRejection = &claim
	return nil
}

func (a *executionStartupAttempt) imageRejectionRetained() bool {
	if a.imageRejection == nil || a.config.execution == nil || a.config.execution.Assignment == nil || *a.imageRejection != a.imageRejectionIdentity() {
		return false
	}
	raw, err := security.ReadPrivate(filepath.Join(a.config.Root, "jobs", string(a.job), "startup-image-rejection.json"), 8192)
	expected, e := json.Marshal(a.imageRejection)
	return err == nil && e == nil && bytes.Equal(raw, expected)
}

func (a *executionStartupAttempt) imageRejectionIdentity() imageStartupRejectionClaim {
	c := a.config.execution
	return imageStartupRejectionClaim{Version: 1, JobID: a.job, ExecutionID: a.input.ExecutionID, InputID: a.input.InputID, RequestID: a.input.TurnRequestID, ServerID: c.Credential.ServerID, DeviceID: c.Credential.DeviceID, InstanceID: c.Instance, AssignmentRevision: c.Assignment.Revision, AssignmentDigest: executionInputDigest(c.Assignment.DocumentJson), InputDigest: a.input.Input.InputDigest()}
}
