// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"testing"
)

func appCallTestIdentity() CodexAppCallIdentity {
	return CodexAppCallIdentity{AccountID: NewID(), Generation: NewID(), AppID: "original-app", Tool: "original-tool", Arguments: json.RawMessage(`{"original":true}`)}
}

func TestCodexAppCallCannotChangeAccountGenerationOrArguments(t *testing.T) {
	identity := appCallTestIdentity()
	prior := ToolSnapshot{Kind: CodexAppTool, Status: ToolRunning, CodexApp: &CodexAppCallObservation{Identity: identity}}
	next := ToolSnapshot{Kind: CodexAppTool, Status: ToolCompleted, CodexApp: &CodexAppCallObservation{Identity: identity, Result: &CodexAppResult{Content: []json.RawMessage{json.RawMessage(`{"type":"text","text":"original result"}`)}, Structured: json.RawMessage(`null`)}}}
	if ValidateCodexAppCallTransition(prior, next) != nil {
		t.Fatal("original result rejected")
	}
	for _, mutate := range []func(*CodexAppCallIdentity){
		func(i *CodexAppCallIdentity) { i.AccountID = NewID() },
		func(i *CodexAppCallIdentity) { i.Generation = NewID() },
		func(i *CodexAppCallIdentity) { i.AppID = "foreign-app" },
		func(i *CodexAppCallIdentity) { i.Tool = "foreign-tool" },
		func(i *CodexAppCallIdentity) { i.Arguments = json.RawMessage(`{"original":false}`) },
	} {
		changed := *next.CodexApp
		changed.Identity = identity
		mutate(&changed.Identity)
		foreign := next
		foreign.CodexApp = &changed
		if ValidateCodexAppCallTransition(prior, foreign) == nil {
			t.Fatal("foreign result adopted original call")
		}
	}
	if ValidateCodexAppCallTransition(next, next) == nil {
		t.Fatal("completed call mutated again")
	}
}

func TestCodexAppQuestionIsOneOriginalCallApproval(t *testing.T) {
	question := Question{ID: CodexAppApprovalQuestionPrefix + "original-native-request", Header: "Approve app tool call?", Text: "Run the original app?", Options: []QuestionOption{{Label: CodexAppApprovalAllow}, {Label: CodexAppApprovalCancel}}}
	request := QuestionRequest{Blocking: true, Questions: []Question{question}, CodexApp: &CodexAppApprovalContext{NativeCallID: "original-call", Identity: appCallTestIdentity()}}
	if request.Validate() != nil {
		t.Fatal("original app approval rejected")
	}
	for _, answers := range [][]string{{CodexAppApprovalAllow}, {CodexAppApprovalCancel}} {
		if (QuestionResponseInput{Answers: map[string][]string{question.ID: answers}}).Validate(&request) != nil {
			t.Fatal("original one-call choice rejected")
		}
	}
	for _, answers := range [][]string{{}, {CodexAppApprovalAllow, CodexAppApprovalCancel}, {"Allow for this session"}, {"Allow and don't ask me again"}} {
		if (QuestionResponseInput{Answers: map[string][]string{question.ID: answers}}).Validate(&request) == nil {
			t.Fatal("ambiguous or remembered app approval accepted")
		}
	}
	for _, change := range []func(*QuestionRequest){
		func(r *QuestionRequest) { r.Blocking = false },
		func(r *QuestionRequest) { r.Questions[0].Other = true },
		func(r *QuestionRequest) { r.Questions[0].Secret = true },
		func(r *QuestionRequest) { r.Questions[0].ID = "ordinary-question" },
		func(r *QuestionRequest) { r.Questions[0].Options[0].Label = "Allow for this session" },
	} {
		raw, _ := json.Marshal(request)
		var changed QuestionRequest
		if Decode(raw, &changed) != nil {
			t.Fatal("fixture clone")
		}
		change(&changed)
		if changed.Validate() == nil {
			t.Fatal("foreign or broader approval accepted")
		}
	}
}
