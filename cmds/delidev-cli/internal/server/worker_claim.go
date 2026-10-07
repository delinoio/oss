// SPDX-License-Identifier: Apache-2.0
package server

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

// Claim receipts wrap the complete job in fixed Record metadata. Reserve 1 KiB
// for that envelope, then enforce the owning job decoder's unchanged bound.
// Remove this exception when immutable large inputs are stored by reference.
const maxWorkerClaimMetadataBytes = 1024
const maxWorkerClaimBytes = max(domain.MaxCompactionJobBytes, workspace.MaxStorageRecoveryJobBytes) + maxWorkerClaimMetadataBytes

func decodeWorkerClaim(raw []byte) (store.Record, domain.Job, error) {
	var record store.Record
	var job domain.Job
	if err := domain.DecodeWithLimit(raw, &record, maxWorkerClaimBytes); err != nil {
		return record, job, err
	}
	if len(raw)-len(record.Data) > maxWorkerClaimMetadataBytes || record.Kind != domain.JobKind || record.ID.Validate() != nil || record.Revision == 0 || record.SessionID != "" && record.SessionID.Validate() != nil || record.ProjectID != "" && record.ProjectID.Validate() != nil {
		return record, job, domain.Fail(domain.InvalidArgument, "Invalid Worker claim record.", "Preserve the original job identity and revision.")
	}
	// The strict outer decoder already checks nested duplicate keys. Inspect the
	// type only to select the complete owning schema and byte bound below.
	var envelope struct {
		Type domain.JobType `json:"type"`
	}
	if err := json.Unmarshal(record.Data, &envelope); err != nil {
		return record, job, domain.Fail(domain.InvalidArgument, "Invalid Worker claim job.", "Preserve the original typed job document.")
	}
	var err error
	switch envelope.Type {
	case domain.CompactSessionJob:
		err = domain.DecodeCompactionJob(record.Data, &job)
	case domain.WorkspaceStorageJob:
		err = workspace.DecodeStorageJob(record.Data, &job)
	default:
		err = domain.Decode(record.Data, &job)
	}
	if err != nil {
		return record, job, err
	}
	return record, job, job.Validate()
}
