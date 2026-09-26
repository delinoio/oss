package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func checkpointRejectionFixture(t *testing.T, feedback *string, cascade bool) (nativeCheckpoint, *sessionAPI, *observedInteraction) {
	t.Helper()
	r := newReplyFixture(t, PermissionInteraction)
	r.f.o.rejectionPolicy = StopOnInteractionRejection
	ids := []string{r.id}
	if cascade {
		ids = append(ids, addPendingPermission(r))
	}
	*r.response.Decision = PermissionReject
	r.response.Feedback = feedback
	if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err != nil {
		t.Fatal(err)
	}
	if _, err := r.f.o.observe(context.Background(), r.closure()); err != nil {
		t.Fatal(err)
	}
	if cascade {
		r.f.observe(PermissionRepliedEvent, map[string]any{"sessionID": fixtureSessionID, "requestID": ids[1], "reply": PermissionReject})
	}
	message := HistoryMessage{ID: r.f.a["id"].(string)}
	for index, id := range ids {
		i := r.f.o.interactions[id]
		failInteractionTool(r, i.value.Tool.CallID, 100+index)
		partID := r.f.o.calls[i.value.Tool.CallID]
		part := r.f.o.parts[partID]
		message.Parts = append(message.Parts, HistoryPart{ID: partID, Kind: ToolPartKind, Digest: mutationDigest(part.raw)})
	}
	value := nativeCheckpoint{Project: "global", NativeRoot: "/", History: HistoryObservation{RequestID: r.f.o.input.receipt.RequestID, SessionID: fixtureSessionID, Messages: []HistoryMessage{message}}}
	value.Tools = r.api.checkpointToolHistory(value)
	if value.Tools == nil || value.Tools.Version != 9 || value.Tools.InteractionFree || len(value.Tools.Rejections) != 1 || (len(value.Tools.RejectionPolicy) == 1) != cascade || checkpointReplacementProfile(value) != nil || len(r.claims) != 1 || r.posts != 1 {
		t.Fatal("original rejection lost or acquired a second direct reply")
	}
	raw, _ := json.Marshal(value.Tools)
	if bytes.Contains(raw, []byte("private correction")) || bytes.Contains(raw, []byte("private native error")) {
		t.Fatal("private correction/error entered checkpoint metadata")
	}
	return value, r.api, r.f.o.interactions[r.id]
}

func TestCheckpointRejectionsRequireOriginalAcceptanceAndFailedRead(t *testing.T) {
	for _, feedback := range []*string{nil, checkpointCorrection(""), checkpointCorrection("private correction")} {
		for _, cascade := range []bool{false, true} {
			value, s, direct := checkpointRejectionFixture(t, feedback, cascade)
			original, _ := json.Marshal(value)
			s.predecessor, s.observer = &value, &inputObserver{}
			next := nativeCheckpoint{Project: "global", NativeRoot: "/", Previous: []HistoryObservation{copyHistoryObservation(value.History)}}
			next.Tools = s.checkpointToolHistory(next)
			if next.Tools == nil || next.Tools.Version != 9 || checkpointReplacementProfile(next) != nil || s.freshCheckpointInput(direct.attempt.claim.RequestID, "new-message", "new-part") {
				t.Fatal("successor lost rejection or allowed response identity reuse")
			}
			if cascade {
				next.Tools.RejectionPolicy[0].Sources[0] = "changed"
			}
			after, _ := json.Marshal(value)
			if !bytes.Equal(original, after) {
				t.Fatal("successor rewrote original rejection context")
			}
			for version := uint32(1); version <= 8; version++ {
				value.Tools.Version = version
				if validCheckpointTools(value) {
					t.Fatal("legacy proof acquired rejected Read authority", version)
				}
			}
		}
	}
}

