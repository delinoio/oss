package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func nativeResponseQuestionFixture() ExecutionInteraction {
	return ExecutionInteraction{Type: UserQuestionInteraction, NativeRequestID: InteractionRequestID{Kind: InteractionTextID, Text: "que_01960dcbe1faABCDEFGHIJKLMN"}, OpenCode: &OpenCodeInteractionRequest{Version: OpenCodeProtocolVersion, NativeEventID: "evt_01960dcbe1faABCDEFGHIJKLMN", NativeMessageID: "msg_01960dcbe1faABCDEFGHIJKLMN", CallID: "original", Questions: []OpenCodeQuestion{{Text: "Original", Header: "Choice", Options: []QuestionOption{{Label: "First"}, {Label: "Second"}}}}}}
}

func TestOpenCodeQuestionResponsesKeepMatrixFlagsAndExactText(t *testing.T) {
	original := nativeResponseQuestionFixture()
	for _, values := range [][][]string{{{"First"}}, {{""}}, {{" 답변\n"}}, {{}}} {
		response := QuestionResponseInput{OpenCode: &OpenCodeQuestionResponse{Answers: values}}
		if err := response.ValidateInteraction(original); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(response)
		var restored QuestionResponseInput
		if Decode(raw, &restored) != nil || !reflect.DeepEqual(response, restored) || strings.Contains(string(raw), `"answers":null`) {
			t.Fatal("native matrix changed during storage")
		}
	}
	allowed := true
	original.OpenCode.Questions[0].Multiple = &allowed
	response := QuestionResponseInput{OpenCode: &OpenCodeQuestionResponse{Answers: [][]string{{"Second", "First"}}}}
	if response.ValidateInteraction(original) != nil {
		t.Fatal("native multiple selection was lost")
	}
	allowed = false
	if response.ValidateInteraction(original) == nil {
		t.Fatal("single question accepted multiple answers")
	}
	original.OpenCode.Questions[0].Custom = &allowed
	response.OpenCode.Answers = [][]string{{"Other"}}
	if response.ValidateInteraction(original) == nil {
		t.Fatal("disabled custom answer was accepted")
	}
	original.OpenCode.Questions[0].Options = append(original.OpenCode.Questions[0].Options, QuestionOption{Label: "First"})
	response.OpenCode.Answers = [][]string{{"First"}}
	if response.ValidateInteraction(original) == nil {
		t.Fatal("ambiguous native labels were accepted")
	}
	original.OpenCode.Questions = []OpenCodeQuestion{}
	response.OpenCode.Answers = [][]string{}
	if response.ValidateInteraction(original) != nil {
		t.Fatal("explicit empty native matrix was dropped")
	}
}

func TestOpenCodeResponseDecodeRejectsAmbiguousOrMixedShapes(t *testing.T) {
	for _, raw := range []string{`{"opencode":null}`, `{"opencode":{"answers":null}}`, `{"opencode":{"answers":[null]}}`, `{"opencode":{"answers":[[null]]}}`, `{"opencode":{"Answers":[]}}`, `{"opencode":{"answers":[],"reject":true}}`, `{"opencode":{"answers":[]},"answers":{}}`, `{"opencode":{"answers":[]},"Answers":{}}`} {
		var response QuestionResponseInput
		if Decode([]byte(raw), &response) == nil {
			t.Fatalf("accepted malformed response %s", raw)
		}
	}
	original := nativeResponseQuestionFixture()
	for _, values := range [][][]string{nil, {}, {nil}, {{"First", "First"}}, {{strings.Repeat("x", (64<<10)+1)}}} {
		if (QuestionResponseInput{OpenCode: &OpenCodeQuestionResponse{Answers: values}}).ValidateInteraction(original) == nil {
			t.Fatal("invalid native answer was accepted")
		}
	}
	for _, raw := range []string{`{"opencode":{"decision":"always"}}`, `{"opencode":{"decision":"reject"}}`, `{"opencode":{"decision":"once","feedback":"text"}}`, `{"opencode":{"Decision":"once"}}`} {
		var response ApprovalResponseInput
		if Decode([]byte(raw), &response) == nil {
			t.Fatal("unimplemented native permission profile was accepted")
		}
	}
}

func TestOpenCodeResponsesCannotEnterCodexEncoders(t *testing.T) {
	q := QuestionResponseInput{OpenCode: &OpenCodeQuestionResponse{Answers: [][]string{}}}
	if q.Validate(&QuestionRequest{Questions: []Question{{ID: "question"}}}) == nil {
		t.Fatal("native matrix became a Codex map")
	}
	a := ApprovalResponseInput{OpenCode: &OpenCodePermissionResponse{Decision: OpenCodePermissionOnce}}
	if a.Validate(nil) == nil {
		t.Fatal("native permission became a Codex approval")
	}
}

func TestOpenCodeReplyProofBindsBodyAndOriginalNamespaces(t *testing.T) {
	question := &OpenCodeQuestionResponse{Answers: [][]string{{"Second", "First"}}}
	digest, err := OpenCodeResponseDigest(question, nil)
	other, _ := OpenCodeResponseDigest(&OpenCodeQuestionResponse{Answers: [][]string{{"First", "Second"}}}, nil)
	if err != nil || digest == other {
		t.Fatal("original ordered body digest was lost")
	}
	evidence := OpenCodeReplyEvidence{NativeEventID: "evt_01960dcbe1fbABCDEFGHIJKLMN", ProposalEventID: "evt_01960dcbe1faABCDEFGHIJKLMN", NativeRequestID: "que_01960dcbe1faABCDEFGHIJKLMN", BodyDigest: digest, HTTPAccepted: true}
	if evidence.Validate(UserQuestionInteraction) != nil || evidence.Validate(NativeApprovalInteraction) == nil {
		t.Fatal("original reply namespaces changed")
	}
	evidence.HTTPAccepted = false
	if evidence.Validate(UserQuestionInteraction) == nil {
		t.Fatal("native event fabricated exact HTTP body acceptance")
	}
}
