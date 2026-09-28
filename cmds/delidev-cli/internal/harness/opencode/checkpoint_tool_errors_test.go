package opencode

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func setCheckpointReadError(value *nativeCheckpoint, s *sessionAPI, id string) {
	part := s.observer.parts[id]
	part.value.Tool.State, part.value.Tool.Output = ToolError, nil
	part.value.Tool.Error = checkpointCorrection("private original missing-file error")
	part.value.Tool.Metadata = nil
	part.raw, _ = json.Marshal(part.value)
	for m := range value.History.Messages {
		for p := range value.History.Messages[m].Parts {
			if value.History.Messages[m].Parts[p].ID == id {
				value.History.Messages[m].Parts[p].Digest = mutationDigest(part.raw)
			}
		}
	}
}

func TestCheckpointOrdinaryReadErrorRequiresOriginalClosedState(t *testing.T) {
	for _, fault := range []string{"valid", "running", "timing", "compacted", "metadata", "loaded", "output", "error", "provider", "attachment", "raw", "missing", "interaction", "name", "stop"} {
		t.Run(fault, func(t *testing.T) {
			value, s := checkpointToolsFixture()
			id := value.History.Messages[0].Parts[0].ID
			setCheckpointReadError(&value, s, id)
			part := s.observer.parts[id]
			tool := part.value.Tool
			switch fault {
			case "running":
				tool.State = ToolRunning
			case "timing":
				tool.Timing.End = nil
			case "compacted":
				tool.Timing.Compacted = tool.Timing.End
			case "metadata":
				tool.Metadata = json.RawMessage(`{"outputPath":"/private/output"}`)
			case "loaded":
				tool.Metadata = json.RawMessage(`{"loaded":["/private/AGENTS.md"]}`)
			case "output":
				tool.Output = checkpointCorrection("unexpected result")
			case "error":
				tool.Error = nil
			case "provider":
				tool.PartMetadata = json.RawMessage(`{"providerExecuted":true}`)
			case "attachment":
				tool.Attachments = []NativePart{{}}
			case "raw":
				part.raw = []byte("changed")
			case "missing":
				delete(s.observer.parts, id)
			case "interaction":
				s.observer.interactions = map[string]*observedInteraction{"original": {}}
			case "name":
				tool.Name = "bash"
			case "stop":
				value.Stop = &StopReceipt{}
			}
			value.Tools = s.checkpointToolHistory(value)
			if (value.Tools != nil) != (fault == "valid") {
				t.Fatal("ordinary Read error eligibility changed")
			}
			if fault != "valid" {
				return
			}
			if value.Tools.Version != 12 || !value.Tools.InteractionFree || value.Tools.Parts[0].Failed || value.Tools.Parts[0].ErrorProfile != checkpointReadError || checkpointReplacementProfile(value) != nil {
				t.Fatal("ordinary Read error lost original identity")
			}
			raw, _ := json.Marshal(value.Tools)
			if bytes.Contains(raw, []byte("private original")) {
				t.Fatal("error text entered checkpoint metadata")
			}
			for version := uint32(1); version <= 11; version++ {
				value.Tools.Version = version
				if validCheckpointTools(value) {
					t.Fatal("old proof acquired ordinary error authority", version)
				}
			}
		})
	}
}

func TestCheckpointOrdinaryReadErrorPreservesOriginalApprovalsAndImmutableLineage(t *testing.T) {
	for _, decision := range []PermissionDecision{PermissionOnce, PermissionAlways} {
		value, s, i := checkpointPermissionFixture(t, decision)
		setCheckpointReadError(&value, s, i.attempt.claim.PartID)
		value.Tools = s.checkpointToolHistory(value)
		if value.Tools == nil || value.Tools.Version != 12 || value.Tools.InteractionFree || checkpointReplacementProfile(value) != nil {
			t.Fatal("Read error lost independent accepted approval", decision)
		}
		original, _ := json.Marshal(value)
		s.predecessor, s.observer = &value, &inputObserver{}
		if decision == PermissionAlways {
			s.restoredAlways = uint32(len(value.Tools.Always))
			s.sessionPermissions = checkpointAppliedPermissions(value.Tools, s.restoredAlways)
		}
		next := nativeCheckpoint{Project: "global", NativeRoot: fixtureNativeRoot(), Previous: []HistoryObservation{copyHistoryObservation(value.History)}}
		next.Tools = s.checkpointToolHistory(next)
		if next.Tools == nil || next.Tools.Version != 12 || checkpointReplacementProfile(next) != nil || s.freshCheckpointInput(i.attempt.claim.RequestID, "new-message", "new-part") {
			t.Fatal("error successor lost original approval or reused response")
		}
		for _, fault := range []string{"empty", "profile", "name", "rejected", "instructions"} {
			part := next.Tools.Parts[0]
			switch fault {
			case "empty":
				part.ErrorProfile = ""
			case "profile":
				part.ErrorProfile = "future"
			case "name":
				part.Name = checkpointShellTool
			case "rejected":
				part.Failed = true
			case "instructions":
				part.InstructionsLoaded = true
			}
			prior := next.Tools.Parts[0]
			next.Tools.Parts[0] = part
			if validCheckpointTools(next) {
				t.Fatal("contradictory error profile accepted", fault)
			}
			next.Tools.Parts[0] = prior
		}
		next.Tools.Parts[0].Digest = "changed"
		after, _ := json.Marshal(value)
		if !bytes.Equal(original, after) {
			t.Fatal("successor changed original Read error")
		}
	}
}