func TestCheckpointRejectionCaptureRefusesContradictoryEvidence(t *testing.T) {
	for _, fault := range []string{"http", "native", "feedback", "correction", "body", "claim", "kind", "event", "name", "pending", "canceled", "absent", "accepted", "unclaimed", "error", "output", "metadata", "timing", "provider", "missing", "policy-attempt", "policy-reserved", "policy-event", "policy-source", "policy-canceled", "policy-always"} {
		t.Run(fault, func(t *testing.T) {
			value, s, i := checkpointRejectionFixture(t, checkpointCorrection("private correction"), true)
			part := s.observer.parts[i.attempt.claim.PartID]
			policy := s.observer.interactions[value.Tools.RejectionPolicy[0].InteractionID]
			switch fault {
			case "http":
				i.attempt.receipt.HTTPAccepted = false
			case "native":
				i.attempt.receipt.NativeAccepted = false
			case "feedback":
				i.attempt.receipt.FeedbackRequested = false
			case "correction":
				i.attempt.correction = false
			case "body":
				i.attempt.body = []byte(`{"reply":"once"}`)
			case "claim":
				i.attempt.claim.BodyDigest = mutationDigest(nil)
			case "kind":
				i.attempt.claim.Kind = ReplyQuestionMutation
			case "event":
				i.replyEvent = i.arrival
			case "name":
				i.value.Permission.Name = "unverified_permission"
			case "pending":
				i.closed = false
			case "canceled":
				i.canceled = true
			case "absent":
				i.pendingAbsent = true
			case "accepted":
				i.rejected = false
			case "unclaimed":
				i.attempt = nil
			case "error":
				part.value.Tool.Error = nil
			case "output":
				part.value.Tool.Output = part.value.Tool.Error
			case "metadata":
				part.value.Tool.Metadata = []byte(`{"loaded":["private instructions"]}`)
			case "timing":
				part.value.Tool.Timing.End = nil
			case "provider":
				part.value.Tool.PartMetadata = []byte(`{"providerExecuted":true}`)
			case "missing":
				delete(s.observer.interactions, i.value.ID)
			case "policy-attempt":
				policy.attempt = i.attempt
			case "policy-reserved":
				policy.rejectionReserved = false
			case "policy-event":
				policy.replyEvent = i.replyEvent
			case "policy-source":
				policy.rejectionSources = []string{"per_01960dcbe199ABCDEFGHIJKLMN"}
			case "policy-canceled":
				policy.canceled = true
			case "policy-always":
				policy.alwaysObservations = []string{i.value.ID}
			}
			if s.checkpointToolHistory(value) != nil {
				t.Fatal("contradictory rejection evidence gained restoration")
			}
		})
	}
}

func TestCheckpointRejectionProofRequiresOriginalFailedPartAndClosureOwnership(t *testing.T) {
	for _, fault := range []string{"failed", "direct", "policy", "duplicate", "event", "proposal", "direct-event", "source", "input", "request", "kind", "unknown-part"} {
		t.Run(fault, func(t *testing.T) {
			value, _, _ := checkpointRejectionFixture(t, nil, true)
			p := value.Tools
			switch fault {
			case "failed":
				p.Parts[0].Failed = false
			case "direct":
				p.Rejections = nil
			case "policy":
				p.RejectionPolicy = nil
			case "duplicate":
				p.Rejections = append(p.Rejections, p.Rejections[0])
			case "event":
				p.RejectionPolicy[0].ReplyEventID = p.Rejections[0].ReplyEventID
			case "proposal":
				p.Rejections[0].ReplyEventID = p.Rejections[0].Claim.ArrivalID
			case "direct-event":
				p.RejectionPolicy[0].ArrivalID = p.Rejections[0].ReplyEventID
			case "source":
				p.RejectionPolicy[0].Sources = []string{p.RejectionPolicy[0].InteractionID}
			case "input":
				p.RejectionPolicy[0].InputRequestID = domain.NewID()
			case "request":
				p.Rejections[0].Claim.RequestID = p.Rejections[0].Claim.InputRequestID
			case "kind":
				p.Rejections[0].Claim.Kind = RejectQuestionMutation
			case "unknown-part":
				p.Rejections[0].Claim.PartID = fixturePartID
			}
			if validCheckpointTools(value) {
				t.Fatal("changed rejection proof accepted")
			}
		})
	}
}

func checkpointCorrection(value string) *string { return &value }

func TestCheckpointRejectionHistoryComposesWithLaterQuestionDismissal(t *testing.T) {
	prior, _, _ := checkpointRejectionFixture(t, checkpointCorrection("private correction"), true)
	original, _ := json.Marshal(prior)
	value, s, i := checkpointQuestionDismissalFixture(t)
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
	s.predecessor = &prior
	value.Previous = []HistoryObservation{copyHistoryObservation(prior.History)}
	value.Tools = s.checkpointToolHistory(value)
	if value.Tools == nil || value.Tools.Version != 9 || len(value.Tools.Parts) != 3 || len(value.Tools.Questions) != 1 || len(value.Tools.Rejections) != 1 || len(value.Tools.RejectionPolicy) != 1 || checkpointReplacementProfile(value) != nil {
		t.Fatal("later dismissal lost original rejection/correction history")
	}
	after, _ := json.Marshal(prior)
	if !bytes.Equal(original, after) {
		t.Fatal("later dismissal changed original rejection proof")
	}
}
