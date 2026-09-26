package opencode

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestInteractionRejectionRequiresExplicitNativePolicyAndToolFailure(t *testing.T) {
	for _, kind := range []InteractionKind{PermissionInteraction, QuestionInteraction} {
		t.Run(string(kind), func(t *testing.T) {
			r := newReplyFixture(t, kind)
			r.response = InteractionResponse{Reject: true}
			if kind == PermissionInteraction {
				decision := PermissionReject
				r.response = InteractionResponse{Decision: &decision}
			}
			if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err == nil {
				t.Fatal("unknown native continue-on-deny setting granted rejection semantics")
			}
			r.f.o.rejectionPolicy = StopOnInteractionRejection
			receipt, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response)
			if err != nil || !receipt.HTTPAccepted || receipt.NativeAccepted {
				t.Fatalf("rejection delivery: %v", err)
			}
			if _, err := r.f.o.observe(context.Background(), r.closure()); err != nil {
				t.Fatal(err)
			}
			if r.f.o.snapshot().RejectedInteraction || r.f.o.snapshot().TerminalObserved {
				t.Fatal("reply alone fabricated original tool failure or root terminal")
			}
			tool := "read"
			if kind == QuestionInteraction {
				tool = "question"
			}
			part := r.f.assistantPart(ToolPartKind, 100, map[string]any{"callID": "call_private", "tool": tool, "state": map[string]any{"status": ToolError, "input": map[string]any{}, "error": "private error", "time": map[string]any{"start": 1236, "end": 1240}}})
			r.f.part(part)
			if !r.f.o.snapshot().RejectedInteraction || r.f.o.snapshot().TerminalObserved {
				t.Fatal("original failure lost rejection or skipped native message completion")
			}
			r.f.part(r.f.assistantPart(StepFinishPartKind, 101, map[string]any{"reason": FinishToolCalls, "cost": 0, "tokens": r.f.a["tokens"]}))
			r.f.a["finish"] = FinishToolCalls
			r.f.a["time"].(map[string]any)["completed"] = 1250
			r.f.message(r.f.a)
			if !r.f.o.snapshot().TerminalObserved || r.f.o.snapshot().SettledObserved {
				t.Fatal("rejection terminal skipped independent idle")
			}
			r.f.status(NativeStatusIdle)
			r.f.observe(SessionIdleEvent, map[string]any{"sessionID": fixtureSessionID})
			if !r.f.o.snapshot().SettledObserved {
				t.Fatal("original denied input did not settle")
			}
			if *r.f.o.messages[r.f.a["id"].(string)].value.Assistant.Finish != FinishToolCalls {
				t.Fatal("rejection rewrote native finish spelling")
			}
		})
	}
}

func TestPermissionRejectionCascadeCannotInventAnotherResponse(t *testing.T) {
	r := newReplyFixture(t, PermissionInteraction)
	r.f.o.rejectionPolicy = StopOnInteractionRejection
	part := r.f.assistantPart(ToolPartKind, 101, map[string]any{"callID": "call_second", "tool": "read", "state": map[string]any{"status": ToolPending, "input": map[string]any{}, "raw": ""}})
	r.f.part(part)
	secondID := "per_01960dcbe1ffABCDEFGHIJKLMN"
	proposal := fixturePermission()
	proposal["id"] = secondID
	proposal["tool"] = map[string]any{"messageID": r.f.a["id"], "callID": "call_second"}
	r.f.observe(PermissionAskedEvent, proposal)
	decision := PermissionReject
	r.response = InteractionResponse{Decision: &decision}
	if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.f.o.prepareInteraction(domain.NewID(), secondID, r.response); err == nil {
		t.Fatal("new response raced the original unobserved acknowledgment")
	}
	if _, err := r.f.o.observe(context.Background(), r.closure()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.f.o.prepareInteraction(domain.NewID(), secondID, r.response); err == nil {
		t.Fatal("known pending cascade granted another native send")
	}
	closure := r.f.observe(PermissionRepliedEvent, map[string]any{"sessionID": fixtureSessionID, "requestID": secondID, "reply": PermissionReject})
	if len(closure.RejectionSources) != 1 || closure.RejectionSources[0] != r.id {
		t.Fatal("cascade lost its original observed rejection source")
	}
	closure.RejectionSources[0] = "caller mutation"
	state := r.f.o.interactions[secondID]
	if state.attempt != nil || !state.closed || !state.rejected || state.rejectionSources[0] != r.id {
		t.Fatal("cascade invented response authority or retained caller alias")
	}
	if _, err := r.f.o.interactionReceipt(secondID); err == nil {
		t.Fatal("cascade manufactured a response receipt")
	}
}

func TestQuestionRejectionCannotAcknowledgeAnAnswerOrPermissionCascade(t *testing.T) {
	r := newReplyFixture(t, QuestionInteraction)
	if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"sessionID": fixtureSessionID, "requestID": r.id})
	if _, err := r.f.o.observe(context.Background(), NativeEvent{ID: "evt_01960dcbe1ffABCDEFGHIJKLMN", Kind: QuestionRejectedEvent, Properties: raw}); err == nil {
		t.Fatal("native dismissal acknowledged a submitted answer")
	}
}
