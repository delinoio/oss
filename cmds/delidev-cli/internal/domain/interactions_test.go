package domain

import (
	"encoding/json"
	"testing"
)

func TestQuestionPublicationRejectsNullMissingAndCrossKindFields(t *testing.T) {
	for _, bad := range []string{"blocking", "other", "secret", "header", "text", "option-description", "option-label", "null-question", "duplicate-id", "approval", "answer", "missing-flag"} {
		t.Run(bad, func(t *testing.T) {
			option := map[string]any{"label": "First", "description": "Description"}
			q := map[string]any{"id": "question", "header": "Header", "text": "Question", "other": true, "secret": false, "options": []any{option}}
			request := map[string]any{"blocking": true, "auto_resolution_ms": nil, "questions": []any{q}}
			switch bad {
			case "blocking":
				request[bad] = nil
			case "other", "secret", "header", "text":
				q[bad] = nil
			case "option-description":
				option["description"] = nil
			case "option-label":
				option["label"] = nil
			case "null-question":
				request["questions"] = []any{nil}
			case "duplicate-id":
				request["questions"] = []any{q, q}
			case "approval":
				q["decision"] = "accept"
			case "answer":
				q["answers"] = []string{"First"}
			case "missing-flag":
				delete(q, "secret")
			}
			raw, _ := json.Marshal(request)
			var value QuestionRequest
			if Decode(raw, &value) == nil {
				t.Fatal("invalid question acquired normalized defaults or permission fields")
			}
		})
	}
	for _, raw := range []string{`{}`, `{"user_input":null,"approval":false}`, `{"user_input":true,"approval":false,"decision":"accept"}`} {
		var waiting NativeWaiting
		if Decode([]byte(raw), &waiting) == nil {
			t.Fatal("invalid native waiting acquired defaults or authority")
		}
	}
}

func TestMCPElicitationMarkerIsLimitedToGenericQuestions(t *testing.T) {
	question := func() ExecutionInteractionUpdate {
		return ExecutionInteractionUpdate{ID: NewID(), NativeItemID: "mcp-elicitation/n:1", NativeRequestID: InteractionRequestID{Kind: InteractionTextID, Text: "native"}, Type: UserQuestionInteraction, Questions: &QuestionRequest{Blocking: true, Questions: []Question{{ID: "action", Header: "MCP", Text: "Write?", Options: []QuestionOption{{Label: "accept"}}}}}, MCPElicitation: true}
	}
	if err := question().Validate(ExecutionInteractionRequested); err != nil {
		t.Fatalf("a marked generic question was rejected: %v", err)
	}
	for name, edit := range map[string]func(*ExecutionInteractionUpdate){
		"approval": func(u *ExecutionInteractionUpdate) {
			u.Type, u.Questions, u.Approval = NativeApprovalInteraction, nil, &ApprovalRequest{}
		},
		"no-questions":  func(u *ExecutionInteractionUpdate) { u.Questions = nil },
		"opencode-body": func(u *ExecutionInteractionUpdate) { u.OpenCode = &OpenCodeInteractionRequest{} },
	} {
		u := question()
		edit(&u)
		if err := u.Validate(ExecutionInteractionRequested); err == nil {
			t.Fatalf("%s accepted an MCP marker", name)
		}
	}
}
