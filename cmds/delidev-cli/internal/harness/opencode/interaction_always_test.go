package opencode

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func addPendingPermission(r *replyFixture) string {
	part := r.f.assistantPart(ToolPartKind, 101, map[string]any{"callID": "call_second", "tool": "read", "state": map[string]any{"status": ToolPending, "input": map[string]any{}, "raw": ""}})
	r.f.part(part)
	id := "per_01960dcbe1ffABCDEFGHIJKLMN"
	proposal := fixturePermission()
	proposal["id"] = id
	proposal["tool"] = map[string]any{"messageID": r.f.a["id"], "callID": "call_second"}
	r.f.observe(PermissionAskedEvent, proposal)
	return id
}

func TestPermissionAlwaysKeepsDirectAcceptanceAndPolicyClosureSeparate(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run("lost="+fmtBool(lost), func(t *testing.T) {
			r := newReplyFixture(t, PermissionInteraction)
			second := addPendingPermission(r)
			decision := PermissionAlways
			r.response = InteractionResponse{Decision: &decision}
			r.lost = lost
			if lost {
				r.onReply = func() {
					if _, err := r.f.o.observe(context.Background(), r.closure()); err != nil {
						t.Error(err)
					}
				}
			}
			receipt, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response)
			if (err != nil) != lost || receipt.HTTPAccepted == lost || receipt.NativeAccepted != lost {
				t.Fatal("always conflated HTTP delivery and original native acceptance")
			}
			if !lost {
				if r.f.o.interactions[r.id].alwaysAccepted {
					t.Fatal("HTTP success fabricated native rule acceptance")
				}
				if _, err := r.f.o.observe(context.Background(), r.closure()); err != nil {
					t.Fatal(err)
				}
			}
			if r.f.o.interactions[second].closed {
				t.Fatal("direct acceptance guessed closure of another pending permission")
			}
			observation := r.f.observe(PermissionRepliedEvent, map[string]any{"sessionID": fixtureSessionID, "requestID": second, "reply": PermissionAlways})
			if len(observation.AlwaysObservations) != 1 || observation.AlwaysObservations[0] != r.id || len(observation.RejectionSources) != 0 {
				t.Fatal("policy closure lost prior direct always observations")
			}
			observation.AlwaysObservations[0] = "caller mutation"
			state := r.f.o.interactions[second]
			if !state.closed || state.alwaysAccepted || state.rejected || state.attempt != nil || state.alwaysObservations[0] != r.id || len(r.claims) != 1 || r.posts != 1 {
				t.Fatal("policy closure fabricated another direct response/rule grant or shared private state")
			}
			if _, err := r.f.o.interactionReceipt(second); err == nil {
				t.Fatal("automatic policy closure manufactured a response receipt")
			}
			if _, _, err := r.f.o.prepareInteraction(domain.NewID(), second, r.response); err == nil || r.f.o.snapshot().TerminalObserved {
				t.Fatal("policy closure granted resend or root completion")
			}
		})
	}
}

func TestPermissionAlwaysClosureNeedsOriginalNativeEvidence(t *testing.T) {
	for _, mode := range []string{"unclaimed", "http-only", "once", "empty-rules", "attempted-target", "wrong-decision"} {
		t.Run(mode, func(t *testing.T) {
			r := newReplyFixture(t, PermissionInteraction)
			second := addPendingPermission(r)
			decision := PermissionAlways
			if mode == "once" {
				decision = PermissionOnce
			}
			if mode == "empty-rules" {
				r.f.o.interactions[r.id].value.Permission.Always = []string{}
			}
			r.response = InteractionResponse{Decision: &decision}
			if mode != "unclaimed" {
				if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err != nil {
					t.Fatal(err)
				}
				if mode != "http-only" {
					if _, err := r.f.o.observe(context.Background(), r.closure()); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "attempted-target" {
				if _, _, err := r.f.o.prepareInteraction(domain.NewID(), second, r.response); err != nil {
					t.Fatal(err)
				}
			}
			closureDecision := PermissionAlways
			if mode == "wrong-decision" {
				closureDecision = PermissionOnce
			}
			raw, _ := json.Marshal(map[string]any{"sessionID": fixtureSessionID, "requestID": second, "reply": closureDecision})
			_, err := r.f.o.observe(context.Background(), NativeEvent{ID: "evt_01960dcbe1ffABCDEFGHIJKLMN", Kind: PermissionRepliedEvent, Properties: raw})
			if err == nil || !r.f.o.snapshot().NeedsRecovery || r.f.o.interactions[second].closed {
				t.Fatal("unsupported policy closure gained native acceptance")
			}
		})
	}
}

func TestPermissionAlwaysDoesNotOverrideAnOriginalOnceClaim(t *testing.T) {
	r := newReplyFixture(t, PermissionInteraction)
	if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err != nil {
		t.Fatal(err)
	}
	decision := PermissionAlways
	r.response = InteractionResponse{Decision: &decision}
	if _, err := r.f.o.observe(context.Background(), r.closure()); err == nil || r.f.o.interactions[r.id].alwaysAccepted {
		t.Fatal("native always closure rewrote the original once response claim")
	}
}
