package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"google.golang.org/protobuf/proto"
)

func grokCLIOriginal(id domain.ID, kind domain.GrokInteractionKind) domain.ExecutionInteraction {
	mode, tool, interaction := domain.GrokDefaultMode, domain.GrokWrite, domain.NativeApprovalInteraction
	if kind == domain.GrokQuestionInteraction {
		tool, interaction = domain.GrokAsk, domain.UserQuestionInteraction
	}
	if kind == domain.GrokPlanApproval {
		tool, mode = domain.GrokExitPlan, domain.GrokPlanMode
	}
	value := domain.ExecutionInteraction{ExecutionID: domain.NewID(), NativeThreadID: string(domain.NewID()), NativeTurnID: "00000000-0000-4000-8000-000000000001", NativeItemID: "original-tool", NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: string(id)}, Type: interaction, Closure: domain.InteractionOpen, Grok: &domain.GrokInteractionRequest{Version: domain.GrokProtocolVersion, Kind: kind, ArrivalID: id, RequestDigest: strings.Repeat("ab", 32), ProposalDigest: strings.Repeat("cd", 32), Mode: mode, ToolName: tool}}
	switch kind {
	case domain.GrokFilePermission:
		value.Grok.Path, value.Grok.Content = "/fixture/file", "Original contents"
	case domain.GrokQuestionInteraction:
		value.Grok.Questions = []domain.GrokQuestion{{Question: "Original, exact question?", Options: []domain.QuestionOption{{Label: "One", Description: "Original"}}}}
	case domain.GrokPlanApproval:
		value.Grok.Plan = &domain.GrokPlanRevision{EntryToolID: "entry", EntryEventID: value.NativeThreadID + "-5", Revision: 2, WriteToolID: "write", Content: "Original Plan", ContentDigest: domain.GrokValueDigest("Original Plan")}
	}
	return value
}

func TestCLIGrokApprovalPreservesDecisionAndClosedReceipt(t *testing.T) {
	for _, kind := range []domain.GrokInteractionKind{domain.GrokFilePermission, domain.GrokPlanApproval} {
		t.Run(string(kind), func(t *testing.T) {
			f := newApprovalCLIFixture(t)
			value := grokCLIOriginal(domain.ID(f.resource.Id), kind)
			f.resource.DocumentJson, _ = json.Marshal(value)
			decision := domain.GrokAllowEditsSession
			if kind == domain.GrokPlanApproval {
				decision = domain.GrokPlanApproved
			}
			raw, _ := json.Marshal(domain.ApprovalResponseInput{Grok: &domain.GrokApprovalResponse{Decision: decision}})
			args := []string{"interaction", "approve", "--id", f.resource.Id, "--revision", "1", "--request-id", string(domain.NewID())}
			if code, output := cliRun(t, f.root, args, string(raw)); code != 0 {
				t.Fatal("original Grok approval failed", code, output)
			}
			f.mu.Lock()
			first := proto.Clone(f.requests[0])
			value.Closure = domain.InteractionNativeClosed
			f.resource.DocumentJson, _ = json.Marshal(value)
			f.resource.Revision = 5
			f.mu.Unlock()
			if code, output := cliRun(t, f.root, args, string(raw)); code != 0 || output["result"].(map[string]any)["replayed"] != true {
				t.Fatal("closed Grok approval lost receipt", code, output)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.requests) != 2 || !proto.Equal(first, f.requests[1]) {
				t.Fatal("receipt retry changed native decision or revision")
			}
		})
	}
}

func TestCLIGrokQuestionKeepsExactNativeKeysBeforeRPC(t *testing.T) {
	f := newQuestionCLIFixture(t)
	value := grokCLIOriginal(domain.ID(f.resource.Id), domain.GrokQuestionInteraction)
	f.resource.DocumentJson, _ = json.Marshal(value)
	args := []string{"interaction", "respond", "--id", f.resource.Id, "--revision", "1"}
	if code, _ := cliRun(t, f.root, args, `{"grok":{"outcome":"accepted","answers":{"foreign":"One"}}}`); code == 0 {
		t.Fatal("foreign Grok answer gained RPC authority")
	}
	f.mu.Lock()
	if len(f.requests) != 0 {
		t.Fatal("foreign answer reached the response RPC")
	}
	f.mu.Unlock()
	raw, _ := json.Marshal(domain.QuestionResponseInput{Grok: &domain.GrokQuestionResponse{Outcome: domain.GrokQuestionAccepted, Answers: map[string]string{"Original, exact question?": " Exact 답변, 🙂\n"}}})
	if code, output := cliRun(t, f.root, args, string(raw)); code != 0 {
		t.Fatal("exact Grok answer failed", code, output)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var answer domain.QuestionResponseInput
	if len(f.requests) != 1 || domain.Decode(f.requests[0].ResponseJson, &answer) != nil || answer.Grok.Answers["Original, exact question?"] != " Exact 답변, 🙂\n" {
		t.Fatal("native keys or answer content changed")
	}
}
