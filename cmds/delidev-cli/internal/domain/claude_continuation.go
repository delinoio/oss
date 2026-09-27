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
