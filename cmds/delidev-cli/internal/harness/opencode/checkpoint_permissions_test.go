package opencode

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func checkpointPermissionFixture(t *testing.T, decisions ...PermissionDecision) (nativeCheckpoint, *sessionAPI, *observedInteraction) {
	t.Helper()
	r := newReplyFixture(t, PermissionInteraction)
	always := len(decisions) == 1 && decisions[0] == PermissionAlways
	if always {
		*r.response.Decision = PermissionAlways
		r.api.creation.settings.Permission = []PermissionRule{}
	}
	if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err != nil {
		t.Fatal(err)
	}
	if _, err := r.f.o.observe(context.Background(), r.closure()); err != nil {
		t.Fatal(err)
	}
	o := r.f.o
	i := o.interactions[r.id]
	claim := i.attempt.claim
	part := o.parts[claim.PartID]
	part.value.Tool = checkpointInlineToolFixture(checkpointReadTool)
	part.value.Tool.CallID = claim.CallID
	value := nativeCheckpoint{Project: "global", NativeRoot: fixtureNativeRoot(), History: HistoryObservation{RequestID: claim.InputRequestID, SessionID: claim.SessionID, Messages: []HistoryMessage{{ID: claim.MessageID, Parts: []HistoryPart{{ID: claim.PartID, Kind: ToolPartKind, Digest: mutationDigest(part.raw)}}}}}}
	value.Tools = r.api.checkpointToolHistory(value)
	valid := value.Tools != nil && !value.Tools.InteractionFree && checkpointReplacementProfile(value) == nil
	if valid && always {
		valid = value.Tools.Version == 3 && len(value.Tools.Always) == 1 && value.Tools.Always[0].Claim == claim
	} else if valid {
		valid = value.Tools.Version == 2 && len(value.Tools.Once) == 1 && value.Tools.Once[0] == claim
	}
	if !valid {
		t.Fatal("independent original one-time acceptance missing")
	}
	return value, r.api, i
}

func TestCheckpointPermissionCaptureRequiresOriginalOneTimeAcceptance(t *testing.T) {
	for _, fault := range []string{"pending", "canceled", "rejected", "always", "policy", "pending-absent", "unsent", "http", "native", "feedback", "decision", "body", "claim", "receipt", "request", "owner", "call", "part", "incomplete"} {
		t.Run(fault, func(t *testing.T) {
			value, s, i := checkpointPermissionFixture(t)
			switch fault {
			case "pending":
				i.closed = false
			case "canceled":
				i.canceled = true
			case "rejected":
				i.rejected = true
			case "always":
				i.alwaysAccepted = true
			case "policy":
				i.alwaysObservations = []string{"prior"}
			case "pending-absent":
				i.pendingAbsent = true
			case "unsent":
				i.attempt.sent = false
			case "http":
				i.attempt.receipt.HTTPAccepted = false
			case "native":
				i.attempt.receipt.NativeAccepted = false
			case "feedback":
				i.attempt.receipt.FeedbackRequested = true
			case "decision":
				*i.attempt.permission = PermissionAlways
			case "body":
				i.attempt.body = []byte(`{"reply":"always"}`)
			case "claim":
				i.attempt.claim.BodyDigest = mutationDigest([]byte("changed"))
			case "receipt":
				i.attempt.receipt.RequestID = domain.NewID()
			case "request":
				s.observer.input.receipt.RequestID = domain.NewID()
			case "owner":
				i.value.Tool.MessageID = fixtureMessageID
			case "call":
				i.value.Tool.CallID = "changed"
			case "part":
				delete(s.observer.parts, i.attempt.claim.PartID)
			case "incomplete":
				s.observer.parts[i.attempt.claim.PartID].value.Tool.State = ToolRunning
			}
			if s.checkpointToolHistory(value) != nil {
				t.Fatal("partial or foreign approval granted restoration")
			}
		})
	}
}

func TestCheckpointOnceProofIsBoundToOriginalToolAndInput(t *testing.T) {
	for _, fault := range []string{"version", "flag", "empty", "duplicate", "body", "kind", "input", "message", "part", "session", "response-reuse", "arrival", "interaction"} {
		t.Run(fault, func(t *testing.T) {
			value, _, _ := checkpointPermissionFixture(t)
			c := &value.Tools.Once[0]
			switch fault {
			case "version":
				value.Tools.Version = 1
			case "flag":
				value.Tools.InteractionFree = true
			case "empty":
				value.Tools.Once = nil
			case "duplicate":
				value.Tools.Once = append(value.Tools.Once, *c)
			case "body":
				c.BodyDigest = mutationDigest([]byte(`{"reply":"always"}`))
			case "kind":
				c.Kind = ReplyQuestionMutation
			case "input":
				c.InputRequestID = domain.NewID()
			case "message":
				c.MessageID = fixtureMessageID
			case "part":
				c.PartID = fixturePartID
			case "session":
				c.SessionID = "ses_01960dcbe1feABCDEFGHIJKLMN"
			case "response-reuse":
				c.RequestID = c.InputRequestID
			case "arrival":
				c.ArrivalID = c.InteractionID
			case "interaction":
				c.InteractionID = c.ArrivalID
			}
			if validCheckpointTools(value) || checkpointReplacementProfile(value) == nil {
				t.Fatal("altered original permission proof accepted")
			}
		})
	}
	value, s, _ := checkpointPermissionFixture(t)
	s.predecessor = &value
	s.observer = &inputObserver{}
	next := nativeCheckpoint{Project: "global", NativeRoot: fixtureNativeRoot(), Previous: []HistoryObservation{value.History}}
	next.Tools = s.checkpointToolHistory(next)
	if next.Tools == nil || next.Tools.Version != 2 || len(next.Tools.Once) != 1 || checkpointReplacementProfile(next) != nil {
		t.Fatal("later text input lost original permission evidence")
	}
	if s.freshCheckpointInput(value.Tools.Once[0].RequestID, "new-message", "new-part") {
		t.Fatal("original permission request was reused as another input")
	}
	next.Tools.Once[0].CallID = "changed"
	if value.Tools.Once[0].CallID == "changed" {
		t.Fatal("successor changed predecessor proof")
	}
}
