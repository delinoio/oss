package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func questionFixture() map[string]any {
	return map[string]any{"id": "choice", "header": "Choice", "question": "Choose one", "options": []any{map[string]any{"label": "First", "description": "First option"}, map[string]any{"label": "Second", "description": "Second option"}}}
}

func questionEvent(t *testing.T, c *Client, turn domain.ID, questions any) nativewire.Event {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"threadId": c.thread, "turnId": turn, "itemId": "question-tool", "isBlocking": true, "autoResolutionMs": nil, "questions": questions})
	if err != nil {
		t.Fatal(err)
	}
	return nativewire.Event{Kind: nativewire.ServerRequest, Method: "item/tool/requestUserInput", ID: json.RawMessage(`7`), Token: domain.NewID(), Params: raw}
}

func TestNativeQuestionsPreserveArrivalIdentityBlockingAndOptionSemantics(t *testing.T) {
	c, turn := observationClient()
	question := questionFixture()
	event := questionEvent(t, c, turn, []any{question})
	e, err := c.observeEventLocked(event)
	if err != nil || e.Kind != InteractionRequestedEvent || !e.Correlated || e.Late || e.Interaction == nil || e.Interaction.ID != event.Token || e.Interaction.NativeID.Kind != NumberRequestID || *e.Interaction.NativeID.Number != 7 || e.ItemID != "question-tool" || !e.Interaction.Questions.Blocking || e.Interaction.Questions.AutoResolutionMS != nil {
		t.Fatalf("native question lost request/arrival semantics: %v", err)
	}
	q := e.Interaction.Questions.Questions[0]
	if q.ID != "choice" || q.Header != "Choice" || q.Text != "Choose one" || q.Other || q.Secret || len(q.Options) != 2 || q.Options[1].Label != "Second" || q.Options[1].Description != "Second option" {
		t.Fatal("native question fields/defaults/options were rewritten")
	}
	raw, _ := json.Marshal(e.Interaction)
	if strings.Contains(string(raw), "Params") || strings.Contains(string(raw), "item/tool/requestUserInput") {
		t.Fatal("private native reply authority escaped normalized serialization")
	}
	question["isOther"], question["isSecret"], question["options"] = true, true, nil
	event = questionEvent(t, c, turn, []any{question})
	event.ID = json.RawMessage(`"7"`)
	var fields map[string]any
	_ = json.Unmarshal(event.Params, &fields)
	fields["isBlocking"], fields["autoResolutionMs"] = false, 100
	event.Params, _ = json.Marshal(fields)
	e, err = c.observeEventLocked(event)
	if err != nil || e.Interaction.NativeID.Kind != TextRequestID || e.Interaction.NativeID.Text != "7" || e.Interaction.NativeID.Number != nil || e.Interaction.Questions.Blocking || *e.Interaction.Questions.AutoResolutionMS != 100 {
		t.Fatal("nonblocking/deprecated timing or textual request identity changed")
	}
	q = e.Interaction.Questions.Questions[0]
	if !q.Other || !q.Secret || q.Options != nil || c.execution.turns[turn].Turn.Status != TurnRunning {
		t.Fatal("secret/freeform flags granted execution or became an implicit answer")
	}
	fields["threadId"] = domain.NewID()
	event.Params, _ = json.Marshal(fields)
	e, err = c.observeEventLocked(event)
	raw, _ = json.Marshal(e)
	if err != nil || e.Kind != NativeExtensionEvent || e.Interaction != nil || strings.Contains(string(raw), "Choose one") {
		t.Fatal("foreign question escaped private extension handling")
	}
}

func TestNativeQuestionsRejectAmbiguousAndWrongKindPayloads(t *testing.T) {
	for _, bad := range []string{"duplicate-id", "duplicate-option", "null-flag", "missing-question", "null-option", "approval-field", "missing-blocking", "unknown-turn", "missing-arrival", "overflow-id", "null-id", "empty-questions"} {
		t.Run(bad, func(t *testing.T) {
			c, turn := observationClient()
			q := questionFixture()
			questions := []any{q}
			switch bad {
			case "duplicate-id":
				questions = append(questions, q)
			case "duplicate-option":
				q["options"] = []any{map[string]any{"label": "Same", "description": "a"}, map[string]any{"label": "Same", "description": "b"}}
			case "null-flag":
				q["isSecret"] = nil
			case "missing-question":
				delete(q, "question")
			case "null-option":
				q["options"] = []any{nil}
			case "approval-field":
				q["decision"] = "accept"
			case "unknown-turn":
				turn = domain.NewID()
			case "empty-questions":
				questions = []any{}
			}
			event := questionEvent(t, c, turn, questions)
			switch bad {
			case "missing-blocking":
				var fields map[string]json.RawMessage
				_ = json.Unmarshal(event.Params, &fields)
				delete(fields, "isBlocking")
				event.Params, _ = json.Marshal(fields)
			case "missing-arrival":
				event.Token = ""
			case "overflow-id":
				event.ID = json.RawMessage(`9223372036854775808`)
			case "null-id":
				event.ID = json.RawMessage(`null`)
			}
			if _, err := c.observeEventLocked(event); err == nil {
				t.Fatal("ambiguous or malformed interaction was normalized")
			}
		})
	}
}