func TestCheckpointOrdinaryReadErrorCannotReplaceRejectionOrMissingAcceptance(t *testing.T) {
	value, s, direct := checkpointRejectionFixture(t, nil, true)
	value.Tools = s.checkpointToolHistory(value)
	if value.Tools == nil || value.Tools.Version != 9 || checkpointHasReadError(value.Tools) {
		t.Fatal("original rejection became ordinary failure")
	}
	direct.attempt = nil
	if s.checkpointToolHistory(value) != nil {
		t.Fatal("missing original reject claim became ordinary failure")
	}
	for _, fault := range []string{"http", "native", "canceled", "pending"} {
		value, s, i := checkpointPermissionFixture(t)
		setCheckpointReadError(&value, s, i.attempt.claim.PartID)
		switch fault {
		case "http":
			i.attempt.receipt.HTTPAccepted = false
		case "native":
			i.attempt.receipt.NativeAccepted = false
		case "canceled":
			i.canceled = true
		case "pending":
			i.closed = false
		}
		if s.checkpointToolHistory(value) != nil {
			t.Fatal("Read error bypassed original approval evidence", fault)
		}
	}
}

func TestCheckpointOrdinaryReadErrorRetainsAutomaticAllowanceClosure(t *testing.T) {
	value, s, _ := checkpointPolicyFixture(t)
	id := value.Tools.Policy[0].PartID
	setCheckpointReadError(&value, s, id)
	value.Tools = s.checkpointToolHistory(value)
	if value.Tools == nil || value.Tools.Version != 12 || len(value.Tools.Always) != 1 || len(value.Tools.Policy) != 1 || checkpointReplacementProfile(value) != nil {
		t.Fatal("automatic allowance lost when its original Read failed")
	}
}

func TestCheckpointOrdinaryReadErrorPreservesEarlierProfilesAndLaterTextStop(t *testing.T) {
	for _, profile := range []string{"instructions", "named-rejection"} {
		t.Run(profile, func(t *testing.T) {
			prior, previous := checkpointToolsFixture()
			if profile == "instructions" {
				previous.observer.parts[prior.History.Messages[0].Parts[0].ID].value.Tool.Metadata = checkpointInstructionMetadata()
			} else {
				var direct *observedInteraction
				prior, previous, direct = checkpointRejectionFixture(t, checkpointCorrection("private correction"), true)
				replaceCheckpointRejectedTool(&prior, previous, direct, checkpointWriteTool, checkpointRejectEdit)
			}
			if prior.History.RequestID == "" {
				prior.History.RequestID = domain.NewID()
			}
			prior.Tools = previous.checkpointToolHistory(prior)
			if prior.Tools == nil {
				t.Fatal("missing original fixture proof")
			}
			original, _ := json.Marshal(prior)
			value, s := checkpointToolsFixture()
			value.History.RequestID = domain.NewID()
			value.History.Messages[0].Parts = value.History.Messages[0].Parts[:1]
			part := &value.History.Messages[0].Parts[0]
			s.observer.parts["new-read-error"] = s.observer.parts[part.ID]
			delete(s.observer.parts, part.ID)
			part.ID = "new-read-error"
			setCheckpointReadError(&value, s, part.ID)
			s.predecessor = &prior
			value.Previous = []HistoryObservation{copyHistoryObservation(prior.History)}
			value.Tools = s.checkpointToolHistory(value)
			if value.Tools == nil || value.Tools.Version != 12 || checkpointReplacementProfile(value) != nil || len(value.Tools.Parts) != len(prior.Tools.Parts)+1 {
				t.Fatal("ordinary error lost earlier original tool profile")
			}
			before, _ := json.Marshal(prior.Tools.Parts)
			after, _ := json.Marshal(value.Tools.Parts[:len(prior.Tools.Parts)])
			if !bytes.Equal(before, after) {
				t.Fatal("ordinary error rewrote earlier tool proof")
			}
			s.predecessor, s.observer = &value, &inputObserver{}
			next := nativeCheckpoint{Project: "global", NativeRoot: fixtureNativeRoot(), History: HistoryObservation{RequestID: domain.NewID()}, Previous: append(append([]HistoryObservation(nil), value.Previous...), copyHistoryObservation(value.History)), Stop: &StopReceipt{}}
			next.Tools = s.checkpointToolHistory(next)
			if next.Tools == nil || next.Tools.Version != 12 || checkpointReplacementProfile(next) != nil {
				t.Fatal("later text Stop invalidated earlier ended Read error")
			}
			if profile == "named-rejection" {
				next.Tools.RejectionPolicy[0].Sources[0] = "changed"
			} else {
				next.Tools.Parts[0].InstructionsLoaded = false
			}
			after, _ = json.Marshal(prior)
			if !bytes.Equal(original, after) {
				t.Fatal("later lineage mutated original profile")
			}
		})
	}
}
