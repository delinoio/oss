// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type revertPreSendBoundary struct {
	claimInvoked         bool
	nativeClosed         bool
	proxyClosed          bool
	workspaceClosed      bool
	authenticationClosed bool
	cleanupFailed        bool
}

// Called only after the original caller proved callback absence and joined
// native/proxy/workspace/protected-account cleanup. Generic errors alone cannot
// reach this report path; no intent or successor checkpoint is manufactured.
func completedRevertPreSendFailure(owner domain.ID, job domain.Job, input domain.SessionCompactionInput, failure error, boundary revertPreSendBoundary) (json.RawMessage, error) {
	if failure == nil || boundary.claimInvoked || !boundary.nativeClosed || !boundary.proxyClosed || !boundary.workspaceClosed || !boundary.authenticationClosed || boundary.cleanupFailed || input.Validate() != nil || input.Version != 4 || input.Revert == nil {
		return nil, domain.CompactionUncertain()
	}
	result := domain.SessionRevertPreSendFailure{Version: 1, Outcome: domain.RevertFailedBeforeClaim, JobID: owner, ActionID: input.ActionID, ExecutionID: input.Assignment.ExecutionID, InputDigest: executionInputDigest(job.Input), ContextRevision: input.Revert.ContextRevision, NativeThreadID: input.Completion.NativeThreadID, ClaimNotInvoked: true, NativeSendNotStarted: true, CleanupVerified: true, FailureCode: domain.SafeError(failure).Code}
	if result.Validate() != nil {
		return nil, domain.CompactionUncertain()
	}
	return json.Marshal(result)
}
