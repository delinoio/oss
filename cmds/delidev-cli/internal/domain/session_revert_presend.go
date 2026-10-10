// SPDX-License-Identifier: Apache-2.0
package domain

type RevertFailureOutcome string

const RevertFailedBeforeClaim RevertFailureOutcome = "failed_before_claim"

// SessionRevertPreSendFailure grants no replacement checkpoint or native retry.
// Only the original Worker can prove this closed boundary after joined cleanup.
type SessionRevertPreSendFailure struct {
	Version              uint32               `json:"version"`
	Outcome              RevertFailureOutcome `json:"revert_failure"`
	JobID                ID                   `json:"job_id"`
	ActionID             ID                   `json:"action_id"`
	ExecutionID          ID                   `json:"execution_id"`
	InputDigest          string               `json:"input_digest"`
	ContextRevision      uint64               `json:"context_revision"`
	NativeThreadID       NativeIdentity       `json:"native_thread_id"`
	ClaimNotInvoked      bool                 `json:"claim_not_invoked"`
	NativeSendNotStarted bool                 `json:"native_send_not_started"`
	CleanupVerified      bool                 `json:"cleanup_verified"`
	FailureCode          Code                 `json:"failure_code"`
}

func (r SessionRevertPreSendFailure) Validate() error {
	if r.Version != 1 || r.Outcome != RevertFailedBeforeClaim || UniqueIDs([]ID{r.JobID, r.ActionID, r.ExecutionID}) != nil || !validCompactionDigest(r.InputDigest) || r.NativeThreadID.Validate(Codex, NativeThreadIdentity) != nil || !r.ClaimNotInvoked || !r.NativeSendNotStarted || !r.CleanupVerified {
		return CompactionUncertain()
	}
	switch r.FailureCode {
	case InvalidArgument, NotFound, Conflict, Unauthenticated, PermissionDenied, Unavailable, ServerUnavailable, ConfirmationRequired, MissingInput, Unsupported, RecoveryRequired, BudgetReached, ResourceExhausted, CursorExpired, ProviderDisabled, Canceled, Internal:
		return nil
	default:
		return CompactionUncertain()
	}
}
