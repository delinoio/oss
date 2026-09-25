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
