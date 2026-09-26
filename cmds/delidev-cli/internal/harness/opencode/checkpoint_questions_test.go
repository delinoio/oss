package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func checkpointQuestionFixture(t *testing.T) (nativeCheckpoint, *sessionAPI, *observedInteraction) {
	t.Helper()
	r := newReplyFixture(t, QuestionInteraction)
	if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err != nil {
		t.Fatal(err)
	}
	if _, err := r.f.o.observe(context.Background(), r.closure()); err != nil {
		t.Fatal(err)
	}
	i := r.f.o.interactions[r.id]
	c := i.attempt.claim
	part := r.f.o.parts[c.PartID]
	part.value.Tool = checkpointInlineToolFixture(checkpointQuestionTool)
	part.value.Tool.CallID = c.CallID
	part.value.Tool.Metadata, _ = json.Marshal(map[string]any{"answers": r.response.Answers, "truncated": false})
	value := nativeCheckpoint{Project: "global", NativeRoot: "/", History: HistoryObservation{RequestID: c.InputRequestID, SessionID: c.SessionID, Messages: []HistoryMessage{{ID: c.MessageID, Parts: []HistoryPart{{ID: c.PartID, Kind: ToolPartKind, Digest: mutationDigest(part.raw)}}}}}}
	value.Tools = r.api.checkpointToolHistory(value)
	if value.Tools == nil || value.Tools.Version != 5 || len(value.Tools.Questions) != 1 || value.Tools.Questions[0].Claim != c || checkpointReplacementProfile(value) != nil {
		t.Fatal("original question acceptance not retained")
	}
	raw, _ := json.Marshal(value.Tools)
	if bytes.Contains(raw, []byte("private-sentinel")) {
		t.Fatal("private answer entered checkpoint metadata")
	}
	return value, r.api, i
}

func TestCheckpointQuestionRequiresMatchingOriginalAnswerAndResult(t *testing.T) {
	for _, fault := range []string{"pending", "canceled", "rejected", "absent", "unclaimed", "unsent", "http", "native", "body", "claim", "receipt", "kind", "input", "owner", "call", "event", "proposal", "metadata", "answers", "null-row", "truncated", "artifact", "unfinished", "always", "permission", "policy", "missing"} {
		t.Run(fault, func(t *testing.T) {
			value, s, i := checkpointQuestionFixture(t)
			part := s.observer.parts[i.attempt.claim.PartID]
			switch fault {
			case "pending":
				i.closed = false
			case "canceled":
				i.canceled = true
			case "rejected":
				i.rejected = true
			case "absent":
				i.pendingAbsent = true
			case "unclaimed":
				i.attempt = nil
			case "unsent":
				i.attempt.sent = false
			case "http":
				i.attempt.receipt.HTTPAccepted = false
			case "native":
				i.attempt.receipt.NativeAccepted = false
			case "body":
				i.attempt.body = []byte(`{"answers":[["changed"]]}`)
			case "claim":
				i.attempt.claim.BodyDigest = mutationDigest([]byte("changed"))
			case "receipt":
				i.attempt.receipt.RequestID = domain.NewID()
			case "kind":
				i.attempt.claim.Kind = RejectQuestionMutation
			case "input":
				s.observer.input.receipt.RequestID = domain.NewID()
			case "owner":
				i.value.Tool.MessageID = fixtureMessageID
			case "call":
				i.value.Tool.CallID = "changed"
			case "event":
				i.replyEvent = ""
			case "proposal":
				i.replyEvent = i.arrival
			case "metadata":
				part.value.Tool.Metadata = []byte(`{"answers":[["private-sentinel"]]}`)
			case "answers":
				part.value.Tool.Metadata = []byte(`{"answers":[["changed"]],"truncated":false}`)
			case "null-row":
				part.value.Tool.Metadata = []byte(`{"answers":[null],"truncated":false}`)
			case "truncated":
				part.value.Tool.Metadata = []byte(`{"answers":[["private-sentinel"]],"truncated":true}`)
			case "artifact":
				part.value.Tool.Metadata = []byte(`{"answers":[["private-sentinel"]],"truncated":false,"outputPath":null}`)
			case "unfinished":
				part.value.Tool.State = ToolRunning
			case "always":
				i.alwaysAccepted = true
			case "permission":
				decision := PermissionOnce
				i.attempt.permission = &decision
			case "policy":
				i.alwaysObservations = []string{fixturePermissionID}
			case "missing":
				delete(s.observer.interactions, i.value.ID)
			}
			if s.checkpointToolHistory(value) != nil {
				t.Fatal("missing or contradictory original answer acquired restoration")
			}
		})
	}
}

