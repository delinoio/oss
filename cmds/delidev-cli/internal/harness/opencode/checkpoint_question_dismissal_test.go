package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func checkpointQuestionDismissalFixture(t *testing.T) (nativeCheckpoint, *sessionAPI, *observedInteraction) {
	t.Helper()
	r := newReplyFixture(t, QuestionInteraction)
	r.f.o.rejectionPolicy = StopOnInteractionRejection
	r.response = InteractionResponse{Reject: true}
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
	part.value.Tool.State, part.value.Tool.Output, part.value.Tool.Metadata = ToolError, nil, nil
	problem := "private original native dismissal"
	part.value.Tool.Error = &problem
	value := nativeCheckpoint{Project: "global", NativeRoot: "/", History: HistoryObservation{RequestID: c.InputRequestID, SessionID: c.SessionID, Messages: []HistoryMessage{{ID: c.MessageID, Parts: []HistoryPart{{ID: c.PartID, Kind: ToolPartKind, Digest: mutationDigest(part.raw)}}}}}}
	value.Tools = r.api.checkpointToolHistory(value)
	if value.Tools == nil || value.Tools.Version != 8 || value.Tools.InteractionFree || len(value.Tools.Questions) != 1 || value.Tools.Questions[0].Claim != c || c.Kind != RejectQuestionMutation || checkpointReplacementProfile(value) != nil {
		t.Fatal("original Question dismissal not retained")
	}
	raw, _ := json.Marshal(value.Tools)
	if bytes.Contains(raw, []byte(problem)) || bytes.Contains(raw, []byte("private-sentinel")) {
		t.Fatal("private Question content entered checkpoint metadata")
	}
	return value, r.api, i
}

func TestCheckpointQuestionDismissalRequiresOriginalAcceptedClosure(t *testing.T) {
	for _, fault := range []string{"pending", "canceled", "unclaimed", "not-rejected", "unsent", "http", "native", "body", "claim", "receipt", "kind", "event", "call", "absent", "answer", "artifact", "interrupted", "error", "output", "timing", "provider", "attachments", "unknown", "missing"} {
		t.Run(fault, func(t *testing.T) {
			value, s, i := checkpointQuestionDismissalFixture(t)
			tool := s.observer.parts[i.attempt.claim.PartID].value.Tool
			switch fault {
			case "pending":
				i.closed = false
			case "canceled":
				i.canceled = true
			case "unclaimed":
				i.attempt = nil
			case "not-rejected":
				i.rejected = false
			case "unsent":
				i.attempt.sent = false
			case "http":
				i.attempt.receipt.HTTPAccepted = false
			case "native":
				i.attempt.receipt.NativeAccepted = false
			case "body":
				i.attempt.body = []byte(`{}`)
			case "claim":
				i.attempt.claim.BodyDigest = mutationDigest([]byte(`{}`))
			case "receipt":
				i.attempt.receipt.RequestID = domain.NewID()
			case "kind":
				i.attempt.claim.Kind = ReplyQuestionMutation
			case "event":
				i.replyEvent = i.arrival
			case "call":
				tool.CallID = "changed"
			case "absent":
				i.pendingAbsent = true
			case "answer":
				tool.Metadata = []byte(`{"answers":[]}`)
			case "artifact":
				tool.Metadata = []byte(`{"outputPath":"/private/result"}`)
			case "interrupted":
				tool.Metadata = []byte(`{"interrupted":true}`)
			case "error":
				tool.Error = nil
			case "output":
				tool.Output = tool.Error
			case "timing":
				tool.Timing.End = nil
			case "provider":
				tool.PartMetadata = []byte(`{"providerExecuted":true}`)
			case "attachments":
				tool.Attachments = []NativePart{{}}
			case "unknown":
				tool.Name = "future"
			case "missing":
				delete(s.observer.interactions, i.value.ID)
			}
			if s.checkpointToolHistory(value) != nil {
				t.Fatal("missing or contradictory dismissal acquired restoration")
			}
		})
	}
}

func TestCheckpointQuestionDismissalPreservesLineageWithoutLegacyPromotion(t *testing.T) {
	value, s, i := checkpointQuestionDismissalFixture(t)
	original, _ := json.Marshal(value)
	s.predecessor, s.observer = &value, &inputObserver{}
	next := nativeCheckpoint{Project: "global", NativeRoot: "/", Previous: []HistoryObservation{copyHistoryObservation(value.History)}}
	next.Tools = s.checkpointToolHistory(next)
	if next.Tools == nil || next.Tools.Version != 8 || checkpointReplacementProfile(next) != nil || s.freshCheckpointInput(i.attempt.claim.RequestID, "new-message", "new-part") {
		t.Fatal("successor lost dismissal ownership or allowed response request reuse")
	}
	next.Tools.Questions[0].Claim.CallID = "changed"
	after, _ := json.Marshal(value)
	if !bytes.Equal(original, after) {
		t.Fatal("successor mutated original dismissal")
	}
	for version := uint32(1); version <= 7; version++ {
		value.Tools.Version = version
		if validCheckpointTools(value) {
			t.Fatal("legacy proof acquired dismissal authority", version)
		}
	}
	value.Tools.Version = 8
	value.Tools.Questions[0].Claim.Kind = ReplyQuestionMutation
	if validCheckpointTools(value) {
		t.Fatal("dismissal became an answer")
	}
}

func TestCheckpointQuestionDismissalKeepsPriorFileHistory(t *testing.T) {
	prior, priorSession := checkpointToolsFixture()
	part := prior.History.Messages[0].Parts[0]
	priorSession.observer.parts[part.ID].value.Tool = checkpointInlineToolFixture(checkpointApplyPatchTool)
	prior.Tools = priorSession.checkpointToolHistory(prior)
	if prior.Tools == nil || prior.Tools.Version != 7 {
		t.Fatal("original file checkpoint missing")
	}
	original, _ := json.Marshal(prior)
	value, s, _ := checkpointQuestionDismissalFixture(t)
	s.predecessor = &prior
	value.Previous = []HistoryObservation{copyHistoryObservation(prior.History)}
	value.Tools = s.checkpointToolHistory(value)
	if value.Tools == nil || value.Tools.Version != 8 || len(value.Tools.Parts) != 3 || checkpointReplacementProfile(value) != nil {
		t.Fatal("dismissal lost original file history")
	}
	after, _ := json.Marshal(prior)
	if !bytes.Equal(original, after) {
		t.Fatal("dismissal changed original file proof")
	}
}
