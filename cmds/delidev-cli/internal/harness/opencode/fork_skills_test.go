// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
	"strings"
	"testing"
)

func TestForkSkillProfileRejectsOtherToolsAndRetainedPermissions(t *testing.T) {
	original := checkpointToolHistory{Version: 14, InteractionFree: true, Parts: []checkpointToolPart{{Name: checkpointSkillTool}}}
	if !forkSkillToolProfile(&original) {
		t.Fatal("closed skill-only profile rejected")
	}
	for _, change := range []string{"other-tool", "older-proof", "failed-tool", "interaction", "permission", "rejection", "loaded-instructions"} {
		t.Run(change, func(t *testing.T) {
			proof := original
			proof.Parts = append([]checkpointToolPart(nil), original.Parts...)
			switch change {
			case "other-tool":
				proof.Parts[0].Name = checkpointShellTool
			case "older-proof":
				proof.Version = 13
			case "failed-tool":
				proof.Parts[0].Failed = true
			case "interaction":
				proof.InteractionFree = false
			case "permission":
				proof.Once = []SessionClaim{{}}
			case "rejection":
				proof.Rejections = []checkpointPermissionRejection{{}}
			case "loaded-instructions":
				proof.Parts[0].InstructionsLoaded = true
			}
			if forkSkillToolProfile(&proof) {
				t.Fatal("unsupported native history became eligible")
			}
		})
	}
}

func TestForkSkillProofUpdatesOnlyAuthenticatedPartAndRetainsCommandIdentity(t *testing.T) {
	fixture := newHistoryFixture(t)
	history, err := fixture.api.InspectHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before := history.Messages[0].Parts[0].Digest
	command := &SkillCommandProof{Name: "selected", Location: "/original/opencode/skills/selected/SKILL.md", ContentRevision: strings.Repeat("a", 64), ArgumentsSHA256: strings.Repeat("b", 64), TextSHA256: strings.Repeat("c", 64), PartSHA256: before, EventID: "evt_01960dcbe1fc1234567890ABCD", InputID: history.InputID, PartID: history.Messages[0].Parts[0].ID, AssistantID: history.AssistantID}
	history.SkillCommand = command
	original := copyForkHistories([]HistoryObservation{history})
	proof := forkSkillRelocation{MessageID: history.InputID, PartID: command.PartID, BeforeSHA256: before, AfterSHA256: strings.Repeat("d", 64), TextSHA256: strings.Repeat("e", 64), TargetLocation: "/child/opencode/skills/selected/SKILL.md"}
	next, err := applyForkSkillProofs(copyForkHistories(original), []forkSkillRelocation{proof})
	if err != nil {
		t.Fatal(err)
	}
	updated := next[0].SkillCommand
	if updated.Location != proof.TargetLocation || updated.PartSHA256 != proof.AfterSHA256 || updated.TextSHA256 != proof.TextSHA256 || updated.EventID != command.EventID || updated.ArgumentsSHA256 != command.ArgumentsSHA256 || updated.ContentRevision != command.ContentRevision {
		t.Fatal("relocation changed original command identity or arguments")
	}
	if next[0].Messages[1].Digest != history.Messages[1].Digest || next[0].Messages[1].Parts[0] != history.Messages[1].Parts[0] || original[0].SkillCommand.Location != command.Location || original[0].Messages[0].Parts[0].Digest != before {
		t.Fatal("relocation rewrote unrelated or original history")
	}
	proof.BeforeSHA256 = strings.Repeat("f", 64)
	if _, err := applyForkSkillProofs(copyForkHistories(original), []forkSkillRelocation{proof}); err == nil {
		t.Fatal("foreign original part digest authorized relocation")
	}
}

func TestForkSkillCommandRelocatesRetainedHistoryAfterContinuation(t *testing.T) {
	observer, _ := nativeSkillToolFixture(t)
	current := observer.skillPackages[0]
	target, err := skills.CopyOpenCodeRuntime(context.Background(), []skills.NativeSelection{current.NativeSelection}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	retiredLocation := filepath.Join(t.TempDir(), "retired", "opencode", "skills", current.Name, "SKILL.md")
	marker := "\n\nBase directory for this skill: " + filepath.Dir(retiredLocation) + "\nRelative paths in this skill (e.g., scripts/, references/) are relative to this base directory."
	unrelated := "\nRetain this original prose path: " + filepath.Dir(retiredLocation)
	text := current.Content + marker + unrelated
	part, _ := json.Marshal(fixturePart(TextPartKind, map[string]any{"text": text}))
	raw, _ := json.Marshal(map[string]any{"parts": []json.RawMessage{part}})
	command := &SkillCommandProof{Name: current.Name, Location: retiredLocation, ContentRevision: current.ContentRevision, PartID: fixturePartID, PartSHA256: mutationDigest(canonicalNative(part)), TextSHA256: mutationDigest([]byte(text))}
	source := nativeCheckpoint{Skills: retainedCheckpointSkills(observer.skillPackages), Previous: []HistoryObservation{{SkillCommand: command}}}
	patches, err := prepareForkSkillPatches(context.Background(), source, []json.RawMessage{raw}, []json.RawMessage{raw}, target)
	if err != nil || len(patches) != 1 {
		t.Fatalf("retained command relocation: %v", err)
	}
	patched, err := decodeNativePart(patches[0].body)
	if err != nil || patched.Text == nil || patched.Text.Text != current.Content+strings.Replace(marker, filepath.Dir(retiredLocation), filepath.Dir(target[0].Path), 1)+unrelated || patches[0].proof.SourceLocation != retiredLocation {
		t.Fatal("historical location or unrelated text was not retained", err)
	}
	command.PartSHA256 = strings.Repeat("f", 64)
	if _, err := prepareForkSkillPatches(context.Background(), source, []json.RawMessage{raw}, []json.RawMessage{raw}, target); err == nil {
		t.Fatal("changed original history authorized relocation")
	}
}
