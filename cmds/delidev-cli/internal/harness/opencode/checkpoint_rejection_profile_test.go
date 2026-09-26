package opencode

import (
	"bytes"
	"encoding/json"
	"testing"
)

func replaceCheckpointRejectedTool(value *nativeCheckpoint, s *sessionAPI, interaction *observedInteraction, name checkpointToolName, permission checkpointRejectionPermission) {
	interaction.value.Permission.Name = string(permission)
	id := s.observer.calls[interaction.value.Tool.CallID]
	part := s.observer.parts[id]
	part.value.Tool.Name = string(name)
	part.raw, _ = json.Marshal(part.value)
	for m := range value.History.Messages {
		for p := range value.History.Messages[m].Parts {
			if value.History.Messages[m].Parts[p].ID == id {
				value.History.Messages[m].Parts[p].Digest = mutationDigest(part.raw)
			}
		}
	}
}

func TestCheckpointNamedRejectionsPreserveCrossToolOriginalClosures(t *testing.T) {
	pairs := []struct {
		name       checkpointToolName
		permission checkpointRejectionPermission
	}{
		{checkpointReadTool, checkpointRejectExternal},
		{checkpointShellTool, checkpointRejectShell},
		{checkpointShellTool, checkpointRejectExternal},
		{checkpointGlobTool, checkpointRejectGlob},
		{checkpointGlobTool, checkpointRejectExternal},
		{checkpointGrepTool, checkpointRejectGrep},
		{checkpointGrepTool, checkpointRejectExternal},
		{checkpointWriteTool, checkpointRejectEdit},
		{checkpointWriteTool, checkpointRejectExternal},
		{checkpointEditTool, checkpointRejectEdit},
		{checkpointEditTool, checkpointRejectExternal},
		{checkpointApplyPatchTool, checkpointRejectEdit},
		{checkpointApplyPatchTool, checkpointRejectExternal},
	}
	for index, pair := range pairs {
		t.Run(string(pair.name)+"/"+string(pair.permission), func(t *testing.T) {
			value, s, direct := checkpointRejectionFixture(t, checkpointCorrection("private correction"), true)
			policy := s.observer.interactions[value.Tools.RejectionPolicy[0].InteractionID]
			nextPair := pairs[(index+1)%len(pairs)]
			replaceCheckpointRejectedTool(&value, s, direct, pair.name, pair.permission)
			replaceCheckpointRejectedTool(&value, s, policy, nextPair.name, nextPair.permission)
			value.Tools = s.checkpointToolHistory(value)
			if value.Tools == nil || value.Tools.Version != 11 || value.Tools.Rejections[0].Permission != pair.permission || value.Tools.RejectionPolicy[0].Permission != nextPair.permission || checkpointReplacementProfile(value) != nil {
				t.Fatal("original named direct/automatic rejection lost")
			}
			original, _ := json.Marshal(value)
			s.predecessor, s.observer = &value, &inputObserver{}
			next := nativeCheckpoint{Project: "global", NativeRoot: "/", Previous: []HistoryObservation{copyHistoryObservation(value.History)}}
			next.Tools = s.checkpointToolHistory(next)
			if next.Tools == nil || next.Tools.Version != 11 || checkpointReplacementProfile(next) != nil || s.freshCheckpointInput(direct.attempt.claim.RequestID, "new-message", "new-part") {
				t.Fatal("successor lost original named rejection or reused response identity")
			}
			next.Tools.RejectionPolicy[0].Sources[0] = "changed"
			after, _ := json.Marshal(value)
			if !bytes.Equal(original, after) || bytes.Contains(original, []byte("private correction")) {
				t.Fatal("successor changed original evidence or retained correction text")
			}
			for version := uint32(1); version <= 10; version++ {
				value.Tools.Version = version
				if validCheckpointTools(value) {
					t.Fatal("older proof gained named rejection authority", version)
				}
			}
		})
	}
}

func TestCheckpointNamedRejectionRefusesWrongPermissionAndAuxiliaryState(t *testing.T) {
	for _, fault := range []string{"pair", "unknown", "read-alias", "missing", "policy-pair", "policy-unknown", "failed", "unclaimed", "output", "metadata", "loaded", "attachment", "compacted", "provider", "timing", "question", "todo"} {
		t.Run(fault, func(t *testing.T) {
			value, s, direct := checkpointRejectionFixture(t, nil, true)
			policy := s.observer.interactions[value.Tools.RejectionPolicy[0].InteractionID]
			replaceCheckpointRejectedTool(&value, s, direct, checkpointWriteTool, checkpointRejectEdit)
			replaceCheckpointRejectedTool(&value, s, policy, checkpointShellTool, checkpointRejectExternal)
			value.Tools = s.checkpointToolHistory(value)
			if value.Tools == nil {
				t.Fatal("missing original fixture")
			}
			part := s.observer.parts[direct.attempt.claim.PartID].value.Tool
			switch fault {
			case "pair":
				direct.value.Permission.Name = "bash"
			case "unknown":
				direct.value.Permission.Name = "future"
			case "read-alias":
				value.Tools.Rejections[0].Permission = checkpointRejectRead
			case "missing":
				value.Tools.Rejections[0].Permission = ""
			case "policy-pair":
				policy.value.Permission.Name = "edit"
			case "policy-unknown":
				policy.value.Permission.Name = "future"
			case "failed":
				value.Tools.Parts[0].Failed = false
			case "unclaimed":
				direct.attempt = nil
			case "output":
				part.Output = checkpointCorrection("unexpected output")
			case "metadata":
				part.Metadata = json.RawMessage(`{"outputPath":"/private/file"}`)
			case "loaded":
				value.Tools.Parts[0].InstructionsLoaded = true
			case "attachment":
				part.Attachments = []NativePart{{}}
			case "compacted":
				part.Timing.Compacted = part.Timing.End
			case "provider":
				part.PartMetadata = json.RawMessage(`{"providerExecuted":true}`)
			case "timing":
				part.Timing.End = nil
			case "question":
				part.Name = "question"
			case "todo":
				part.Name = "todowrite"
			}
			if fault == "read-alias" || fault == "missing" || fault == "failed" || fault == "loaded" {
				if validCheckpointTools(value) {
					t.Fatal("contradictory named rejection proof accepted")
				}
			} else if s.checkpointToolHistory(value) != nil {
				t.Fatal("unsupported named rejection captured")
			}
		})
	}
}

func TestCheckpointNamedRejectionComposesWithLoadedRead(t *testing.T) {
	prior, previous := checkpointToolsFixture()
	previous.observer.parts[prior.History.Messages[0].Parts[0].ID].value.Tool.Metadata = checkpointInstructionMetadata()
	prior.Tools = previous.checkpointToolHistory(prior)
	original, _ := json.Marshal(prior)
	value, s, direct := checkpointRejectionFixture(t, nil, false)
	replaceCheckpointRejectedTool(&value, s, direct, checkpointReadTool, checkpointRejectExternal)
	s.predecessor = &prior
	value.Previous = []HistoryObservation{copyHistoryObservation(prior.History)}
	value.Tools = s.checkpointToolHistory(value)
	if value.Tools == nil || value.Tools.Version != 11 || !value.Tools.Parts[0].InstructionsLoaded || checkpointReplacementProfile(value) != nil {
		t.Fatal("named rejection lost original loaded-instruction history")
	}
	after, _ := json.Marshal(prior)
	if !bytes.Equal(original, after) {
		t.Fatal("named rejection changed original loaded Read proof")
	}
}
