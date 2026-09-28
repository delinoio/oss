package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestOpenCodePolicyBodiesPreserveRejectAndOptionalCorrection(t *testing.T) {
	empty, feedback := "", " 원래 수정\n"
	for _, test := range []struct {
		response OpenCodePermissionResponse
		body     string
	}{
		{OpenCodePermissionResponse{Decision: OpenCodePermissionOnce}, `{"reply":"once"}`},
		{OpenCodePermissionResponse{Decision: OpenCodePermissionAlways}, `{"reply":"always"}`},
		{OpenCodePermissionResponse{Decision: OpenCodePermissionReject}, `{"reply":"reject"}`},
		{OpenCodePermissionResponse{Decision: OpenCodePermissionReject, Feedback: &empty}, `{"reply":"reject","message":""}`},
		{OpenCodePermissionResponse{Decision: OpenCodePermissionReject, Feedback: &feedback}, "{\"reply\":\"reject\",\"message\":\" 원래 수정\\n\"}"},
	} {
		raw, _ := json.Marshal(test.response)
		var restored OpenCodePermissionResponse
		if Decode(raw, &restored) != nil || !reflect.DeepEqual(test.response, restored) {
			t.Fatal("native policy changed during storage")
		}
		digest, err := OpenCodeResponseDigest(nil, &restored)
		expected := sha256.Sum256([]byte(test.body))
		if err != nil || digest != hex.EncodeToString(expected[:]) {
			t.Fatal("native policy lost exact optional feedback/body bytes")
		}
	}
	question := QuestionResponseInput{OpenCode: &OpenCodeQuestionResponse{Reject: true}}
	if question.ValidateInteraction(nativeResponseQuestionFixture()) != nil {
		t.Fatal("original question could not be rejected")
	}
	raw, _ := json.Marshal(question)
	if string(raw) != `{"opencode":{"reject":true}}` {
		t.Fatal("native question rejection fabricated an answer matrix")
	}
	digest, err := OpenCodeResponseDigest(question.OpenCode, nil)
	emptyBody := sha256.Sum256(nil)
	if err != nil || digest != hex.EncodeToString(emptyBody[:]) {
		t.Fatal("question rejection did not preserve its empty native HTTP body")
	}
}

func TestOpenCodePoliciesRejectAmbiguousAndOversizedResponses(t *testing.T) {
	for _, raw := range []string{`{"reject":false}`, `{"reject":null}`, `{"Reject":true}`, `{"reject":true,"answers":[]}`, `{"reject":true,"answers":null}`} {
		var response OpenCodeQuestionResponse
		if Decode([]byte(raw), &response) == nil {
			t.Fatal("ambiguous native rejection accepted")
		}
	}
	for _, raw := range []string{`{"decision":"reject","feedback":null}`, `{"decision":"reject","Feedback":"x"}`, `{"decision":"always","feedback":""}`, `{"decision":"REJECT"}`, `{"decision":"reject","message":"x"}`} {
		var response OpenCodePermissionResponse
		if Decode([]byte(raw), &response) == nil {
			t.Fatal("ambiguous native permission accepted")
		}
	}
	original := nativeResponseQuestionFixture()
	original.Type, original.NativeRequestID.Text = NativeApprovalInteraction, "per_01960dcbe1faABCDEFGHIJKLMN"
	original.OpenCode.Questions = nil
	original.OpenCode.Permission = &OpenCodePermission{Name: "read", Patterns: []string{}, Always: []string{}, MetadataJSON: `{}`}
	for _, feedback := range []string{strings.Repeat("x", (64<<10)+1), "invalid\x00text", "invalid\xff", strings.Repeat("\x01", 64<<10)} {
		response := ApprovalResponseInput{OpenCode: &OpenCodePermissionResponse{Decision: OpenCodePermissionReject, Feedback: &feedback}}
		if response.ValidateInteraction(original) == nil {
			t.Fatal("invalid or oversized complete correction was accepted")
		}
	}
}

func TestOpenCodeAutomaticClosureRequiresBoundedDistinctOriginalContext(t *testing.T) {
	valid := func() OpenCodePolicyClosure {
		return OpenCodePolicyClosure{NativeEventID: "evt_01960dcbe1fcABCDEFGHIJKLMN", ProposalEventID: "evt_01960dcbe1fbABCDEFGHIJKLMN", Decision: OpenCodePermissionAlways, Sources: []OpenCodePolicySource{{InteractionID: NewID(), NativeRequestID: "per_01960dcbe1faABCDEFGHIJKLMN"}}}
	}
	if valid().Validate() != nil {
		t.Fatal("valid original policy context rejected")
	}
	for _, name := range []string{"once", "empty", "duplicate-id", "duplicate-native", "foreign-event", "same-event", "question", "capacity"} {
		p := valid()
		switch name {
		case "once":
			p.Decision = OpenCodePermissionOnce
		case "empty":
			p.Sources = nil
		case "duplicate-id":
			p.Sources = append(p.Sources, OpenCodePolicySource{InteractionID: p.Sources[0].InteractionID, NativeRequestID: "per_01960dcbe1fbABCDEFGHIJKLMN"})
		case "duplicate-native":
			p.Sources = append(p.Sources, OpenCodePolicySource{InteractionID: NewID(), NativeRequestID: p.Sources[0].NativeRequestID})
		case "foreign-event":
			p.NativeEventID = string(NewID())
		case "same-event":
			p.NativeEventID = p.ProposalEventID
		case "question":
			p.Sources[0].NativeRequestID = "que_01960dcbe1faABCDEFGHIJKLMN"
		case "capacity":
			p.Sources = make([]OpenCodePolicySource, 1025)
		}
		if p.Validate() == nil {
			t.Fatalf("invalid closure context accepted: %s", name)
		}
	}
}
