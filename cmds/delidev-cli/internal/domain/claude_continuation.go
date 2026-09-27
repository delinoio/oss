package domain

// ClaudeContinuationBoundary checks public original-input facts. The Worker
// must additionally prove its private native history and exclusive workspace;
// these observations alone can never reconstruct a missing checkpoint.
func (p ExecutionProgress) ClaudeContinuationBoundary(input ID) bool {
	t := p.ClaudeTerminal
	if t == nil || t.Validate() != nil || t.InputID != input || t.Outcome() != ExecutionSucceeded || p.Outcome != ExecutionSucceeded || p.ClaudeStop != nil || p.ClaudeDenial != nil || p.ClaudeInterruption != nil || p.OpenCodeStop != nil || p.Waiting != (NativeWaiting{}) || p.UnconfirmedResponses != 0 || len(p.AcceptedInputs) > 1 || len(p.AcceptedInputs) == 1 && p.AcceptedInputs[0].InputID != input || p.SteerAttempts != 0 {
		return false
	}
	return p.ClaudeProgress == nil || !p.ClaudeProgress.PermissionChanged && (p.ClaudeProgress.Permission == nil || *p.ClaudeProgress.Permission == p.Observed.ClaudePermission)
}

// This public candidate preserves a completed original root Read, including a
// native read error. It is not native-history proof: the Worker still validates
// exact persisted input/result metadata against its original controller ledger.
func (t *ClaudeToolContent) ClaudeReadContinuationCandidate() bool {
	return t != nil && t.Reference.Validate() == nil && t.Reference.Name == "Read" && t.MessageID.Validate() == nil && Text(t.NativeMessageID, "native provider message", 1024, true) == nil && t.Proposal != nil && validClaudeToolJSON(t.Proposal.Proposed) && validClaudeToolJSON(t.Proposal.Applied) && (t.Caller == nil || *t.Caller == ClaudeDirectToolCaller) && t.Result != nil && t.Result.Validate() == nil && t.Result.NonExecution == nil && t.Result.Structured != nil
}
