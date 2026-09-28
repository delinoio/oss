package opencode

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestPendingInteractionDisappearanceDoesNotProveAcceptanceOrRestoreSend(t *testing.T) {
	r := newReplyFixture(t, QuestionInteraction)
	r.pendingBody = []byte(`[]`)
	if receipt, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err == nil || receipt.RequestID != "" {
		t.Fatal("missing native request consumed an answer or inferred acceptance")
	}
	if r.f.o.interactions[r.id].closed || !r.f.o.interactions[r.id].pendingAbsent || r.f.o.snapshot().TerminalObserved {
		t.Fatal("missing pending request acquired terminal meaning")
	}
	r.pendingBody = nil
	if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err == nil {
		t.Fatal("request reappearance silently regained response authority")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.posts != 0 || len(r.claims) != 0 {
		t.Fatal("uncertain original request was replied to")
	}
}

func TestPendingInteractionRequiresExactOriginalProposalAndCompleteInventory(t *testing.T) {
	for _, mode := range []string{"changed-question", "duplicate", "null", "wrong-namespace", "unknown-field"} {
		t.Run(mode, func(t *testing.T) {
			r := newReplyFixture(t, QuestionInteraction)
			fields, _ := object(r.f.o.interactions[r.id].raw)
			switch mode {
			case "changed-question":
				fields["questions"] = json.RawMessage(`[]`)
			case "unknown-field":
				fields["extra"] = json.RawMessage(`false`)
			case "wrong-namespace":
				fields["id"] = json.RawMessage(`"` + fixturePermissionID + `"`)
			}
			changed, _ := json.Marshal(fields)
			entries := []json.RawMessage{changed}
			if mode == "duplicate" {
				entries = append(entries, changed)
			}
			r.pendingBody, _ = json.Marshal(entries)
			if mode == "null" {
				r.pendingBody = []byte(`null`)
			}
			if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err == nil || !r.f.o.snapshot().NeedsRecovery {
				t.Fatal("contradictory pending inventory regained original reply authority")
			}
			r.pendingBody = nil
			if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err == nil {
				t.Fatal("matching later inventory erased contradiction")
			}
		})
	}
}

func TestPendingInteractionLiveClosureWinsRacingOlderInventory(t *testing.T) {
	r := newReplyFixture(t, QuestionInteraction)
	if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err != nil {
		t.Fatal(err)
	}
	r.onPending = func() {
		if _, err := r.f.o.observe(context.Background(), r.closure()); err != nil {
			t.Error(err)
		}
	}
	if _, err := r.api.pendingInteraction(context.Background(), r.f.o, r.id); err == nil {
		t.Fatal("older inventory overrode original live closure")
	}
	receipt, _ := r.f.o.interactionReceipt(r.id)
	if !receipt.NativeAccepted || r.f.o.snapshot().NeedsRecovery {
		t.Fatal("racing read erased accepted closure or fabricated contradiction")
	}
}
