package opencode

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func failInteractionTool(r *replyFixture, call string, number int) {
	r.f.t.Helper()
	name := "read"
	if r.f.o.interactions[r.id].value.Kind == QuestionInteraction {
		name = "question"
	}
	r.f.part(r.f.assistantPart(ToolPartKind, number, map[string]any{"callID": call, "tool": name, "state": map[string]any{"status": ToolError, "input": map[string]any{}, "error": "private native error", "time": map[string]any{"start": 1236, "end": 1240}}}))
}

func finishInteractionMessage(r *replyFixture) {
	r.f.t.Helper()
	r.f.part(r.f.assistantPart(StepFinishPartKind, 102, map[string]any{"reason": FinishToolCalls, "cost": 0, "tokens": r.f.a["tokens"]}))
	r.f.a["finish"] = FinishToolCalls
	r.f.a["time"].(map[string]any)["completed"] = 1250
	r.f.message(r.f.a)
}

func TestInteractionContinuationRetainsRejectionWithoutInventingTermination(t *testing.T) {
	for _, kind := range []InteractionKind{PermissionInteraction, QuestionInteraction} {
		for _, policy := range []RejectionPolicy{StopOnInteractionRejection, ContinueOnInteractionRejection} {
			for _, feedback := range []string{"", "private correction"} {
				if kind == QuestionInteraction && feedback != "" {
					continue
				}
				t.Run(string(kind)+"/"+string(policy)+"/feedback="+fmtBool(feedback != ""), func(t *testing.T) {
					r := newReplyFixture(t, kind)
					r.f.o.rejectionPolicy = policy
					r.response = InteractionResponse{Reject: true}
					if kind == PermissionInteraction {
						decision := PermissionReject
						r.response = InteractionResponse{Decision: &decision, Feedback: &feedback}
					}
					if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err != nil {
						t.Fatal(err)
					}
					if _, err := r.f.o.observe(context.Background(), r.closure()); err != nil {
						t.Fatal(err)
					}
					failInteractionTool(r, "call_private", 100)
					finishInteractionMessage(r)
					stopped := policy == StopOnInteractionRejection && feedback == ""
					r.f.status(NativeStatusIdle)
					r.f.observe(SessionIdleEvent, map[string]any{"sessionID": fixtureSessionID})
					if p := r.f.o.snapshot(); !p.RejectedInteraction || p.StoppedOnRejection != stopped || p.SettledObserved != stopped {
						t.Fatal("rejection, stop policy and original idle were conflated")
					}
					if stopped {
						return
					}
					r.f.status(NativeStatusBusy)
					r.f.a = fixtureAssistant()
					r.f.a["id"] = "msg_01960dcbe200ABCDEFGHIJKLMN"
					r.f.message(r.f.a)
					r.f.part(r.f.assistantPart(StepStartPartKind, 200, nil))
					r.f.part(r.f.assistantPart(StepFinishPartKind, 201, map[string]any{"reason": FinishStop, "cost": 0, "tokens": r.f.a["tokens"]}))
					r.f.a["finish"] = FinishStop
					r.f.a["time"].(map[string]any)["completed"] = 1300
					r.f.message(r.f.a)
					r.f.status(NativeStatusIdle)
					r.f.observe(SessionIdleEvent, map[string]any{"sessionID": fixtureSessionID})
					if p := r.f.o.snapshot(); !p.SettledObserved || !p.RejectedInteraction || p.StoppedOnRejection {
						t.Fatal("successor erased prior original rejection or became rejected termination")
					}
				})
			}
		}
	}
}

func TestCorrectionLostHTTPDoesNotManufactureAFeedbackEcho(t *testing.T) {
	r := newReplyFixture(t, PermissionInteraction)
	r.f.o.rejectionPolicy = StopOnInteractionRejection
	decision, feedback := PermissionReject, "private-correction-sentinel"
	r.response = InteractionResponse{Decision: &decision, Feedback: &feedback}
	r.lost = true
	r.onReply = func() {
		if _, err := r.f.o.observe(context.Background(), r.closure()); err != nil {
			t.Error(err)
		}
	}
	receipt, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response)
	if err == nil || !receipt.NativeAccepted || receipt.HTTPAccepted || !receipt.FeedbackRequested {
		t.Fatal("lost HTTP invented exact feedback delivery")
	}
	body, _ := json.Marshal(permissionResponse{Reply: decision, Message: &feedback})
	if r.claims[0].BodyDigest != mutationDigest(body) {
		t.Fatal("feedback escaped original durable body claim")
	}
	serialized, _ := json.Marshal([]any{receipt, r.claims[0], r.response})
	if strings.Contains(string(serialized), feedback) {
		t.Fatal("private correction leaked through typed metadata")
	}
	feedback = "caller mutation"
	if !r.f.o.interactions[r.id].attempt.correction || string(r.f.o.interactions[r.id].attempt.body) != string(body) {
		t.Fatal("caller changed retained feedback scope")
	}
	if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err == nil || r.posts != 1 {
		t.Fatal("lost feedback delivery granted another send")
	}
}

func TestCorrectionDoesNotConvertCascadedRejectionIntoFeedback(t *testing.T) {
	r := newReplyFixture(t, PermissionInteraction)
	r.f.o.rejectionPolicy = StopOnInteractionRejection
	second := addPendingPermission(r)
	decision, feedback := PermissionReject, "private correction"
	r.response = InteractionResponse{Decision: &decision, Feedback: &feedback}
	if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err != nil {
		t.Fatal(err)
	}
	if _, err := r.f.o.observe(context.Background(), r.closure()); err != nil {
		t.Fatal(err)
	}
	r.f.observe(PermissionRepliedEvent, map[string]any{"sessionID": fixtureSessionID, "requestID": second, "reply": PermissionReject})
	failInteractionTool(r, "call_private", 100)
	if r.f.o.snapshot().StoppedOnRejection {
		t.Fatal("direct correction fabricated native stop")
	}
	failInteractionTool(r, "call_second", 101)
	finishInteractionMessage(r)
	if !r.f.o.snapshot().StoppedOnRejection || !r.f.o.snapshot().TerminalObserved || r.f.o.interactions[second].attempt != nil {
		t.Fatal("cascaded plain rejection borrowed the original correction")
	}
}

func TestCorrectionRefusesInvalidScopeBeforeClaim(t *testing.T) {
	for _, mode := range []string{"once", "always", "question", "nul", "oversized", "invalid-utf8", "unknown-policy"} {
		t.Run(mode, func(t *testing.T) {
			kind := PermissionInteraction
			if mode == "question" {
				kind = QuestionInteraction
			}
			r := newReplyFixture(t, kind)
			r.f.o.rejectionPolicy = StopOnInteractionRejection
			decision, feedback := PermissionReject, "private correction"
			switch mode {
			case "once":
				decision = PermissionOnce
			case "always":
				decision = PermissionAlways
			case "nul":
				feedback = "\x00"
			case "oversized":
				feedback = strings.Repeat("x", (64<<10)+1)
			case "invalid-utf8":
				feedback = string([]byte{0xff})
			case "unknown-policy":
				r.f.o.rejectionPolicy = ""
			}
			response := InteractionResponse{Decision: &decision, Feedback: &feedback}
			if kind == QuestionInteraction {
				response = InteractionResponse{Reject: true, Feedback: &feedback}
			}
			if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, response); err == nil || len(r.claims) != 0 || r.posts != 0 {
				t.Fatal("invalid correction consumed native response authority")
			}
		})
	}
}
