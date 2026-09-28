package domain

import (
	"encoding/json"
	"testing"
)

func originalClaudeQuestionContinuation(t *testing.T) (ExecutionInteraction, ExecutionMessage) {
	t.Helper()
	v := claudeResponseOriginal(true)
	v.ExecutionID, v.NativeThreadID, v.NativeTurnID = NewID(), string(NewID()), string(NewID())
	v.Closure, v.FirstSequence, v.LastSequence = InteractionNativeClosed, 4, 9
	tool := claudeSettlementTool(v)
	reply := &ClaudePermissionResponse{Behavior: ClaudeReplyAllow, Answers: map[string]string{"Original?": "Two"}}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal([]byte(v.Claude.InputJSON), &fields)
	fields["answers"], _ = json.Marshal(reply.Answers)
	raw, _ := json.Marshal(fields)
	result := string(raw)
	tool.Result.Structured = &result
	digest, err := ClaudeResponseDigest(v, *reply)
	if err != nil {
		t.Fatal(err)
	}
	v.Response = &QuestionResponse{ID: NewID(), State: QuestionResponseAccepted, Input: QuestionResponseInput{Claude: reply}, Claim: &QuestionResponseClaim{ID: NewID(), JobID: NewID(), MachineID: NewID(), InstanceID: NewID(), DeviceID: NewID()}, Delivery: &QuestionDeliveryObservation{State: QuestionTransmitted, Sequence: 5}, ClaudeEcho: &ClaudeReplyEcho{ArrivalID: v.Claude.ArrivalID, BodyDigest: digest, Sequence: 6}, Acceptance: &QuestionAcceptanceObservation{Evidence: QuestionAcceptanceEvidence(ClaudeAnswersProcessed), Sequence: 9}}
	v.ClaudeSettlement = &ClaudeCallbackSettlement{ArrivalID: v.Claude.ArrivalID, ToolMessageID: tool.Reference.ID, ResultNativeID: tool.Result.NativeEventID, Evidence: ClaudeAnswersProcessed, Sequence: 9}
	m := ExecutionMessage{ExecutionID: v.ExecutionID, NativeThreadID: v.NativeThreadID, NativeTurnID: v.NativeTurnID, NativeID: tool.Reference.NativeID, NativeParentID: tool.NativeMessageID, Role: ToolMessage, State: MessageComplete, LastSequence: 8, ClaudeTool: &tool}
	return v, m
}

func TestClaudeQuestionContinuationRequiresOriginalAcceptedResponse(t *testing.T) {
	for _, change := range []string{"valid", "uncertain-delivery", "open", "canceled", "no-response", "transmitted", "no-claim", "foreign-claim", "no-delivery", "not-sent", "late-delivery", "no-echo", "changed-echo", "late-echo", "no-acceptance", "other-acceptance", "no-settlement", "other-settlement", "changed-answer", "changed-result", "foreign-execution", "foreign-thread", "foreign-turn", "foreign-tool", "foreign-result", "foreign-arrival", "foreign-provider", "tool-error", "no-tool", "tool-streaming", "tool-role", "early-settlement", "changed-closure"} {
		t.Run(change, func(t *testing.T) {
			v, m := originalClaudeQuestionContinuation(t)
			switch change {
			case "uncertain-delivery":
				v.Response.Delivery.State = QuestionDeliveryUncertain
			case "open":
				v.Closure = InteractionOpen
			case "canceled":
				v.ClaudeCancellation = &ClaudeInteractionCancellation{ArrivalID: v.Claude.ArrivalID}
			case "no-response":
				v.Response = nil
			case "transmitted":
				v.Response.State = QuestionResponseTransmitted
			case "no-claim":
				v.Response.Claim = nil
			case "foreign-claim":
				v.Response.Claim.ID = "invalid"
			case "no-delivery":
				v.Response.Delivery = nil
			case "not-sent":
				v.Response.Delivery.State = QuestionNotSent
			case "late-delivery":
				v.Response.Delivery.Sequence = 10
			case "no-echo":
				v.Response.ClaudeEcho = nil
			case "changed-echo":
				v.Response.ClaudeEcho.BodyDigest = "foreign"
			case "late-echo":
				v.Response.ClaudeEcho.Sequence = 8
			case "no-acceptance":
				v.Response.Acceptance = nil
			case "other-acceptance":
				v.Response.Acceptance.Evidence = NativeQuestionOutput
			case "no-settlement":
				v.ClaudeSettlement = nil
			case "other-settlement":
				v.ClaudeSettlement.Evidence = ClaudeToolProcessed
			case "changed-answer":
				v.Response.Input.Claude.Answers["Original?"] = "Changed"
			case "changed-result":
				*m.ClaudeTool.Result.Structured = `{"questions":[],"answers":{}}`
			case "foreign-execution":
				m.ExecutionID = NewID()
			case "foreign-thread":
				m.NativeThreadID = string(NewID())
			case "foreign-turn":
				m.NativeTurnID = string(NewID())
			case "foreign-tool":
				m.NativeID = "foreign"
			case "foreign-result":
				v.ClaudeSettlement.ResultNativeID = string(NewID())
			case "foreign-arrival":
				v.ClaudeSettlement.ArrivalID = NewID()
			case "foreign-provider":
				m.NativeParentID = "foreign"
			case "tool-error":
				failed := true
				m.ClaudeTool.Result.Error = &failed
			case "no-tool":
				m.ClaudeTool = nil
			case "tool-streaming":
				m.State = MessageStreaming
			case "tool-role":
				m.Role = AssistantMessage
			case "early-settlement":
				v.ClaudeSettlement.Sequence = 8
			case "changed-closure":
				v.LastSequence++
			}
			if v.ClaudeQuestionContinuationEvidence(m) != (change == "valid" || change == "uncertain-delivery") {
				t.Fatal("unproved question history acquired continuation")
			}
		})
	}
}

