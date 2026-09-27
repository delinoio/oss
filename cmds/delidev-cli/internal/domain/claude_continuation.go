package domain

// ClaudeContinuationBoundary checks public original-input facts. The Worker
// must additionally prove its private native history and exclusive workspace;
// these observations alone can never reconstruct a missing checkpoint.
func (p ExecutionProgress) ClaudeContinuationBoundary(input ID) bool {
	if !p.ClaudeTasks.InlineBashHistoryReady() || !p.ClaudeCompaction.Closed() {
		return false
	}
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
	return t.claudeInlineContinuationCandidate() && t.Reference.Name == "Read"
}

func (t *ClaudeToolContent) ClaudeQuestionContinuationCandidate() bool {
	return t.claudeInlineContinuationCandidate() && t.Reference.Name == "AskUserQuestion" && (t.Result.Error == nil || !*t.Result.Error)
}

func (t *ClaudeToolContent) claudeInlineContinuationCandidate() bool {
	return t != nil && t.Reference.Validate() == nil && t.MessageID.Validate() == nil && Text(t.NativeMessageID, "native provider message", 1024, true) == nil && t.Proposal != nil && validClaudeToolJSON(t.Proposal.Proposed) && validClaudeToolJSON(t.Proposal.Applied) && (t.Caller == nil || *t.Caller == ClaudeDirectToolCaller) && t.Result != nil && t.Result.Validate() == nil && t.Result.NonExecution == nil && t.Result.Structured != nil
}

// Recheck the independently accepted original response, echo and result. A
// closed request, complete tool or successful root cannot substitute for them.
func (v ExecutionInteraction) ClaudeQuestionContinuationEvidence(tool ExecutionMessage) bool {
	r := v.Response
	if v.Claude == nil || v.Claude.Kind != ClaudeUserQuestion || v.Type != UserQuestionInteraction || v.ApprovalResponse != nil || !tool.ClaudeTool.ClaudeQuestionContinuationCandidate() || r == nil || r.ID.Validate() != nil || r.State != QuestionResponseAccepted || r.Input.Claude == nil || r.Input.Claude.Behavior != ClaudeReplyAllow || r.Input.ValidateInteraction(v) != nil || r.Delivery == nil || (r.Delivery.State != QuestionTransmitted && r.Delivery.State != QuestionDeliveryUncertain) || r.Acceptance == nil || r.Acceptance.OpenCode != nil || r.Acceptance.Evidence != QuestionAcceptanceEvidence(ClaudeAnswersProcessed) {
		return false
	}
	return v.claudeCallbackContinuationEvidence(tool, ClaudeAnswersProcessed, *r.Input.Claude, r.Claim, r.ClaudeEcho, r.Delivery.Sequence, r.Acceptance.Sequence)
}

func (v ExecutionInteraction) ClaudeToolApprovalContinuationEvidence(tool ExecutionMessage) bool {
	r := v.ApprovalResponse
	if v.Claude == nil || v.Claude.Kind != ClaudeToolPermission || v.Type != NativeApprovalInteraction || v.Response != nil || (!tool.ClaudeTool.ClaudeReadContinuationCandidate() && !tool.ClaudeTool.ClaudeEffectContinuationCandidate()) || r == nil || r.ID.Validate() != nil || r.State != ApprovalResponseAccepted || r.Input.Claude == nil || r.Input.Claude.Behavior != ClaudeReplyAllow || r.Input.ValidateInteraction(v) != nil || r.Delivery == nil || (r.Delivery.State != ApprovalTransmitted && r.Delivery.State != ApprovalDeliveryUncertain) || r.Acceptance == nil || r.Acceptance.OpenCode != nil || r.Acceptance.Evidence != ApprovalAcceptanceEvidence(ClaudeToolProcessed) {
		return false
	}
	return v.claudeCallbackContinuationEvidence(tool, ClaudeToolProcessed, *r.Input.Claude, r.Claim, r.ClaudeEcho, r.Delivery.Sequence, r.Acceptance.Sequence)
}

func (v ExecutionInteraction) claudeCallbackContinuationEvidence(tool ExecutionMessage, expected ClaudeCallbackEvidence, reply ClaudePermissionResponse, claim *QuestionResponseClaim, echo *ClaudeReplyEcho, delivery, acceptance uint64) bool {
	s := v.ClaudeSettlement
	if v.Closure != InteractionNativeClosed || v.ClaudeCancellation != nil || claim == nil || claim.ID.Validate() != nil || claim.JobID.Validate() != nil || claim.InstanceID.Validate() != nil || claim.DeviceID.Validate() != nil || claim.MachineID.Validate() != nil || echo == nil || s == nil || s.Evidence != expected {
		return false
	}
	if tool.ExecutionID != v.ExecutionID || tool.NativeThreadID != v.NativeThreadID || tool.NativeTurnID != v.NativeTurnID || tool.NativeID != v.NativeItemID || tool.State != MessageComplete || tool.Role != ToolMessage || tool.NativeParentID != tool.ClaudeTool.NativeMessageID || s.ArrivalID != v.Claude.ArrivalID || s.ToolMessageID != tool.ClaudeTool.Reference.ID || s.ResultNativeID != tool.ClaudeTool.Result.NativeEventID || s.Sequence != v.LastSequence || s.Sequence != acceptance || echo.ArrivalID != s.ArrivalID || v.FirstSequence == 0 || delivery <= v.FirstSequence || delivery >= s.Sequence || echo.Sequence <= v.FirstSequence || echo.Sequence >= tool.LastSequence || tool.LastSequence >= s.Sequence {
		return false
	}
	digest, err := ClaudeResponseDigest(v, reply)
	if err != nil || digest != echo.BodyDigest {
		return false
	}
	evidence, err := ClaudeCallbackResultEvidence(v, reply, *tool.ClaudeTool)
	return err == nil && evidence == expected
}

type ClaudeToolHistoryKind string

const (
	ClaudeReadHistory     ClaudeToolHistoryKind = "read"
	ClaudeQuestionHistory ClaudeToolHistoryKind = "question"
	ClaudeEffectHistory   ClaudeToolHistoryKind = "effect"
)

// Effect history preserves completed observations only; restoration never
// re-executes a command, edits a file or restores a previous filesystem state.
func (t *ClaudeToolContent) ClaudeEffectContinuationCandidate() bool {
	if !t.claudeInlineContinuationCandidate() || t.Result.Error != nil && *t.Result.Error {
		return false
	}
	switch t.Reference.Name {
	case "Bash", "Write", "Edit":
		return true
	}
	return false
}

func (t *ClaudeToolContent) ClaudeContinuationHistoryKind() ClaudeToolHistoryKind {
	switch {
	case t.ClaudeReadContinuationCandidate():
		return ClaudeReadHistory
	case t.ClaudeQuestionContinuationCandidate():
		return ClaudeQuestionHistory
	case t.ClaudeEffectContinuationCandidate():
		return ClaudeEffectHistory
	default:
		return ""
	}
}
