package opencode

import (
	"encoding/json"
	"strings"
	"testing"
)

const fixturePermissionID = "per_01960dcbe1faABCDEFGHIJKLMN"
const fixtureQuestionID = "que_01960dcbe1faABCDEFGHIJKLMN"

func fixturePermission() map[string]any {
	return map[string]any{"id": fixturePermissionID, "sessionID": fixtureSessionID, "permission": "read", "patterns": []string{"private-sentinel"}, "always": []string{"private-sentinel/*"}, "metadata": map[string]any{"path": "private-sentinel"}, "tool": map[string]any{"messageID": fixtureMessageID, "callID": "call_private"}}
}

func fixtureQuestion() map[string]any {
	return map[string]any{"id": fixtureQuestionID, "sessionID": fixtureSessionID, "questions": []any{map[string]any{"question": "private-sentinel", "header": "private-sentinel", "options": []any{map[string]any{"label": "private-sentinel", "description": "private-sentinel"}}, "multiple": false, "custom": false}}, "tool": map[string]any{"messageID": fixtureMessageID, "callID": "call_private"}}
}

func TestNativeInteractionPreservesPrivateScopeAndOptionalOwner(t *testing.T) {
	for kind, build := range map[EventKind]func() map[string]any{PermissionAskedEvent: fixturePermission, QuestionAskedEvent: fixtureQuestion} {
		for _, owned := range []bool{false, true} {
			value := build()
			if !owned {
				delete(value, "tool")
			}
			raw, _ := json.Marshal(value)
			interaction, err := decodeNativeInteraction(kind, raw)
			if err != nil || (interaction.Tool != nil) != owned || interaction.SessionID != fixtureSessionID {
				t.Fatalf("native interaction owner: %v", err)
			}
			encoded, _ := json.Marshal(interaction)
			if strings.Contains(string(encoded), "private-sentinel") {
				t.Fatal("private interaction content escaped serialization")
			}
			if interaction.Kind == QuestionInteraction && (interaction.Questions[0].Custom == nil || *interaction.Questions[0].Custom || interaction.Questions[0].Multiple == nil || *interaction.Questions[0].Multiple) {
				t.Fatal("explicit false question options were lost")
			}
			if interaction.Kind == PermissionInteraction && (len(interaction.Permission.Always) != 1 || len(interaction.Permission.Patterns) != 1 || interaction.Permission.Metadata == nil) {
				t.Fatal("native permission scope was flattened")
			}
		}
	}
	value := fixtureQuestion()
	q := value["questions"].([]any)[0].(map[string]any)
	delete(q, "custom")
	delete(q, "multiple")
	q["options"] = []any{}
	raw, _ := json.Marshal(value)
	parsed, err := decodeNativeInteraction(QuestionAskedEvent, raw)
	if err != nil || parsed.Questions[0].Custom != nil || parsed.Questions[0].Multiple != nil || parsed.Questions[0].Options == nil {
		t.Fatal("absent native defaults or empty options were rewritten")
	}
}

func TestNativeInteractionRefusesMalformedAndMixedNamespaces(t *testing.T) {
	for _, change := range []func(map[string]any){
		func(v map[string]any) { v["id"] = fixturePermissionID },
		func(v map[string]any) { v["tool"] = nil },
		func(v map[string]any) { v["tool"].(map[string]any)["messageID"] = fixtureSessionID },
		func(v map[string]any) { v["questions"] = nil },
		func(v map[string]any) { v["questions"].([]any)[0].(map[string]any)["custom"] = nil },
		func(v map[string]any) { v["questions"].([]any)[0].(map[string]any)["options"] = nil },
		func(v map[string]any) {
			v["questions"].([]any)[0].(map[string]any)["options"].([]any)[0].(map[string]any)["description"] = nil
		},
		func(v map[string]any) { v["permission"] = "read" },
	} {
		value := fixtureQuestion()
		change(value)
		raw, _ := json.Marshal(value)
		if _, err := decodeNativeInteraction(QuestionAskedEvent, raw); err == nil {
			t.Fatal("malformed question acquired native meaning")
		}
	}
	for _, field := range []string{"metadata", "patterns", "always"} {
		value := fixturePermission()
		value[field] = nil
		raw, _ := json.Marshal(value)
		if _, err := decodeNativeInteraction(PermissionAskedEvent, raw); err == nil {
			t.Fatal("missing permission scope was inferred")
		}
	}
	raw, _ := json.Marshal(fixturePermission())
	if _, err := decodeNativeInteraction(PermissionV2AskedEvent, raw); err == nil {
		t.Fatal("v2 interaction silently aliased into v1")
	}
}

func TestNativeInteractionRepliesKeepClosureAndAnswersDistinct(t *testing.T) {
	for _, decision := range []PermissionDecision{PermissionOnce, PermissionAlways, PermissionReject} {
		raw, _ := json.Marshal(map[string]any{"sessionID": fixtureSessionID, "requestID": fixturePermissionID, "reply": decision})
		value, err := decodeNativeInteractionReply(PermissionRepliedEvent, raw)
		if err != nil || value.Decision == nil || *value.Decision != decision || value.Rejected != (decision == PermissionReject) || value.Answers != nil {
			t.Fatal("native permission closure changed")
		}
	}
	for _, rejected := range []bool{false, true} {
		kind := QuestionRepliedEvent
		fields := map[string]any{"sessionID": fixtureSessionID, "requestID": fixtureQuestionID}
		if rejected {
			kind = QuestionRejectedEvent
		} else {
			fields["answers"] = [][]string{{"private-sentinel", "other"}, {}}
		}
		raw, _ := json.Marshal(fields)
		value, err := decodeNativeInteractionReply(kind, raw)
		if err != nil || value.Rejected != rejected || value.Decision != nil || (value.Answers == nil) != rejected {
			t.Fatal("question dismissal fabricated an answer")
		}
		if !rejected && (len(value.Answers) != 2 || len(value.Answers[0]) != 2 || value.Answers[1] == nil) {
			t.Fatal("ordered native answer matrix was flattened")
		}
		encoded, _ := json.Marshal(value)
		if strings.Contains(string(encoded), "private-sentinel") {
			t.Fatal("private answer escaped serialization")
		}
	}
	for _, raw := range []string{
		`{"sessionID":"` + fixtureSessionID + `","requestID":"` + fixtureQuestionID + `","answers":null}`,
		`{"sessionID":"` + fixtureSessionID + `","requestID":"` + fixtureQuestionID + `","answers":[null]}`,
		`{"sessionID":"` + fixtureSessionID + `","requestID":"` + fixturePermissionID + `","answers":[]}`,
		`{"sessionID":"` + fixtureSessionID + `","requestID":"` + fixtureQuestionID + `","answers":[],"reply":"once"}`,
	} {
		if _, err := decodeNativeInteractionReply(QuestionRepliedEvent, []byte(raw)); err == nil {
			t.Fatal("mixed or malformed closure was accepted")
		}
	}
}