func originalClaudeApprovalContinuation(t *testing.T, name string) (ExecutionInteraction, ExecutionMessage) {
	t.Helper()
	v, m := originalClaudeQuestionContinuation(t)
	question := v.Response
	v.Response = nil
	v.Type, v.Claude.Kind, v.Claude.Tool.Name = NativeApprovalInteraction, ClaudeToolPermission, name
	v.Claude.InputJSON = `{"original":"unchanged"}`
	tool := claudeSettlementTool(v)
	metadata := `{"original":"native result"}`
	tool.Result.Structured = &metadata
	m.ClaudeTool = &tool
	v.ClaudeSettlement.Evidence = ClaudeToolProcessed
	v.ClaudeSettlement.ResultNativeID = tool.Result.NativeEventID
	reply := &ClaudePermissionResponse{Behavior: ClaudeReplyAllow}
	digest, err := ClaudeResponseDigest(v, *reply)
	if err != nil {
		t.Fatal(err)
	}
	v.ApprovalResponse = &ApprovalResponse{ID: question.ID, State: ApprovalResponseAccepted, Input: ApprovalResponseInput{Claude: reply}, Claim: question.Claim, Delivery: &ApprovalDeliveryObservation{State: ApprovalTransmitted, Sequence: question.Delivery.Sequence}, ClaudeEcho: &ClaudeReplyEcho{ArrivalID: v.Claude.ArrivalID, BodyDigest: digest, Sequence: question.ClaudeEcho.Sequence}, Acceptance: &ApprovalAcceptanceObservation{Evidence: ApprovalAcceptanceEvidence(ClaudeToolProcessed), Sequence: question.Acceptance.Sequence}}
	return v, m
}

func TestClaudeToolContinuationRetainsOnlyOriginalAcceptedPermission(t *testing.T) {
	for _, name := range []string{"Read", "Bash", "Write", "Edit"} {
		t.Run(name, func(t *testing.T) {
			for _, change := range []string{"valid", "uncertain-delivery", "missing-response", "question-response", "transmitted", "no-claim", "no-echo", "changed-echo", "not-sent", "wrong-acceptance", "missing-settlement", "question-settlement", "denied", "changed-input", "tool-error", "no-metadata", "non-executed", "canceled", "open"} {
				t.Run(change, func(t *testing.T) {
					v, m := originalClaudeApprovalContinuation(t, name)
					r := v.ApprovalResponse
					switch change {
					case "uncertain-delivery":
						r.Delivery.State = ApprovalDeliveryUncertain
					case "missing-response":
						v.ApprovalResponse = nil
					case "question-response":
						v.Response = &QuestionResponse{}
					case "transmitted":
						r.State = ApprovalResponseTransmitted
					case "no-claim":
						r.Claim = nil
					case "no-echo":
						r.ClaudeEcho = nil
					case "changed-echo":
						r.ClaudeEcho.BodyDigest = "foreign"
					case "not-sent":
						r.Delivery.State = ApprovalNotSent
					case "wrong-acceptance":
						r.Acceptance.Evidence = NativeApprovedCommand
					case "missing-settlement":
						v.ClaudeSettlement = nil
					case "question-settlement":
						v.ClaudeSettlement.Evidence = ClaudeAnswersProcessed
					case "denied":
						message := "Original denial"
						r.Input.Claude = &ClaudePermissionResponse{Behavior: ClaudeReplyDeny, Message: &message}
					case "changed-input":
						m.ClaudeTool.Proposal.Applied = `{"changed":true}`
					case "tool-error":
						failed := true
						m.ClaudeTool.Result.Error = &failed
					case "no-metadata":
						m.ClaudeTool.Result.Structured = nil
					case "non-executed":
						m.ClaudeTool.Result.NonExecution = &ClaudeToolNonExecution{NativeID: v.NativeItemID, Kind: ClaudeUserRejectedNonExecution}
					case "canceled":
						v.ClaudeCancellation = &ClaudeInteractionCancellation{ArrivalID: v.Claude.ArrivalID}
					case "open":
						v.Closure = InteractionOpen
					}
					want := change == "valid" || change == "uncertain-delivery" || change == "tool-error" && name == "Read"
					if v.ClaudeToolApprovalContinuationEvidence(m) != want {
						t.Fatal("tool continuation reinterpreted original approval")
					}
					if v.ClaudeQuestionContinuationEvidence(m) {
						t.Fatal("tool approval became question authority")
					}
				})
			}
		})
	}
}