func TestCheckpointQuestionProofRetainsDistinctReplyOwnership(t *testing.T) {
	for _, fault := range []string{"version", "empty", "duplicate", "event", "proposal", "input", "owner", "part", "tool", "kind", "request", "unclaimed-tool"} {
		t.Run(fault, func(t *testing.T) {
			value, _, _ := checkpointQuestionFixture(t)
			q := &value.Tools.Questions[0]
			switch fault {
			case "version":
				value.Tools.Version = 4
			case "empty":
				value.Tools.Questions = nil
			case "duplicate":
				value.Tools.Questions = append(value.Tools.Questions, *q)
			case "event":
				q.ReplyEventID = ""
			case "proposal":
				q.ReplyEventID = q.Claim.ArrivalID
			case "input":
				q.Claim.InputRequestID = domain.NewID()
			case "owner":
				q.Claim.MessageID = fixtureMessageID
			case "part":
				q.Claim.PartID = fixturePartID
			case "tool":
				value.Tools.Parts[0].Name = checkpointReadTool
			case "kind":
				q.Claim.Kind = RejectQuestionMutation
			case "request":
				q.Claim.RequestID = q.Claim.InputRequestID
			case "unclaimed-tool":
				value.Tools.Version, value.Tools.InteractionFree, value.Tools.Questions = 1, true, nil
			}
			if validCheckpointTools(value) || checkpointReplacementProfile(value) == nil {
				t.Fatal("contradictory question proof accepted")
			}
		})
	}
	value, s, _ := checkpointQuestionFixture(t)
	s.predecessor, s.observer = &value, &inputObserver{}
	next := nativeCheckpoint{Project: "global", NativeRoot: "/", Previous: []HistoryObservation{value.History}}
	next.Tools = s.checkpointToolHistory(next)
	if next.Tools == nil || next.Tools.Version != 5 || checkpointReplacementProfile(next) != nil || s.freshCheckpointInput(value.Tools.Questions[0].Claim.RequestID, "new-message", "new-part") {
		t.Fatal("successor lost question ownership or permitted request reuse")
	}
	next.Tools.Questions[0].Claim.CallID = "changed"
	if value.Tools.Questions[0].Claim.CallID == "changed" {
		t.Fatal("successor changed original question proof")
	}
}

func TestCheckpointQuestionPreservesEarlierReadPolicyLineage(t *testing.T) {
	prior, _, _ := checkpointPolicyFixture(t)
	value, s, i := checkpointQuestionFixture(t)
	// Give this independently observed successor distinct original native IDs.
	old := i.attempt.claim
	c := &i.attempt.claim
	c.MessageID, c.PartID, c.CallID = "msg_01960dcbe299ABCDEFGHIJKLMN", "prt_01960dcbe299ABCDEFGHIJKLMN", "call_question_successor"
	c.ArrivalID, i.replyEvent = "evt_01960dcbe298ABCDEFGHIJKLMN", "evt_01960dcbe299ABCDEFGHIJKLMN"
	i.arrival, i.attempt.receipt.ArrivalID = c.ArrivalID, c.ArrivalID
	i.value.Tool.MessageID, i.value.Tool.CallID = c.MessageID, c.CallID
	part := s.observer.parts[old.PartID]
	delete(s.observer.parts, old.PartID)
	part.value.MessageID, part.value.Tool.CallID = c.MessageID, c.CallID
	s.observer.parts[c.PartID], s.observer.calls[c.CallID] = part, c.PartID
	value.History.Messages[0].ID, value.History.Messages[0].Parts[0].ID = c.MessageID, c.PartID
	s.creation.settings.Permission = []PermissionRule{}
	s.predecessor, s.restoredAlways = &prior, 1
	s.sessionPermissions = checkpointAppliedPermissions(prior.Tools, 1)
	value.Previous = []HistoryObservation{prior.History}
	value.Tools = s.checkpointToolHistory(value)
	if value.Tools == nil || value.Tools.Version != 5 || len(value.Tools.Parts) != 3 || len(value.Tools.Questions) != 1 || len(value.Tools.Policy) != 1 || len(value.Tools.Always) != 1 || value.Tools.AppliedAlways != 1 || checkpointReplacementProfile(value) != nil {
		t.Fatal("question successor lost original automatic Read policy evidence")
	}
	if prior.Tools.Version != 4 || len(prior.Tools.Questions) != 0 || prior.Tools.AppliedAlways != 0 {
		t.Fatal("question successor rewrote original Read checkpoint")
	}
}
