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
