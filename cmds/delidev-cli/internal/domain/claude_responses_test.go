package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func claudeResponseOriginal(question bool) ExecutionInteraction {
	u := claudeInteractionFixture()
	if question {
		u.Type, u.Claude.Kind, u.Claude.Tool.Name = UserQuestionInteraction, ClaudeUserQuestion, "AskUserQuestion"
		u.Claude.InputJSON = `{"questions":[{"question":"Original?","header":"Choice","options":[{"label":"One","description":"First"},{"label":"Two","description":"Second"}],"multiSelect":true}]}`
	}
	return ExecutionInteraction{Type: u.Type, NativeItemID: u.NativeItemID, NativeRequestID: u.NativeRequestID, Claude: u.Claude}
}
func TestClaudeResponseKeepsNativeQuestionKeysAndExplicitEmptyAnswers(t *testing.T) {
	original := claudeResponseOriginal(true)
	for _, answers := range []map[string]string{{}, {"Original?": ""}, {"Original?": "One, Two"}, {"Original?": "  답변\n"}} {
		r := QuestionResponseInput{Claude: &ClaudePermissionResponse{Behavior: ClaudeReplyAllow, Answers: answers}}
		if err := r.ValidateInteraction(original); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(r)
		var copy QuestionResponseInput
		if Decode(raw, &copy) != nil || copy.Claude == nil || copy.Claude.Answers == nil || copy.ValidateInteraction(original) != nil || strings.Contains(string(raw), `"answers":null`) {
			t.Fatal("original question answers lost", string(raw))
		}
		first, _ := ClaudeResponseDigest(original, *r.Claude)
		second, _ := ClaudeResponseDigest(original, *copy.Claude)
		if first == "" || first != second {
			t.Fatal("response digest changed after retention")
		}
	}
}
func TestClaudeResponseRejectsMixedAndMalformedAnswers(t *testing.T) {
	for _, raw := range []string{
		`{"claude":null}`, `{"claude":{"behavior":"allow","answers":null}}`, `{"claude":{"behavior":"allow","answers":{"Original?":null}}}`,
		`{"claude":{"behavior":"allow","answers":{"foreign":"One"}}}`, `{"claude":{"behavior":"allow"}}`, `{"claude":{"behavior":"allow","answers":{},"interrupt":false}}`,
		`{"claude":{"behavior":"allow","answers":{},"updatedInput":{}}}`, `{"claude":{"behavior":"allow","answers":{}},"answers":{}}`, `{"claude":{"behavior":"allow","answers":{}},"opencode":{"answers":[]}}`,
		`{"claude":{"behavior":"deny","message":null}}`, `{"claude":{"behavior":"deny","message":""}}`, `{"claude":{"behavior":"deny","message":"No","answers":{}}}`, `{"claude":{"behavior":"deny","message":"No","interrupt":null}}`,
	} {
		var r QuestionResponseInput
		if Decode([]byte(raw), &r) == nil && r.ValidateInteraction(claudeResponseOriginal(true)) == nil {
			t.Fatal("invalid response accepted", raw)
		}
	}
	r := QuestionResponseInput{Claude: &ClaudePermissionResponse{Behavior: ClaudeReplyAllow, Answers: map[string]string{"Original?": strings.Repeat("x", MaxQuestionResponseBytes)}}}
	if r.ValidateInteraction(claudeResponseOriginal(true)) == nil {
		t.Fatal("unbounded complete response accepted")
	}
}
func TestClaudeResponseKeepsOriginalPermissionInputAndDenial(t *testing.T) {
	o := claudeResponseOriginal(false)
	allow := ClaudePermissionResponse{Behavior: ClaudeReplyAllow}
	first, err := ClaudeResponseDigest(o, allow)
	if err != nil {
		t.Fatal(err)
	}
	o.Claude.InputJSON = `{"exact":9007199254740993,"command":"printf original"}`
	second, err := ClaudeResponseDigest(o, allow)
	if err != nil || first != second {
		t.Fatal("native object ordering changed response digest", err)
	}
	o.Claude.InputJSON = `{"exact":9007199254740992,"command":"printf original"}`
	third, _ := ClaudeResponseDigest(o, allow)
	if third == first {
		t.Fatal("native exact number rounded")
	}
	message, interrupt := "  Original correction\n", false
	deny := ApprovalResponseInput{Claude: &ClaudePermissionResponse{Behavior: ClaudeReplyDeny, Message: &message, Interrupt: &interrupt}}
	if err := deny.ValidateInteraction(o); err != nil {
		t.Fatal(err)
	}
	interrupt = true
	if deny.ValidateInteraction(o) == nil {
		t.Fatal("uncomposed interruption context gained reply authority")
	}
	interrupt = false
	deny.Decision = &CodexApprovalDecision{Kind: CodexApprovalAccept}
	if deny.ValidateInteraction(o) == nil {
		t.Fatal("mixed native decision accepted")
	}
	deny.Decision = nil
	o.Claude = nil
	if deny.ValidateInteraction(o) == nil {
		t.Fatal("Claude response acquired another harness request")
	}
}
