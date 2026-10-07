package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func claudeInteractionFixture() ExecutionInteractionUpdate {
	return ExecutionInteractionUpdate{ID: NewID(), NativeItemID: "tool_original", NativeRequestID: InteractionRequestID{Kind: InteractionTextID, Text: "request_original"}, Type: NativeApprovalInteraction, Claude: &ClaudeInteractionRequest{Version: ClaudeProtocolVersion, Kind: ClaudeToolPermission, ArrivalID: NewID(), Tool: ClaudeToolReference{ID: NewID(), NativeID: "tool_original", Name: "Bash"}, MessageID: NewID(), NativeMessageID: "msg_original", InputJSON: `{"command":"printf original","exact":9007199254740993}`}}
}
func TestClaudeInteractionRetainsOriginalKindsAndOptionalMetadata(t *testing.T) {
	for _, kind := range []ClaudeInteractionKind{ClaudeToolPermission, ClaudeUserQuestion, ClaudePlanApproval} {
		u := claudeInteractionFixture()
		u.Claude.Kind = kind
		no := false
		u.Claude.Metadata.RequiresUserInteraction = &no
		rule := "printf:*"
		u.Claude.Metadata.Suggestions = []ClaudePermissionUpdate{{Kind: ClaudeAddRules, Rules: []ClaudePermissionRule{{Tool: "Bash", Content: &rule}}, Behavior: ClaudeRuleAllow, Destination: ClaudeSessionSettings}}
		switch kind {
		case ClaudeUserQuestion:
			u.Type = UserQuestionInteraction
			u.Claude.Tool.Name = "AskUserQuestion"
			u.Claude.InputJSON = `{"questions":[{"question":"Original?","header":"Choice","options":[{"label":"One","description":"First"},{"label":"Two","description":"Second"}],"multiSelect":false}]}`
		case ClaudePlanApproval:
			u.Claude.Tool.Name = "ExitPlanMode"
			u.Claude.InputJSON = `{"plan":"# Original plan","planFilePath":"/private/original.md","allowedPrompts":[]}`
		}
		if err := u.Validate(ExecutionInteractionRequested); err != nil {
			t.Fatal(kind, err)
		}
		raw, _ := json.Marshal(u)
		var copy ExecutionInteractionUpdate
		if Decode(raw, &copy) != nil || copy.Claude.InputJSON != u.Claude.InputJSON || copy.Claude.Caller != nil || copy.Claude.Metadata.RequiresUserInteraction == nil || *copy.Claude.Metadata.RequiresUserInteraction {
			t.Fatal("original callback content or missing/false metadata changed")
		}
		original := ExecutionInteraction{Type: u.Type, Claude: u.Claude}
		if (QuestionResponseInput{Answers: map[string][]string{}}).ValidateInteraction(original) == nil || (ApprovalResponseInput{}).ValidateInteraction(original) == nil {
			t.Fatal("another harness response acquired Claude callback authority")
		}
	}
}
func TestClaudeInteractionRejectsForeignOrMixedOriginalRequests(t *testing.T) {
	for _, change := range []string{"version", "arrival", "tool", "provider", "kind", "caller", "input", "suggestion", "mode", "bound", "question-answer", "plan-missing", "mixed", "cancel"} {
		t.Run(change, func(t *testing.T) {
			u := claudeInteractionFixture()
			switch change {
			case "version":
				u.Claude.Version = "invalid/version"
			case "arrival":
				u.Claude.ArrivalID = "invalid"
			case "tool":
				u.Claude.Tool.NativeID = "foreign"
			case "provider":
				u.Claude.MessageID = u.Claude.Tool.ID
			case "kind":
				u.Claude.Kind = ClaudeUserQuestion
			case "caller":
				v := ClaudeToolCallerKind("foreign")
				u.Claude.Caller = &v
			case "input":
				u.Claude.InputJSON = `[]`
			case "suggestion":
				u.Claude.Metadata.Suggestions = []ClaudePermissionUpdate{{Kind: ClaudeUpdateKind("unknown")}}
			case "mode":
				u.Claude.Metadata.Suggestions = []ClaudePermissionUpdate{{Kind: ClaudeSetMode, Mode: ClaudePermissionMode("unknown")}}
			case "bound":
				v := strings.Repeat("x", 4097)
				u.Claude.Metadata.Description = &v
			case "question-answer":
				u.Type = UserQuestionInteraction
				u.Claude.Kind = ClaudeUserQuestion
				u.Claude.Tool.Name = "AskUserQuestion"
				u.Claude.InputJSON = `{"questions":[],"answers":{}}`
			case "plan-missing":
				u.Claude.Kind = ClaudePlanApproval
				u.Claude.Tool.Name = "ExitPlanMode"
			case "mixed":
				u.Questions = &QuestionRequest{}
			case "cancel":
				u.ClaudeCancellation = &ClaudeInteractionCancellation{ArrivalID: u.Claude.ArrivalID}
			}
			if u.Validate(ExecutionInteractionRequested) == nil {
				t.Fatal("invalid native callback accepted")
			}
		})
	}
}
func TestClaudeCallbackInputComparisonPreservesExactNativeNumbers(t *testing.T) {
	if !EqualClaudeToolInput(`{"b":2,"a":9007199254740993}`, `{ "a":9007199254740993, "b":2 }`) || EqualClaudeToolInput(`{"a":9007199254740993}`, `{"a":9007199254740992}`) || EqualClaudeToolInput(`{"a":1}`, `{"a":1.0}`) || EqualClaudeToolInput(`{"a":1,"a":2}`, `{"a":2}`) {
		t.Fatal("callback input comparison lost original values")
	}
}
