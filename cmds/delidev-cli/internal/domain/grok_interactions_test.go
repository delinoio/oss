package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGrokOriginalResponseFamiliesRemainExclusive(t *testing.T) {
	for _, raw := range []string{`{"grok":null}`, `{"grok":{"decision":"allow-once"},"decision":null}`, `{"grok":{"decision":"allow-once"},"opencode":null}`} {
		var reply ApprovalResponseInput
		if Decode([]byte(raw), &reply) == nil {
			t.Fatal("mixed or absent Grok response acquired a native family", raw)
		}
	}
	original := ExecutionInteraction{Type: NativeApprovalInteraction, NativeItemID: "original-write", NativeRequestID: InteractionRequestID{Kind: InteractionTextID, Text: string(NewID())}}
	original.Grok = &GrokInteractionRequest{Version: GrokProtocolVersion, Kind: GrokFilePermission, ArrivalID: ID(original.NativeRequestID.Text), RequestDigest: strings.Repeat("ab", 32), ProposalDigest: strings.Repeat("cd", 32), Mode: GrokDefaultMode, ToolName: GrokWrite, Path: "/fixture/file", Content: "Original contents"}
	for _, decision := range []GrokDecision{GrokAllowOnce, GrokAllowEditsSession, GrokRejectOnce} {
		if (ApprovalResponseInput{Grok: &GrokApprovalResponse{Decision: decision}}).ValidateInteraction(original) != nil {
			t.Fatal("native offered decision rejected", decision)
		}
	}
	for _, decision := range []GrokDecision{GrokPlanApproved, "always", "unknown"} {
		if (ApprovalResponseInput{Grok: &GrokApprovalResponse{Decision: decision}}).ValidateInteraction(original) == nil {
			t.Fatal("wrong native decision accepted", decision)
		}
	}
	if (QuestionResponseInput{Grok: &GrokQuestionResponse{Outcome: GrokQuestionCancelled}}).ValidateInteraction(original) == nil {
		t.Fatal("approval acquired question send")
	}
	original.Type = UserQuestionInteraction
	original.Grok.Kind = GrokQuestionInteraction
	original.Grok.ToolName = GrokAsk
	original.Grok.Path = ""
	original.Grok.Content = ""
	original.Grok.Questions = []GrokQuestion{{Question: "Exact native key, 한글", Options: []QuestionOption{{Label: "One", Description: "Original"}}, MultiSelect: nil}}
	answer := GrokQuestionResponse{Outcome: GrokQuestionAccepted, Answers: map[string]string{original.Grok.Questions[0].Question: "Exact, answer\n🙂"}}
	if (QuestionResponseInput{Grok: &answer}).ValidateInteraction(original) != nil {
		t.Fatal("exact native answer rejected")
	}
	raw, _ := json.Marshal(QuestionResponseInput{Grok: &answer})
	var copy QuestionResponseInput
	if Decode(raw, &copy) != nil || copy.Grok.Answers[original.Grok.Questions[0].Question] != answer.Answers[original.Grok.Questions[0].Question] {
		t.Fatal("native text keys or answer changed")
	}
	answer.Answers = map[string]string{"foreign": "One"}
	if answer.Validate(*original.Grok) == nil {
		t.Fatal("foreign answer key acquired authority")
	}
	if Decode([]byte(`{"grok":{"outcome":"cancelled"},"answers":{}}`), &copy) == nil {
		t.Fatal("mixed native response decoded")
	}
}
func TestGrokPlanDigestRetainsNativeJSONEncoding(t *testing.T) {
	thread := NewID()
	content := "# <Original plan>\n🙂"
	plan := GrokPlanRevision{EntryToolID: "entry", EntryEventID: string(thread) + "-5", Revision: 2, WriteToolID: "write-revision", Content: content, ContentDigest: GrokValueDigest(content)}
	if plan.Validate(string(thread)) != nil {
		t.Fatal("original native revision rejected")
	}
	plan.ContentDigest = GrokDigest([]byte(content))
	if plan.Validate(string(thread)) == nil {
		t.Fatal("raw content hash substituted native JSON digest")
	}
}
