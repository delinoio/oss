package opencode

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func checkpointInstructionMetadata() json.RawMessage {
	return json.RawMessage(`{"preview":"original file","truncated":false,"loaded":["/private/nested/AGENTS.md","/private/AGENTS.md"],"display":{"type":"file","path":"/private/nested/file","text":"original file","lineStart":1,"lineEnd":1,"totalLines":1,"truncated":false}}`)
}

func TestCheckpointLoadedInstructionsRequireOriginalCompleteRead(t *testing.T) {
	for _, fault := range []string{"valid", "running", "error", "output", "timing", "compacted", "attachment", "provider", "truncated", "interrupted", "artifact", "null", "missing", "count", "path", "digest", "missing-part"} {
		t.Run(fault, func(t *testing.T) {
			value, s := checkpointToolsFixture()
			part := &value.History.Messages[0].Parts[0]
			observed := s.observer.parts[part.ID]
			tool := observed.value.Tool
			tool.Metadata = checkpointInstructionMetadata()
			var fields map[string]any
			_ = json.Unmarshal(tool.Metadata, &fields)
			switch fault {
			case "running":
				tool.State = ToolRunning
			case "error":
				tool.State = ToolError
			case "output":
				tool.Output = nil
			case "timing":
				tool.Timing.End = nil
			case "compacted":
				tool.Timing.Compacted = tool.Timing.End
			case "attachment":
				tool.Attachments = []NativePart{{}}
			case "provider":
				tool.PartMetadata = json.RawMessage(`{"providerExecuted":true}`)
			case "truncated":
				fields["truncated"] = true
			case "interrupted":
				fields["interrupted"] = true
			case "artifact":
				fields["outputPath"] = "/private/result"
			case "null":
				fields["loaded"] = nil
			case "missing":
				delete(fields, "loaded")
			case "count":
				fields["loaded"] = make([]string, 1025)
			case "path":
				fields["loaded"] = []string{strings.Repeat("x", 32769)}
			}
			tool.Metadata, _ = json.Marshal(fields)
			observed.raw, _ = json.Marshal(observed.value)
			part.Digest = mutationDigest(observed.raw)
			before := bytes.Clone(observed.raw)
			if fault == "digest" {
				part.Digest = mutationDigest([]byte("changed"))
			}
			if fault == "missing-part" {
				delete(s.observer.parts, part.ID)
			}
			value.Tools = s.checkpointToolHistory(value)
			if (value.Tools != nil) != (fault == "valid") {
				t.Fatal("unsupported or missing original Read gained instruction restoration")
			}
			if fault == "valid" {
				if value.Tools.Version != 10 || !value.Tools.Parts[0].InstructionsLoaded || value.Tools.Parts[1].InstructionsLoaded || checkpointReplacementProfile(value) != nil || !bytes.Equal(before, observed.raw) {
					t.Fatal("original loaded Read evidence changed or was lost")
				}
				for version := uint32(1); version <= 9; version++ {
					value.Tools.Version = version
					if validCheckpointTools(value) {
						t.Fatal("legacy proof acquired instruction restoration", version)
					}
				}
			}
		})
	}
}

func TestCheckpointLoadedInstructionsPreserveEarlierPermissionEvidence(t *testing.T) {
	for _, decision := range []PermissionDecision{PermissionOnce, PermissionAlways} {
		value, s, interaction := checkpointPermissionFixture(t, decision)
		s.observer.parts[interaction.attempt.claim.PartID].value.Tool.Metadata = checkpointInstructionMetadata()
		value.Tools = s.checkpointToolHistory(value)
		if value.Tools == nil || value.Tools.Version != 10 || value.Tools.InteractionFree || !value.Tools.Parts[0].InstructionsLoaded || checkpointReplacementProfile(value) != nil {
			t.Fatal("loaded Read lost original accepted permission", decision)
		}
		original, _ := json.Marshal(value)
		s.predecessor, s.observer = &value, &inputObserver{}
		if decision == PermissionAlways {
			s.restoredAlways = uint32(len(value.Tools.Always))
			s.sessionPermissions = checkpointAppliedPermissions(value.Tools, s.restoredAlways)
		}
		next := nativeCheckpoint{Project: "global", NativeRoot: "/", Previous: []HistoryObservation{copyHistoryObservation(value.History)}}
		next.Tools = s.checkpointToolHistory(next)
		if next.Tools == nil || next.Tools.Version != 10 || checkpointReplacementProfile(next) != nil {
			t.Fatal("text successor lost loaded-instruction lineage")
		}
		next.Tools.Parts[0].InstructionsLoaded = false
		if validCheckpointTools(next) {
			t.Fatal("version 10 gained authority without original loaded Read")
		}
		next.Tools.Parts[0].InstructionsLoaded, next.Tools.Parts[0].Failed = true, true
		if validCheckpointTools(next) {
			t.Fatal("failed Read gained loaded-instruction authority")
		}
		after, _ := json.Marshal(value)
		if !bytes.Equal(original, after) {
			t.Fatal("successor changed original instruction/permission proof")
		}
	}
	value, _ := checkpointToolsFixture()
	value.Tools.Version, value.Tools.Parts[1].InstructionsLoaded = 10, true
	if validCheckpointTools(value) {
		t.Fatal("non-Read tool gained loaded-instruction authority")
	}
}

func TestCheckpointLoadedInstructionsComposeWithOriginalRejectionHistory(t *testing.T) {
	prior, _, _ := checkpointRejectionFixture(t, checkpointCorrection("private correction"), true)
	original, _ := json.Marshal(prior)
	value, s := checkpointToolsFixture()
	value.History.RequestID, value.History.SessionID = domain.NewID(), prior.History.SessionID
	part := value.History.Messages[0].Parts[0]
	s.observer.parts[part.ID].value.Tool.Metadata = checkpointInstructionMetadata()
	s.predecessor = &prior
	value.Previous = []HistoryObservation{copyHistoryObservation(prior.History)}
	value.Tools = s.checkpointToolHistory(value)
	if value.Tools == nil || value.Tools.Version != 10 || len(value.Tools.Rejections) != 1 || len(value.Tools.RejectionPolicy) != 1 || checkpointReplacementProfile(value) != nil {
		t.Fatal("loaded Read dropped original rejection/correction context")
	}
	value.Tools.RejectionPolicy[0].Sources[0] = "changed"
	after, _ := json.Marshal(prior)
	if !bytes.Equal(original, after) {
		t.Fatal("loaded Read continuation mutated original rejection evidence")
	}
}
