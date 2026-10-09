// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func revertRecoveryRequest(tx *store.Tx, server domain.ID, sr store.Record, session domain.Session) (domain.ExecutionRecoveryRequest, error) {
	var empty domain.ExecutionRecoveryRequest
	if session.CompactionJobID == "" || session.Execution == nil || !session.Execution.CleanupVerified || session.ActiveExecutionID != "" || session.PendingSteerID != "" || session.PendingInputs != 0 || session.Execution.Waiting != (domain.NativeWaiting{}) || session.Execution.UnconfirmedResponses != 0 || len(session.Execution.Subagents) != 0 || session.Recovery != domain.NeedsRecovery && session.Recovery != domain.Reconciling {
		return empty, domain.ExecutionRecoveryUncertain()
	}
	row, err := tx.Get(domain.JobKind, session.CompactionJobID)
	if err != nil {
		return empty, err
	}
	job, err := store.Decode[domain.Job](row)
	var input domain.SessionCompactionInput
	if err != nil || job.Type != domain.CompactSessionJob || job.State != domain.JobUncertain || row.SessionID != sr.ID || domain.DecodeCompactionInput(job.Input, &input) != nil || input.Validate() != nil || input.Version != 4 || input.Revert == nil || !session.OwnsExecution(input.Assignment) || input.Revert.ContextRevision != session.ContextRevision || input.SourceJobID != session.Execution.JobID || input.Assignment.ExecutionID != session.Execution.ExecutionID {
		return empty, domain.ExecutionRecoveryUncertain()
	}
	assignment, err := tx.JobAssignment(row.ID)
	if err != nil {
		return empty, err
	}
	claim, err := store.Decode[domain.Job](assignment)
	if err != nil || assignment.SessionID != sr.ID || claim.Type != domain.CompactSessionJob || claim.State != domain.JobClaimed || claim.InstanceID != job.InstanceID || claim.AssignedDeviceID != job.AssignedDeviceID || claim.MachineID != session.MachineID || !bytes.Equal(claim.Input, job.Input) {
		return empty, domain.ExecutionRecoveryUncertain()
	}
	// A current lease or unconfirmed server-owned credential operation cannot be
	// replaced by the Worker's native history observation.
	if input.Assignment.Configuration.Subscription {
		_, account, err := accountFromTx(tx, input.Assignment.AccountID, 0)
		if err != nil || account.Subscription == nil || account.Subscription.Lease != nil || account.Subscription.RecoveryRequired {
			return empty, domain.ExecutionRecoveryUncertain()
		}
	}
	result := domain.ExecutionRecoveryRequest{Version: 2, Harness: domain.Codex, ServerID: server, DeviceID: claim.AssignedDeviceID, InstanceID: claim.InstanceID, JobID: row.ID, SessionID: sr.ID, MachineID: session.MachineID, AssignmentRevision: assignment.Revision, AssignmentDigest: continuationDigest(assignment.Data), AssignmentInputDigest: continuationDigest(claim.Input), AccountID: input.Assignment.AccountID, ConnectionID: input.Assignment.ConnectionID, ContextRevision: session.ContextRevision, Revert: &domain.SessionRevertRecovery{Input: input}}
	return result, result.Validate()
}
func finishRevertRecovery(tx *store.Tx, row store.Record, job domain.Job, expected domain.ExecutionRecoveryRequest) error {
	var evidence domain.ExecutionRecoveryEvidence
	if domain.Decode(job.Output, &evidence) != nil || evidence.Validate(expected) != nil {
		return domain.ExecutionRecoveryUncertain()
	}
	original, err := tx.Get(domain.JobKind, expected.JobID)
	if err != nil {
		return err
	}
	previous, err := store.Decode[domain.Job](original)
	if err != nil {
		return err
	}
	sr, session, err := sessionRecord(tx, row.SessionID)
	if err != nil {
		return err
	}
	// Only this explicit observation of the exact original action may release
	// its quarantine. Historical executions and accounting are untouched.
	session.Recovery = domain.NoRecovery
	session.ExecutionRecoveryJobID = ""
	session.Problem = nil
	if _, err = tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
		return err
	}
	raw, err := json.Marshal(evidence.Revert)
	if err != nil {
		return err
	}
	_, err = finishSessionCompaction(tx, original, previous, original.Revision, raw, nil)
	return err
}
