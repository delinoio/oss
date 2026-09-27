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
	r, s := v.Response, v.ClaudeSettlement
	if v.Claude == nil || v.Claude.Kind != ClaudeUserQuestion || v.Type != UserQuestionInteraction || v.Closure != InteractionNativeClosed || v.ClaudeCancellation != nil || v.ApprovalResponse != nil || r == nil || r.ID.Validate() != nil || r.State != QuestionResponseAccepted || r.Input.Claude == nil || r.Input.Claude.Behavior != ClaudeReplyAllow || r.Input.ValidateInteraction(v) != nil || r.Claim == nil || r.Claim.ID.Validate() != nil || r.Claim.JobID.Validate() != nil || r.Claim.InstanceID.Validate() != nil || r.Claim.DeviceID.Validate() != nil || r.Claim.MachineID.Validate() != nil || r.Delivery == nil || (r.Delivery.State != QuestionTransmitted && r.Delivery.State != QuestionDeliveryUncertain) || r.ClaudeEcho == nil || r.Acceptance == nil || r.Acceptance.OpenCode != nil || r.Acceptance.Evidence != QuestionAcceptanceEvidence(ClaudeAnswersProcessed) || s == nil || s.Evidence != ClaudeAnswersProcessed {
		return false
	}
	if tool.ExecutionID != v.ExecutionID || tool.NativeThreadID != v.NativeThreadID || tool.NativeTurnID != v.NativeTurnID || tool.NativeID != v.NativeItemID || tool.State != MessageComplete || tool.Role != ToolMessage || !tool.ClaudeTool.ClaudeQuestionContinuationCandidate() || tool.NativeParentID != tool.ClaudeTool.NativeMessageID || s.ArrivalID != v.Claude.ArrivalID || s.ToolMessageID != tool.ClaudeTool.Reference.ID || s.ResultNativeID != tool.ClaudeTool.Result.NativeEventID || s.Sequence != v.LastSequence || s.Sequence != r.Acceptance.Sequence || r.ClaudeEcho.ArrivalID != s.ArrivalID || v.FirstSequence == 0 || r.Delivery.Sequence <= v.FirstSequence || r.Delivery.Sequence >= s.Sequence || r.ClaudeEcho.Sequence <= v.FirstSequence || r.ClaudeEcho.Sequence >= tool.LastSequence || tool.LastSequence >= s.Sequence {
		return false
	}
	digest, err := ClaudeResponseDigest(v, *r.Input.Claude)
	if err != nil || digest != r.ClaudeEcho.BodyDigest {
		return false
	}
	evidence, err := ClaudeCallbackResultEvidence(v, *r.Input.Claude, *tool.ClaudeTool)
	return err == nil && evidence == ClaudeAnswersProcessed
}
