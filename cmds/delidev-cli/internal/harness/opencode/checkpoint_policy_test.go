package opencode

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func checkpointPolicyFixture(t *testing.T) (nativeCheckpoint, *sessionAPI, *observedInteraction) {
	t.Helper()
	r := newReplyFixture(t, PermissionInteraction)
	second := addPendingPermission(r)
	*r.response.Decision = PermissionAlways
	r.api.creation.settings.Permission = []PermissionRule{}
	if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err != nil {
		t.Fatal(err)
	}
	if _, err := r.f.o.observe(context.Background(), r.closure()); err != nil {
		t.Fatal(err)
	}
	r.f.observe(PermissionRepliedEvent, map[string]any{"sessionID": fixtureSessionID, "requestID": second, "reply": PermissionAlways})
	o := r.f.o
	message := HistoryMessage{ID: r.f.a["id"].(string)}
	for _, id := range []string{r.id, second} {
		i := o.interactions[id]
		partID := o.calls[i.value.Tool.CallID]
		part := o.parts[partID]
		part.value.Tool = checkpointInlineToolFixture(checkpointReadTool)
		part.value.Tool.CallID = i.value.Tool.CallID
		message.Parts = append(message.Parts, HistoryPart{ID: partID, Kind: ToolPartKind, Digest: mutationDigest(part.raw)})
	}
	value := nativeCheckpoint{Project: "global", NativeRoot: "/", History: HistoryObservation{RequestID: o.input.receipt.RequestID, SessionID: fixtureSessionID, Messages: []HistoryMessage{message}}}
	value.Tools = r.api.checkpointToolHistory(value)
	if value.Tools == nil || value.Tools.Version != 4 || len(value.Tools.Policy) != 1 || len(value.Tools.Always) != 1 || len(value.Tools.Once) != 0 || checkpointReplacementProfile(value) != nil || len(r.claims) != 1 || r.posts != 1 {
		t.Fatal("original policy closure lost or acquired a direct response")
	}
	return value, r.api, o.interactions[second]
}

func TestCheckpointPolicyCaptureRequiresOriginalReadClosure(t *testing.T) {
	for _, fault := range []string{"pending", "attempt", "always", "rejected", "canceled", "missing", "reserved", "rejection", "name", "event", "proposal", "sources", "source-http", "source-native", "source-event", "source-unknown", "source-duplicate", "owner", "call", "incomplete", "shell"} {
		t.Run(fault, func(t *testing.T) {
			value, s, i := checkpointPolicyFixture(t)
			prior := s.observer.interactions[i.alwaysObservations[0]]
			part := s.observer.parts[s.observer.calls[i.value.Tool.CallID]]
			switch fault {
			case "pending":
				i.closed = false
			case "attempt":
				i.attempt = prior.attempt
			case "always":
				i.alwaysAccepted = true
			case "rejected":
				i.rejected = true
			case "canceled":
				i.canceled = true
			case "missing":
				i.pendingAbsent = true
			case "reserved":
				i.rejectionReserved = true
			case "rejection":
				i.rejectionSources = []string{prior.value.ID}
			case "name":
				i.value.Permission.Name = "external_directory"
			case "event":
				i.replyEvent = ""
			case "proposal":
				i.arrival = i.replyEvent
			case "sources":
				i.alwaysObservations = nil
			case "source-http":
				prior.attempt.receipt.HTTPAccepted = false
			case "source-native":
				prior.attempt.receipt.NativeAccepted = false
			case "source-event":
				prior.replyEvent = i.replyEvent
			case "source-unknown":
				i.alwaysObservations[0] = "per_01960dcbe199ABCDEFGHIJKLMN"
			case "source-duplicate":
				i.alwaysObservations = append(i.alwaysObservations, i.alwaysObservations[0])
			case "owner":
				i.value.Tool.MessageID = fixtureMessageID
			case "call":
				i.value.Tool.CallID = "missing"
			case "incomplete":
				part.value.Tool.State = ToolRunning
			case "shell":
				part.value.Tool.Name = "bash"
			}
			if s.checkpointToolHistory(value) != nil {
				t.Fatal("incomplete or contradictory native policy acquired restoration")
			}
		})
	}
}

func TestCheckpointPolicyProofBindsOriginalLineage(t *testing.T) {
	for _, fault := range []string{"version", "empty", "direct-empty", "duplicate", "event", "proposal", "direct-proposal", "direct-id", "input", "owner", "part", "shell", "sources", "source-self", "source-duplicate", "source-foreign"} {
		t.Run(fault, func(t *testing.T) {
			value, _, _ := checkpointPolicyFixture(t)
			p := &value.Tools.Policy[0]
			switch fault {
			case "version":
				value.Tools.Version = 3
			case "empty":
				value.Tools.Policy = nil
			case "direct-empty":
				value.Tools.Always = nil
			case "duplicate":
				value.Tools.Policy = append(value.Tools.Policy, *p)
			case "event":
				p.ReplyEventID = ""
			case "proposal":
				p.ArrivalID = p.ReplyEventID
			case "direct-proposal":
				p.ReplyEventID = value.Tools.Always[0].Claim.ArrivalID
			case "direct-id":
				p.InteractionID = value.Tools.Always[0].Claim.InteractionID
			case "input":
				p.InputRequestID = domain.NewID()
			case "owner":
				p.MessageID = fixtureMessageID
			case "part":
				p.PartID = fixturePartID
			case "shell":
				value.Tools.Parts[1].Name = checkpointShellTool
			case "sources":
				p.Sources = nil
			case "source-self":
				p.Sources[0] = p.InteractionID
			case "source-duplicate":
				p.Sources = append(p.Sources, p.Sources[0])
			case "source-foreign":
				p.Sources[0] = "per_01960dcbe199ABCDEFGHIJKLMN"
			}
			if validCheckpointTools(value) || checkpointReplacementProfile(value) == nil {
				t.Fatal("changed native policy proof accepted")
			}
		})
	}
	value, s, i := checkpointPolicyFixture(t)
	s.predecessor, s.observer = &value, &inputObserver{}
	s.restoredAlways = 1
	s.sessionPermissions = checkpointAppliedPermissions(value.Tools, 1)
	next := nativeCheckpoint{Project: "global", NativeRoot: "/", Previous: []HistoryObservation{value.History}}
	next.Tools = s.checkpointToolHistory(next)
	if next.Tools == nil || next.Tools.Version != 4 || next.Tools.AppliedAlways != 1 || checkpointReplacementProfile(next) != nil {
		t.Fatal("text successor lost original policy history")
	}
	next.Tools.Policy[0].Sources[0] = "changed"
	if value.Tools.Policy[0].Sources[0] != i.alwaysObservations[0] {
		t.Fatal("successor changed original policy source context")
	}
	value.Tools.Policy[0].Sources[0] = "changed"
	if i.alwaysObservations[0] == "changed" {
		t.Fatal("checkpoint changed original native observation")
	}
}
